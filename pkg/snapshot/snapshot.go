// Package snapshot keeps a thumbnail of every visible window
// (specs/008-window-snapshots). A window is captured when its contents
// change, at most once an interval: its pixmap, named through Composite, is
// bound as a texture (GLX_EXT_texture_from_pixmap) in a GL context of the
// package's own, averaged by area on the GPU to at most 512×512, and read
// back. A pixmap the GPU will not bind — another client holds a GLX pixmap of
// the window — is scaled in the X server by RENDER instead
// (specs/018-snapshot-bind-conflicts). Windows the window manager unmaps keep
// their last thumbnail. While the switcher is shown, the windows that change
// are averaged again into textures the presenter draws: the live thumbnails of
// live.go (specs/020-live-thumbnails).
package snapshot

import (
	"errors"
	"fmt"
	"image"
	"sync"
	"sync/atomic"
	"time"

	"github.com/almaz-uno/qws/pkg/composite"
	"github.com/almaz-uno/qws/pkg/glx"
	"github.com/almaz-uno/qws/pkg/x11"
	"github.com/go-gl/gl/v4.6-core/gl"
	"github.com/jezek/xgb"
	xcomposite "github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/damage"
	"github.com/jezek/xgb/xfixes"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog/log"
)

// Causes of a snapshot, as logged
const (
	causeChange     = "change"     // the contents changed, or the window became viewable
	causeActivation = "activation" // the switcher asked for it
)

// Snapshotter keeps the thumbnails. It has an X connection of its own, so
// that the event loop of the switcher does not read its events, and a GL
// context of its own, current on a thread of its own.
type Snapshotter struct {
	conn     *xgb.Conn
	root     xproto.Window
	interval time.Duration
	atoms    struct{ clientList, live xproto.Atom }
	share    *glx.Share // the GL context, for the presenter's to share

	mu       sync.RWMutex
	thumbs   map[xproto.Window]image.Image
	thumbGen map[xproto.Window]uint64 // the generation of each thumbnail

	// The live thumbnails shared with the presenter's thread
	// (specs/020-live-thumbnails, live.go), under mu: the pictures published,
	// the publications so far, those a frame being drawn took them after, the
	// fence after the last frame that drew any, whether the switcher has been
	// woken since its last frame, and what SetLive asked for
	pics      map[xproto.Window]Picture
	seq       uint64
	fetchSeq  uint64
	inFrame   bool
	presFence uintptr
	woken     bool
	lastEnd   time.Time       // the end of the last frame
	prevEnd   time.Time       // and of the one before
	endWanted bool            // a pass waits for the end of the frame: EndFrame wakes the loop
	shown     []xproto.Window // the windows of the last frame, those the switcher shows
	liveWant  liveSession

	paused   atomic.Bool
	wake     chan struct{} // the pause ended
	liveWake chan struct{} // SetLive was called
	frameEnd chan struct{} // EndFrame was called, a pass waiting for it
	events   chan xgb.Event
	refresh  chan chan struct{}
	quit     chan struct{}
	done     chan struct{}

	// Owned by the loop
	windows map[xproto.Window]*window
	frames  map[xproto.Window]xproto.Window // frame of the window manager → its client
	// The switchers of the qws instances, for the pause (specs/011-snapshot-pause)
	switchers switchers
	off       *glx.Offscreen
	gpu       *gpu
	render    *xrender // nil without RENDER or MIT-SHM
	cpu       *composite.Capturer
	live      liveSession     // the live thumbnails taken; overlay 0: none
	lastPass  time.Time       // the last live pass
	hold      time.Duration   // liveHold; 0 in a test: no pass waits for a frame
	guard     time.Duration   // liveGuard
	liveRetry time.Time       // a pass a frame held back is tried again then
	trash     []*liveTextures // live textures to delete once no frame is drawn
}

// window is what the snapshotter knows of a client window
type window struct {
	id       xproto.Window
	frame    xproto.Window // its parent, when not the root
	visual   xproto.Visualid
	damage   damage.Damage
	schedule schedule

	// Mapped, the window and its frame: viewable, as far as the events tell,
	// without asking the X server at each capture
	mapped, frameMapped bool

	// The pixmap bound as a texture; stale after a map, an unmap or a
	// change of size, when Composite gives the window a new pixmap. Named but
	// not bound, when the GPU would not bind it: taken by RENDER until named
	// anew
	stale         bool
	retried       int // captures retried since the last that worked
	pixmap        xproto.Pixmap
	texture       uint32
	bound         *glx.TexturePixmap
	width, height int
	depth         int

	// The ancestor whose pixmap the window is drawn in and taken from, when
	// the X server does not redirect the window itself, 0 when the pixmap is
	// its own; the visual and the depth of the pixmap named
	// (specs/022-uncaptured-windows)
	via       xproto.Window
	pixVisual xproto.Visualid
	pixDepth  int

	// The live thumbnails (specs/020-live-thumbnails): the pictures so far,
	// snapshots and passes, which number their generations; the textures of
	// the passes, nil before the first; when it is due one
	pictures uint64
	live     *liveTextures
	liveDue  liveSchedule
}

// errNotViewable: the X server would not name the window's pixmap — it is not
// viewable after all, as while i3 switches workspaces and the events run
// ahead of the server
var errNotViewable = errors.New("window not viewable")

// retries bounds the captures retried a settle later after errNotViewable
const retries = 3

// errNotBound: the GPU would not bind the window's pixmap — another client
// holds a GLX pixmap of the window, or no FBConfig has its depth
var errNotBound = errors.New("pixmap not bound on the GPU")

// errNotRedirected: the window is viewable, but neither its pixmap nor that
// of an ancestor can be named — without a compositor, a window the X server
// does not redirect on its own (specs/022-uncaptured-windows)
var errNotRedirected = errors.New("no pixmap of the window or of an ancestor")

// newOffscreen is the GL context of the snapshots; a variable, so that a test
// can make it fail
var newOffscreen = glx.NewOffscreen

// New starts the snapshotter. It fails without Composite, DAMAGE, GLX 4.6 or
// GLX_EXT_texture_from_pixmap; the caller then keeps the snapshots of 1.0.0.
// scaling is the algorithm of pkg/composite, for the windows neither the GPU
// nor RENDER can take.
func New(interval time.Duration, scaling string) (*Snapshotter, error) {
	conn, err := x11.NewConn()
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	return start(conn, xproto.Setup(conn).DefaultScreen(conn).Root, interval, scaling)
}

// start runs a snapshotter on conn of the client windows listed on root, the
// root window but in a test; conn is closed should it fail
func start(conn *xgb.Conn, root xproto.Window, interval time.Duration, scaling string) (*Snapshotter, error) {
	s := &Snapshotter{
		conn:      conn,
		root:      root,
		interval:  interval,
		thumbs:    make(map[xproto.Window]image.Image),
		thumbGen:  make(map[xproto.Window]uint64),
		pics:      make(map[xproto.Window]Picture),
		wake:      make(chan struct{}, 1),
		liveWake:  make(chan struct{}, 1),
		frameEnd:  make(chan struct{}, 1),
		hold:      liveHold,
		guard:     liveGuard,
		switchers: noSwitchers{},
		events:    make(chan xgb.Event, 256),
		refresh:   make(chan chan struct{}),
		quit:      make(chan struct{}),
		done:      make(chan struct{}),
		windows:   make(map[xproto.Window]*window),
		frames:    make(map[xproto.Window]xproto.Window),
	}
	if err := s.initX(scaling); err != nil {
		conn.Close()
		return nil, fmt.Errorf("snapshot: %w", err)
	}

	ready := make(chan error)
	go s.run(ready)
	if err := <-ready; err != nil {
		conn.Close()
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	go s.read()
	return s, nil
}

// initX checks the extensions, set up with the connection
// (specs/021-xgb-extension-init), and watches the client list
func (s *Snapshotter) initX(scaling string) error {
	if err := x11.CheckExtension(s.conn, "Composite"); err != nil {
		return fmt.Errorf("Composite: %w", err)
	}
	if _, err := xcomposite.QueryVersion(s.conn, 0, 4).Reply(); err != nil {
		return fmt.Errorf("Composite: %w", err)
	}
	if err := x11.CheckExtension(s.conn, "XFIXES"); err != nil {
		return fmt.Errorf("XFIXES: %w", err)
	}
	if _, err := xfixes.QueryVersion(s.conn, 5, 0).Reply(); err != nil {
		return fmt.Errorf("XFIXES: %w", err)
	}
	if err := x11.CheckExtension(s.conn, "DAMAGE"); err != nil {
		return fmt.Errorf("DAMAGE: %w", err)
	}
	if _, err := damage.QueryVersion(s.conn, 1, 1).Reply(); err != nil {
		return fmt.Errorf("DAMAGE: %w", err)
	}
	name := "_NET_CLIENT_LIST"
	atom, err := xproto.InternAtom(s.conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return err
	}
	s.atoms.clientList = atom.Atom
	atom, err = xproto.InternAtom(s.conn, false, uint16(len(LiveAtom)), LiveAtom).Reply()
	if err != nil {
		return err
	}
	s.atoms.live = atom.Atom
	if err := xproto.ChangeWindowAttributesChecked(s.conn, s.root, xproto.CwEventMask,
		[]uint32{xproto.EventMaskPropertyChange}).Check(); err != nil {
		return err
	}
	if sw, err := x11.NewSwitchers(s.conn, s.root); err == nil {
		s.switchers = sw
	} else {
		log.Debug().Err(err).Msg("Switchers of other instances not followed")
	}
	cpu, err := composite.NewCapturer(s.conn, s.root, scaling)
	if err != nil {
		return err
	}
	s.cpu = cpu
	return nil
}

// read passes the events of the connection to the loop
func (s *Snapshotter) read() {
	for {
		ev, err := s.conn.WaitForEvent()
		if ev == nil && err == nil {
			return // connection closed
		}
		if ev != nil {
			select {
			case s.events <- ev:
			case <-s.quit:
				return
			}
		}
	}
}

// Thumbnail is the last thumbnail of the window
func (s *Snapshotter) Thumbnail(w xproto.Window) (image.Image, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	img, ok := s.thumbs[w]
	return img, ok
}

// Refresh captures every viewable window changed since its snapshot, at once,
// waiting for them at most timeout — for an activation, before its first
// frame
func (s *Snapshotter) Refresh(timeout time.Duration) {
	done := make(chan struct{})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case s.refresh <- done:
	case <-timer.C:
		return
	}
	select {
	case <-done:
	case <-timer.C:
	}
}

// Pause stops or resumes the snapshots taken on change: they are paused while
// the switcher is shown (specs/007-animation), and while that of another qws
// instance is (specs/011-snapshot-pause); Refresh still captures, and the
// live passes between two SetLive run paused or not, but for the switchers of
// the other instances (specs/020-live-thumbnails)
func (s *Snapshotter) Pause(paused bool) {
	s.paused.Store(paused)
	if !paused {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// pausedNow reports whether the snapshots on change are paused: for this
// instance's switcher or another's. In the loop only.
func (s *Snapshotter) pausedNow() bool {
	return s.paused.Load() || s.switchers != nil && s.switchers.Shown(0)
}

// switchers is what the snapshotter needs of the switchers of the qws
// instances, x11.Switchers, an interface so that a test can give switchers of
// its own; nil, or noSwitchers, follows none
type switchers interface {
	// Handle takes an event and reports whether it was about the switchers
	Handle(ev xgb.Event) bool
	// Shown reports whether a switcher is shown, but the overlay except
	Shown(except xproto.Window) bool
}

// noSwitchers are the switchers where none can be followed
type noSwitchers struct{}

func (noSwitchers) Handle(xgb.Event) bool    { return false }
func (noSwitchers) Shown(xproto.Window) bool { return false }

// wait is how long the loop waits for a window due at due, at now: no
// longer than till then, and not at all once that has passed; while paused,
// or with no window due, an hour — the loop wakes for its events, an
// activation or the end of the pause. Armed for a window due while paused, the
// timer fired at once and the loop spun (specs/011-snapshot-pause).
func wait(paused bool, due time.Time, ok bool, now time.Time) time.Duration {
	if paused || !ok {
		return time.Hour
	}
	return max(0, due.Sub(now))
}

// Close stops the snapshotter and frees what it holds in the X server and the
// GPU
func (s *Snapshotter) Close() {
	close(s.quit)
	<-s.done
	s.conn.Close()
}

// run is the loop, on the thread of the GL context
func (s *Snapshotter) run(ready chan<- error) {
	defer close(s.done)
	off, err := newOffscreen()
	if err != nil {
		ready <- err
		return
	}
	defer off.Destroy()
	g, err := newGPU()
	if err != nil {
		ready <- err
		return
	}
	defer g.close()
	s.off, s.gpu, s.share = off, g, off.Share()
	if x, err := newXRender(s.conn, s.root); err != nil {
		log.Info().Err(err).Msg("RENDER unavailable, windows the GPU will not bind are captured on the CPU")
	} else {
		defer x.close()
		s.render = x
	}

	s.reconcile()
	ready <- nil

	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		if s.live.overlay != 0 {
			timer.Reset(s.liveWait(time.Now()))
		} else {
			due, ok := s.nextDue()
			timer.Reset(wait(s.pausedNow(), due, ok, time.Now()))
		}
		select {
		case ev := <-s.events:
			s.handle(ev)
		case <-s.wake:
		case <-s.liveWake:
			s.setLive()
		case <-s.frameEnd:
			if s.live.overlay != 0 {
				s.liveTick(time.Now(), true)
			}
		case done := <-s.refresh:
			s.captureChanged(time.Now(), causeActivation, true)
			close(done)
		case <-timer.C:
			s.tick(time.Now())
		case <-s.quit:
			for id := range s.windows {
				s.forget(id)
			}
			s.emptyTrash()
			if s.presFence != 0 {
				gl.DeleteSync(s.presFence)
			}
			return
		}
	}
}

// tick is the work of the timer: while live, the live passes and no snapshot
// on change (specs/020-live-thumbnails); else the snapshots due, unless paused
func (s *Snapshotter) tick(now time.Time) {
	switch {
	case s.live.overlay != 0:
		s.liveTick(now, false)
	case !s.pausedNow():
		s.captureChanged(now, causeChange, false)
	}
}

// nextDue is when the next window is to be captured
func (s *Snapshotter) nextDue() (time.Time, bool) {
	var next time.Time
	found := false
	for _, w := range s.windows {
		if due, ok := w.schedule.due(s.interval); ok && (!found || due.Before(next)) {
			next, found = due, true
		}
	}
	return next, found
}

// captureChanged captures the changed windows that are due at now — all of
// them when now is an activation's
func (s *Snapshotter) captureChanged(now time.Time, cause string, all bool) {
	for _, w := range s.windows {
		due, ok := w.schedule.due(s.interval)
		if ok && (all || !due.After(now)) {
			s.capture(w, cause)
		}
	}
}

// handle follows the windows: the client list, their structure and that of
// their frames, their damage
func (s *Snapshotter) handle(ev xgb.Event) {
	if s.switchers != nil && s.switchers.Handle(ev) {
		return
	}
	now := time.Now()
	switch e := ev.(type) {
	case xproto.PropertyNotifyEvent:
		if e.Window == s.root && e.Atom == s.atoms.clientList {
			s.reconcile()
		}
	case damage.NotifyEvent:
		if w, ok := s.windows[xproto.Window(e.Drawable)]; ok {
			w.schedule.change(now)
			w.liveDue.change(now)
		}
	case xproto.MapNotifyEvent:
		s.setMapped(e.Window, true)
		s.restructured(e.Window, now)
	case xproto.UnmapNotifyEvent:
		s.setMapped(e.Window, false)
		if w := s.client(e.Window); w != nil {
			s.release(w)
			w.stale = true
		}
	case xproto.ConfigureNotifyEvent:
		if w, ok := s.windows[e.Window]; ok && (int(e.Width) != w.width || int(e.Height) != w.height) {
			s.restructured(e.Window, now)
		} else if w := s.client(e.Window); w != nil && w.via != 0 && w.via == e.Window {
			// The frame the window is taken from: of a new size, a new pixmap
			s.restructured(e.Window, now)
		}
	case xproto.ReparentNotifyEvent:
		if w, ok := s.windows[e.Window]; ok {
			delete(s.frames, w.frame)
			s.watchFrame(w, e.Parent)
			s.restructured(e.Window, now)
		}
	case xproto.DestroyNotifyEvent:
		if _, ok := s.windows[e.Window]; ok {
			s.forget(e.Window)
		}
	}
}

// restructured marks the window of a client or a frame for a new pixmap and a
// capture: it was mapped or changed size
func (s *Snapshotter) restructured(id xproto.Window, now time.Time) {
	if w := s.client(id); w != nil {
		w.stale = true
		w.schedule.change(now)
	}
}

// setMapped records the map state of a client window or a frame
func (s *Snapshotter) setMapped(id xproto.Window, mapped bool) {
	if w, ok := s.windows[id]; ok {
		w.mapped = mapped
	} else if w := s.client(id); w != nil {
		w.frameMapped = mapped
	}
}

// viewable reports whether the window is viewable, as far as the events tell
func (w *window) viewable() bool {
	return w.mapped && (w.frame == 0 || w.frameMapped)
}

// client is the window a client window or a frame stands for
func (s *Snapshotter) client(id xproto.Window) *window {
	if w, ok := s.windows[id]; ok {
		return w
	}
	if c, ok := s.frames[id]; ok {
		return s.windows[c]
	}
	return nil
}

// reconcile follows the windows of _NET_CLIENT_LIST, new and gone
func (s *Snapshotter) reconcile() {
	reply, err := xproto.GetProperty(s.conn, false, s.root, s.atoms.clientList,
		xproto.AtomWindow, 0, 4096).Reply()
	if err != nil {
		return
	}
	listed := make(map[xproto.Window]bool)
	now := time.Now()
	for i := 0; i+4 <= len(reply.Value); i += 4 {
		id := xproto.Window(xgb.Get32(reply.Value[i:]))
		listed[id] = true
		if _, ok := s.windows[id]; !ok {
			s.follow(id, now)
		}
	}
	for id := range s.windows {
		if !listed[id] {
			s.forget(id)
		}
	}
}

// follow starts following a client window: its structure, its frame's, and
// its damage; it is captured once it settles
func (s *Snapshotter) follow(id xproto.Window, now time.Time) {
	if err := xproto.ChangeWindowAttributesChecked(s.conn, id, xproto.CwEventMask,
		[]uint32{xproto.EventMaskStructureNotify}).Check(); err != nil {
		return // gone already
	}
	w := &window{id: id, stale: true}
	if attrs, err := xproto.GetWindowAttributes(s.conn, id).Reply(); err == nil {
		w.mapped = attrs.MapState != xproto.MapStateUnmapped
		w.visual = attrs.Visual
	}
	if d, err := damage.NewDamageId(s.conn); err == nil {
		if damage.CreateChecked(s.conn, d, xproto.Drawable(id), damage.ReportLevelNonEmpty).Check() == nil {
			w.damage = d
		}
	}
	if tree, err := xproto.QueryTree(s.conn, id).Reply(); err == nil {
		s.watchFrame(w, tree.Parent)
	}
	s.windows[id] = w
	w.schedule.change(now)
}

// watchFrame follows the structure of the window's parent, when it is a frame
// of the window manager: i3 hides a window by unmapping its frame
func (s *Snapshotter) watchFrame(w *window, parent xproto.Window) {
	w.frame = 0
	if parent == s.root || parent == 0 {
		return
	}
	if xproto.ChangeWindowAttributesChecked(s.conn, parent, xproto.CwEventMask,
		[]uint32{xproto.EventMaskStructureNotify}).Check() == nil {
		w.frame = parent
		s.frames[parent] = w.id
		if attrs, err := xproto.GetWindowAttributes(s.conn, parent).Reply(); err == nil {
			w.frameMapped = attrs.MapState != xproto.MapStateUnmapped
		}
	}
}

// forget stops following a window and drops its thumbnail
func (s *Snapshotter) forget(id xproto.Window) {
	w, ok := s.windows[id]
	if !ok {
		return
	}
	s.release(w)
	s.dropLive(w)
	if w.texture != 0 {
		gl.DeleteTextures(1, &w.texture)
	}
	if w.damage != 0 {
		damage.Destroy(s.conn, w.damage)
	}
	delete(s.frames, w.frame)
	delete(s.windows, id)
	s.mu.Lock()
	delete(s.thumbs, id)
	delete(s.thumbGen, id)
	s.mu.Unlock()
}

// release unbinds and frees the pixmap of the window
func (s *Snapshotter) release(w *window) {
	if w.bound != nil {
		w.bound.Release()
		w.bound = nil
	}
	if w.pixmap != 0 {
		xproto.FreePixmap(s.conn, w.pixmap)
		w.pixmap = 0
	}
}

// capture takes a thumbnail of the window, if it is viewable
func (s *Snapshotter) capture(w *window, cause string) {
	start := time.Now()
	// Changes from here on report again, viewable or not: DAMAGE reports
	// only the first change after a subtraction
	if w.damage != 0 {
		damage.Subtract(s.conn, w.damage, 0, 0)
	}
	if !w.viewable() {
		// Not viewable: no contents; it is captured when it changes again
		s.release(w)
		w.stale = true
		w.schedule.dirty = false
		return
	}

	path := "gpu"
	img, err := s.captureGPU(w)
	if errors.Is(err, errNotViewable) {
		// Again a settle later, a few times; then at its next change
		w.schedule.shot(time.Now())
		if w.retried++; w.retried <= retries {
			w.schedule.change(time.Now())
			w.schedule.lastShot = time.Time{}
		}
		return
	}
	w.retried = 0
	if errors.Is(err, errNotBound) && s.render != nil {
		path = "render"
		var r image.Rectangle
		if r, err = s.pixmapRect(w); err == nil {
			img, err = s.render.thumbnail(w.pixmap, w.pixVisual, w.pixDepth, r)
		}
	}
	if err != nil {
		log.Debug().Err(err).Uint32("window", uint32(w.id)).Str("path", path).Msg("Snapshot failed, taken on the CPU")
		path = "cpu"
		var cimg image.Image
		if cimg, err = s.cpu.CaptureWindow(w.id, maxSide, maxSide); err == nil {
			s.store(w, cimg)
		}
	} else {
		s.store(w, img)
	}
	w.schedule.shot(time.Now())
	if err != nil {
		return
	}
	e := log.Debug()
	if w.via != 0 {
		e = e.Uint32("via", uint32(w.via))
	}
	e.Uint32("window", uint32(w.id)).
		Int("width", w.width).
		Int("height", w.height).
		Str("path", path).
		Str("cause", cause).
		Dur("ms", time.Since(start)).
		Msg("Snapshot")
}

// captureGPU averages the window's pixmap on the GPU, naming and binding it
// anew when it is stale. A pixmap the GPU would not bind stays named, for
// RENDER, and is bound again only once named anew: the client holding a GLX
// pixmap of the window holds it, as qws does, while the window keeps its
// pixmap (specs/018-snapshot-bind-conflicts).
func (s *Snapshotter) captureGPU(w *window) (*image.RGBA, error) {
	if w.texture == 0 {
		w.texture = newWindowTexture()
	}
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, w.texture)

	switch {
	case w.stale:
		s.release(w)
		geom, err := xproto.GetGeometry(s.conn, xproto.Drawable(w.id)).Reply()
		if err != nil {
			return nil, err
		}
		w.width, w.height, w.depth = int(geom.Width), int(geom.Height), int(geom.Depth)
		if err := s.namePixmap(w); err != nil {
			return nil, err
		}
		w.stale = false
		if w.via != 0 {
			// The pixmap of a frame is the compositor's, bound on the GPU by
			// it: taken by RENDER, never bound by qws, which would hold the one
			// GLX pixmap its storage may have (specs/018-snapshot-bind-conflicts)
			log.Debug().Uint32("window", uint32(w.id)).Uint32("via", uint32(w.via)).
				Msg("No pixmap of its own: taken from its frame by RENDER")
			return nil, errNotBound
		}
		bound, err := s.off.BindPixmap(uint32(w.pixmap), w.depth)
		if err != nil {
			log.Debug().Err(err).Uint32("window", uint32(w.id)).Msg("Pixmap not bound on the GPU until named anew")
			return nil, errNotBound
		}
		w.bound = bound
	case w.bound == nil:
		return nil, errNotBound
	default:
		w.bound.Rebind()
	}
	return s.gpu.thumbnail(w.width, w.height, w.bound.YInverted), nil
}

// namePixmap names the pixmap the window is drawn in: its own, when the X
// server redirects it — as it does a window whose visual differs from its
// frame's, one of the two the 32-bit ARGB visual of Composite — and else,
// when the window is viewable, the pixmap of its nearest ancestor below the
// root that can be named: its frame, which the compositor redirects
// (specs/022-uncaptured-windows). errNotViewable when the window is not
// viewable, errNotRedirected when no ancestor's pixmap can be named.
func (s *Snapshotter) namePixmap(w *window) error {
	w.via, w.pixVisual, w.pixDepth = 0, w.visual, w.depth
	pixmap, err := xproto.NewPixmapId(s.conn)
	if err != nil {
		return err
	}
	refusal := xcomposite.NameWindowPixmapChecked(s.conn, w.id, pixmap).Check()
	if refusal == nil {
		w.pixmap = pixmap
		return nil
	}
	// BadMatch either way: not viewable, or not redirected
	attrs, err := xproto.GetWindowAttributes(s.conn, w.id).Reply()
	if err != nil || attrs.MapState != xproto.MapStateViewable {
		return fmt.Errorf("%w: %v", errNotViewable, refusal)
	}
	for id := w.id; ; {
		tree, err := xproto.QueryTree(s.conn, id).Reply()
		if err != nil {
			return err
		}
		if tree.Parent == 0 || tree.Parent == s.root {
			return errNotRedirected
		}
		id = tree.Parent
		// The ID of a pixmap not named stays free for the next attempt
		if xcomposite.NameWindowPixmapChecked(s.conn, id, pixmap).Check() != nil {
			continue
		}
		geom, err := xproto.GetGeometry(s.conn, xproto.Drawable(id)).Reply()
		if err == nil {
			attrs, err = xproto.GetWindowAttributes(s.conn, id).Reply()
		}
		if err != nil {
			xproto.FreePixmap(s.conn, pixmap)
			return err
		}
		w.pixmap, w.via = pixmap, id
		w.pixVisual, w.pixDepth = attrs.Visual, int(geom.Depth)
		return nil
	}
}

// pixmapRect is where the window lies in the pixmap named for it: all of its
// own, but for its border; in its ancestor's, at its offset from the
// ancestor's origin, asked at each capture — i3 moves a window in its frame,
// and its ConfigureNotify events in root coordinates do not tell — and past
// the ancestor's border, which the pixmap holds
func (s *Snapshotter) pixmapRect(w *window) (image.Rectangle, error) {
	r := image.Rect(0, 0, w.width, w.height)
	if w.via == 0 {
		return r, nil
	}
	pos, err := xproto.TranslateCoordinates(s.conn, w.id, w.via, 0, 0).Reply()
	if err != nil {
		return image.Rectangle{}, err
	}
	geom, err := xproto.GetGeometry(s.conn, xproto.Drawable(w.via)).Reply()
	if err != nil {
		return image.Rectangle{}, err
	}
	b := int(geom.BorderWidth)
	return r.Add(image.Pt(int(pos.DstX)+b, int(pos.DstY)+b)), nil
}

// store keeps the thumbnail of the window, a picture of a new generation
func (s *Snapshotter) store(w *window, img image.Image) {
	w.pictures++
	s.mu.Lock()
	s.thumbs[w.id] = img
	s.thumbGen[w.id] = w.pictures
	s.mu.Unlock()
}
