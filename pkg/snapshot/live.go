package snapshot

import (
	"image"
	"time"

	"github.com/almaz-uno/qws/pkg/glx"
	"github.com/go-gl/gl/v4.6-core/gl"
	"github.com/jezek/xgb/damage"
	"github.com/jezek/xgb/render"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog/log"
)

// Live thumbnails (specs/020-live-thumbnails). While the switcher is shown —
// from SetLive with its overlay to SetLive with none — a viewable window that
// changes is averaged again on the GPU, without the settle of a snapshot and
// without a read back, into one of two thumbnail textures of its own: a
// window at most once a live interval, the window that has waited longest
// first, one window a pass, the passes spread over the interval. The
// presenter's GL context shares the snapshotter's (Share) and draws the latest
// picture of a window over its card; a pass writes the other texture.
//
// The commands of two contexts run in no order of each other (OpenGL 4.6,
// 5.3), so the textures change hands by fences:
//
//   - a pass ends with a fence, tested on the snapshotter's thread; only once
//     it has signalled is its texture published, made the window's picture:
//     the presenter draws complete pictures only, and never waits;
//   - a pass writes the texture the presenter drew until the last publication
//     of the window. It is not made while a frame that took the pictures
//     before that publication is being drawn — between BeginFrame and
//     EndFrame — and it waits, on the GPU, for the fence the presenter made
//     after the last frame that drew live pictures.
//
// Snapshots keep their framebuffer of 008 and are read back from it: a window
// gets its live textures at its first pass. Each picture of a window, snapshot
// or pass, has a generation, so that a live picture is drawn only over a card
// drawn from an older snapshot.

// LiveAtom names the ClientMessage the snapshotter sends the overlay when a
// live picture is published, so that a switcher at rest presents a frame
const LiveAtom = "_QWS_LIVE"

// Picture is the live thumbnail of a window: a texture of the share group of
// the snapshotter's GL context, complete, its first row the top row of the
// window
type Picture struct {
	Texture       uint32
	Width, Height int
	Gen           uint64    // among the pictures of the window, snapshots and passes: larger is newer
	Changed       time.Time // when the DAMAGE event of the first change it shows was read
}

// livePoll is how often the fences of the passes under way are tested, and
// a pass a frame holds back is tried again
const livePoll = 500 * time.Microsecond

// While frames come one after another — the last two less than liveHold
// apart, the last less than liveHold ago: a step, a hover moving — no pass
// is made; one due waits for them to stop, liveHold after the last. In
// the pauses between the frames of a step, 4 ms before the next frame at
// the latest, as first made, the passes held the frames off the screen
// (research, "Passes in the pauses"), but not on the desktop of E1: two
// windows changing all the time, passed 60 times a second, made the steps
// of the grid miss 5.63 % and 8.14 % of their frames against 0.68 and
// 0.91 % without — the work of the X server for a pass, binding again or
// scaling by RENDER, delays the frames of the overlay after it (research,
// "S5 at rest"). The pictures of a frame in motion are those published
// before it.
const liveHold = 10 * time.Millisecond

// passAt is when a pass due at due is made, at now: at once, or when due,
// in a pause between frames; while frames come one after another, given the
// ends of the last two, once they have stopped
func passAt(now, due, last, prev time.Time, hold time.Duration) time.Time {
	t := due
	if t.Before(now) {
		t = now
	}
	period := last.Sub(prev)
	if prev.IsZero() || period <= 0 || period >= hold {
		// Frames do not come one after another
		return t
	}
	if stop := last.Add(hold); stop.After(t) {
		return stop
	}
	return t
}

// liveSession is what SetLive asks for: the overlay of the switcher shown, 0
// for none, and the live interval
type liveSession struct {
	overlay  xproto.Window
	interval time.Duration
}

// liveTextures are the two thumbnail textures of a window, written in turn,
// and the pass under way
type liveTextures struct {
	texture, fbo  [2]uint32
	width, height int
	cur           int    // the texture published; -1: none yet
	pubSeq        uint64 // the publication that made it the picture

	pending uintptr       // the fence of a pass into the other texture; 0: none
	changed time.Time     // the first change the pass shows
	start   time.Time     // when the pass began
	cpu     time.Duration // the pass on the snapshotter's thread
}

// liveSchedule is when a window is due a live pass
type liveSchedule struct {
	dirty   bool      // changed since its last pass, or since its snapshot
	dirtyAt time.Time // when it first changed since then
	last    time.Time // its last pass; zero: none
}

// change marks a change at at; one on top of one not yet passed keeps the
// earlier time
func (l *liveSchedule) change(at time.Time) {
	if !l.dirty {
		l.dirty, l.dirtyAt = true, at
	}
}

// passed records a pass at at
func (l *liveSchedule) passed(at time.Time) {
	l.dirty, l.last = false, at
}

// due is when the window may be passed: at once after a change, without the
// settle of a snapshot, but no sooner than an interval after its last pass
func (l liveSchedule) due(interval time.Duration) time.Time {
	t := l.dirtyAt
	if !l.last.IsZero() {
		if u := l.last.Add(interval); u.After(t) {
			t = u
		}
	}
	return t
}

// nextPass is which of the windows waiting a pass goes next, and when: no
// sooner than the first of them is due, nor than the interval divided by the
// n windows that take passes after the pass before, at lastPass; of those due
// by then, the one that has waited longest. False when none waits.
//
// n counts the windows waiting and those passed within the last interval,
// which wait again once they change: a window passed alone then would hold
// the next pass of another back by a whole interval.
func nextPass(waiting []liveSchedule, n int, lastPass time.Time, interval time.Duration) (int, time.Time, bool) {
	if len(waiting) == 0 {
		return 0, time.Time{}, false
	}
	n = max(n, len(waiting))
	at := waiting[0].due(interval)
	for _, l := range waiting[1:] {
		if d := l.due(interval); d.Before(at) {
			at = d
		}
	}
	if !lastPass.IsZero() {
		if u := lastPass.Add(interval / time.Duration(n)); u.After(at) {
			at = u
		}
	}
	best := -1
	for i, l := range waiting {
		if !l.due(interval).After(at) && (best < 0 || l.dirtyAt.Before(waiting[best].dirtyAt)) {
			best = i
		}
	}
	return best, at, true
}

// SetLive starts the live thumbnails for the switcher whose overlay is
// shown, a window passed at most once an interval, or, with overlay 0, ends
// them. In between, no snapshot is taken on change. The windows passed are
// those of the last frame begun: the switcher begins one before it starts
// them.
func (s *Snapshotter) SetLive(overlay xproto.Window, interval time.Duration) {
	s.mu.Lock()
	s.liveWant = liveSession{overlay, interval}
	s.mu.Unlock()
	select {
	case s.liveWake <- struct{}{}:
	default:
	}
}

// Share names the snapshotter's GL context, for the presenter's to be created
// sharing its objects
func (s *Snapshotter) Share() *glx.Share {
	return s.share
}

// BeginFrame takes, for a frame the presenter is about to draw, the live
// pictures that are newer than their windows' snapshots, of every window: a
// card on its way out of the frame draws its own. Until EndFrame, no pass
// writes the textures it gave. shown are the windows the switcher shows,
// those the passes are for until the next frame; the snapshotter keeps the
// slice, which the caller does not change after, and gives anew when the
// windows change. A slice other than the last wakes the loop: a window that
// comes into view may wait a pass since it changed while out of view, and
// DAMAGE reports nothing more of it until that pass.
func (s *Snapshotter) BeginFrame(shown []xproto.Window) map[xproto.Window]Picture {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFrame, s.fetchSeq, s.woken = true, s.seq, false
	if len(shown) != len(s.shown) || len(shown) > 0 && &shown[0] != &s.shown[0] {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
	s.shown = shown
	var pics map[xproto.Window]Picture
	for id, p := range s.pics {
		if p.Gen > s.thumbGen[id] {
			if pics == nil {
				pics = make(map[xproto.Window]Picture)
			}
			pics[id] = p
		}
	}
	return pics
}

// EndFrame follows the frame of BeginFrame with a fence of the presenter's
// commands after it, 0 when it drew no live picture. It returns the fence it
// replaces, for the presenter to delete. It does not wake the loop: a pass
// waits for the frames to stop, not for the end of one.
func (s *Snapshotter) EndFrame(fence uintptr) uintptr {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inFrame, s.prevEnd, s.lastEnd = false, s.lastEnd, time.Now()
	if fence == 0 {
		return 0
	}
	old := s.presFence
	s.presFence = fence
	return old
}

// setLive takes what SetLive asked for. Live begins with the windows changed
// since their snapshot waiting a pass; it ends with the passes under way
// dropped, never published.
func (s *Snapshotter) setLive() {
	s.mu.Lock()
	want := s.liveWant
	s.mu.Unlock()
	if want == s.live {
		return
	}
	began := s.live.overlay == 0 && want.overlay != 0
	s.live = want
	switch {
	case began:
		for _, w := range s.windows {
			w.liveDue = liveSchedule{}
			if w.schedule.dirty {
				w.liveDue.change(w.schedule.dirtyAt)
			}
		}
	case want.overlay == 0:
		for _, w := range s.windows {
			if w.live != nil && w.live.pending != 0 {
				gl.DeleteSync(w.live.pending)
				w.live.pending = 0
			}
		}
		s.emptyTrash()
	}
}

// liveWait is how long the loop waits while live: for the next pass, and no
// longer than livePoll while a pass is under way or textures wait to be
// deleted
func (s *Snapshotter) liveWait(now time.Time) time.Duration {
	d := time.Hour
	if len(s.trash) > 0 {
		d = livePoll
	}
	for _, w := range s.windows {
		if w.live != nil && w.live.pending != 0 {
			d = livePoll
			break
		}
	}
	if s.othersShown() {
		// No pass until the other switcher goes: its unmapping wakes the
		// loop
		return d
	}
	if _, due, ok := s.nextLive(now); ok {
		if s.liveRetry.After(due) {
			due = s.liveRetry
		}
		if at := s.passAt(now, due); at.After(due) {
			// For the frames to stop; another frame meanwhile puts it off
			due = at
		}
		d = min(d, max(0, due.Sub(now)))
	}
	return d
}

// passAt is passAt of the frames the switcher presented
func (s *Snapshotter) passAt(now, due time.Time) time.Time {
	s.mu.Lock()
	last, prev := s.lastEnd, s.prevEnd
	s.mu.Unlock()
	return passAt(now, due, last, prev, s.hold)
}

// liveTick publishes the passes done and makes the next one, if due and in
// a pause between frames
func (s *Snapshotter) liveTick(now time.Time) {
	s.publishDone()
	s.emptyTrash()
	if s.othersShown() {
		return
	}
	w, due, ok := s.nextLive(now)
	if !ok || due.After(now) || s.liveRetry.After(now) {
		return
	}
	if s.passAt(now, due).After(now) {
		return
	}
	if s.livePass(w, now) {
		s.lastPass = now
	} else {
		s.liveRetry = now.Add(livePoll)
	}
}

// othersShown reports whether the switcher of another qws instance is shown:
// the live passes pause for it as the snapshots do (specs/011-snapshot-pause,
// D5 of specs/020-live-thumbnails), but not for this instance's own overlay,
// listed among the switchers as well
func (s *Snapshotter) othersShown() bool {
	return s.switchers != nil && s.switchers.Shown(s.live.overlay)
}

// nextLive is the window due the next pass at now, and when: of the windows
// the switcher shows, those of its last frame
func (s *Snapshotter) nextLive(now time.Time) (*window, time.Time, bool) {
	s.mu.Lock()
	shown := s.shown
	s.mu.Unlock()
	var windows []*window
	var waiting []liveSchedule
	n := 0
	for _, id := range shown {
		w, ok := s.windows[id]
		if !ok {
			continue
		}
		if w.livePassable(s.render != nil && !s.noScaled) {
			windows = append(windows, w)
			waiting = append(waiting, w.liveDue)
			n++
		} else if !w.liveDue.last.IsZero() && now.Sub(w.liveDue.last) < s.live.interval {
			n++
		}
	}
	i, due, ok := nextPass(waiting, n, s.lastPass, s.live.interval)
	if !ok {
		return nil, time.Time{}, false
	}
	return windows[i], due, true
}

// livePassable reports whether the window waits a pass that can be made: it
// changed, is viewable, keeps the pixmap of its snapshot — a window whose
// size changed keeps its last picture — bound on the GPU, or, taken from its
// frame's pixmap (specs/022-uncaptured-windows), named, with fromFrame, the
// passes by RENDER (scaleFromFrame) possible; and it has no pass under way
func (w *window) livePassable(fromFrame bool) bool {
	if !w.liveDue.dirty || !w.viewable() || w.stale || w.live != nil && w.live.pending != 0 {
		return false
	}
	if w.via != 0 {
		return fromFrame && w.pixmap != 0
	}
	return w.bound != nil
}

// livePass binds the window's pixmap again and averages it into the live
// texture not published, then makes a fence; a window taken from its frame,
// scaled first into a pixmap of qws's own (scaleFromFrame), is copied from
// that one. False, and no pass, while a frame that may draw that texture is
// being drawn, or when the window cannot be scaled.
func (s *Snapshotter) livePass(w *window, now time.Time) bool {
	start := time.Now()
	tw, th := thumbSize(w.width, w.height)
	if lv := w.live; lv != nil && (lv.width != tw || lv.height != th) {
		// Textures of the size of an earlier snapshot
		s.dropLive(w)
	}
	if w.live == nil {
		w.live = newLiveTextures(tw, th)
	}
	lv := w.live
	next := 0
	if lv.cur == 0 {
		next = 1
	}
	if lv.cur >= 0 {
		s.mu.Lock()
		if s.inFrame && s.fetchSeq < lv.pubSeq {
			// A frame that took the pictures before the last publication — it
			// may draw the texture — is being drawn
			s.mu.Unlock()
			return false
		}
		if f := s.presFence; f != 0 {
			// The frames before it have drawn the texture
			if r := gl.ClientWaitSync(f, 0, 0); r != gl.ALREADY_SIGNALED && r != gl.CONDITION_SATISFIED {
				gl.WaitSync(f, 0, gl.TIMEOUT_IGNORED)
			}
		}
		s.mu.Unlock()
	}

	// Changes from here on report again
	if w.damage != 0 {
		damage.Subtract(s.conn, w.damage, 0, 0)
	}
	gl.ActiveTexture(gl.TEXTURE0)
	if w.via != 0 && !s.scaleFromFrame(w, tw, th) {
		return false
	}
	// What the X server was asked to draw into the pixmap — by the window's
	// client, or by RENDER from the frame — is drawn on the GPU before the
	// pixmap is bound again: without it 5 of 55 runs of TestSharedLive read
	// the pixmap as it was, with the desktop in use, and 10 of 200 passes
	// from frames
	s.off.WaitX()
	if w.via != 0 {
		// Of the thumbnail's size already: the average copies it
		sp := w.scaled
		gl.BindTexture(gl.TEXTURE_2D, sp.texture)
		sp.bound.Rebind()
		s.gpu.average(lv.fbo[next], tw, th, sp.bound.YInverted)
	} else {
		gl.BindTexture(gl.TEXTURE_2D, w.texture)
		w.bound.Rebind()
		s.gpu.average(lv.fbo[next], w.width, w.height, w.bound.YInverted)
	}
	lv.pending = gl.FenceSync(gl.SYNC_GPU_COMMANDS_COMPLETE, 0)
	// The pass starts now; and a fence tested from this context signals only
	// once flushed
	gl.Flush()
	lv.changed, lv.start, lv.cpu = w.liveDue.dirtyAt, start, time.Since(start)
	w.liveDue.passed(now)
	return true
}

// Live passes of the windows taken from their frame (D2 of
// specs/020-live-thumbnails, the author's answer A): such a window — of depth
// 24 in a frame of depth 24, which the X server does not redirect apart from
// it — has no pixmap of its own, and the frame's is the compositor's, never
// bound by qws (specs/022-uncaptured-windows, D2). A pass scales the window's
// rectangle out of the frame's pixmap by RENDER, as its snapshot is scaled,
// straight into a pixmap of qws's own of the thumbnail's size, bound on the
// GPU once — its storage is the snapshotter's, which no other client binds
// (specs/018-snapshot-bind-conflicts) — and copies that into the live texture
// not published: the fences and the two textures of every other pass.
// Nothing is read back.

// scaledPixmap is the pixmap a window taken from its frame is scaled into,
// a8r8g8b8 of its thumbnail's size, its picture, and its texture bound on the
// GPU
type scaledPixmap struct {
	pixmap        xproto.Pixmap
	picture       render.Picture
	width, height int
	texture       uint32
	bound         *glx.TexturePixmap
}

// bindScaled binds a scaled pixmap to the texture bound; a variable, so that
// a test can make it fail, or see what is bound
var bindScaled = func(off *glx.Offscreen, pixmap xproto.Pixmap) (*glx.TexturePixmap, error) {
	return off.BindPixmap(uint32(pixmap), 32)
}

// scaleFromFrame scales the window, taken from its frame's pixmap, into its
// scaled pixmap, of tw×th, by RENDER, and waits for the X server to have
// done it: the GPU reads the pixmap next. False when it cannot: the window is
// then stale and keeps its picture until a snapshot names its pixmap anew;
// should a scaled pixmap not bind on the GPU, no window is passed from its
// frame for the session.
func (s *Snapshotter) scaleFromFrame(w *window, tw, th int) bool {
	sp, err := s.scaledFor(w, tw, th)
	if err == nil {
		var r image.Rectangle
		if r, err = s.pixmapRect(w); err == nil {
			var passes []render.CompositeCookie
			passes, err = s.render.scale(w.pixmap, w.pixVisual, w.pixDepth, r, sp.picture)
			for _, p := range passes {
				if err == nil {
					err = p.Check()
				}
			}
		}
	}
	if err != nil {
		log.Debug().Err(err).Uint32("window", uint32(w.id)).Uint32("via", uint32(w.via)).
			Msg("Live pass from the frame failed: the window keeps its picture")
		w.stale = true
		return false
	}
	return true
}

// scaledFor is the scaled pixmap of the window, of tw×th, made at its first
// pass from the frame and anew at another size
func (s *Snapshotter) scaledFor(w *window, tw, th int) (*scaledPixmap, error) {
	if sp := w.scaled; sp != nil && sp.width == tw && sp.height == th {
		return sp, nil
	}
	s.dropScaled(w)
	sp := &scaledPixmap{width: tw, height: th}
	w.scaled = sp // for dropScaled to free what is made, should a step fail
	var err error
	if sp.pixmap, err = xproto.NewPixmapId(s.conn); err != nil {
		sp.pixmap = 0
		s.dropScaled(w)
		return nil, err
	}
	if err = xproto.CreatePixmapChecked(s.conn, 32, sp.pixmap, xproto.Drawable(s.root), uint16(tw), uint16(th)).Check(); err != nil {
		sp.pixmap = 0
		s.dropScaled(w)
		return nil, err
	}
	if sp.picture, err = render.NewPictureId(s.conn); err == nil {
		err = render.CreatePictureChecked(s.conn, sp.picture, xproto.Drawable(sp.pixmap), s.render.argb32, 0, nil).Check()
	}
	if err != nil {
		sp.picture = 0
		s.dropScaled(w)
		return nil, err
	}
	sp.texture = newWindowTexture()
	if sp.bound, err = bindScaled(s.off, sp.pixmap); err != nil {
		s.dropScaled(w)
		s.noScaled = true
		log.Info().Err(err).Msg("Live thumbnails of the windows taken from their frame off: a pixmap of qws does not bind on the GPU")
		return nil, err
	}
	return sp, nil
}

// dropScaled frees the window's scaled pixmap and what goes with it
func (s *Snapshotter) dropScaled(w *window) {
	sp := w.scaled
	if sp == nil {
		return
	}
	if sp.bound != nil {
		sp.bound.Release()
	}
	if sp.texture != 0 {
		gl.DeleteTextures(1, &sp.texture)
	}
	if sp.picture != 0 {
		render.FreePicture(s.conn, sp.picture)
	}
	if sp.pixmap != 0 {
		xproto.FreePixmap(s.conn, sp.pixmap)
	}
	w.scaled = nil
}

// publishDone publishes the passes whose fences have signalled, and wakes the
// switcher if it has not been woken since its last frame
func (s *Snapshotter) publishDone() {
	published := false
	for _, w := range s.windows {
		lv := w.live
		if lv == nil || lv.pending == 0 {
			continue
		}
		if r := gl.ClientWaitSync(lv.pending, 0, 0); r != gl.ALREADY_SIGNALED && r != gl.CONDITION_SATISFIED {
			continue
		}
		gl.DeleteSync(lv.pending)
		lv.pending = 0
		next := 0
		if lv.cur == 0 {
			next = 1
		}
		w.pictures++
		s.mu.Lock()
		s.seq++
		lv.cur, lv.pubSeq = next, s.seq
		s.pics[w.id] = Picture{Texture: lv.texture[next], Width: lv.width, Height: lv.height, Gen: w.pictures, Changed: lv.changed}
		s.mu.Unlock()
		published = true
		log.Debug().
			Uint32("window", uint32(w.id)).
			Int("width", lv.width).
			Int("height", lv.height).
			Dur("ms", lv.cpu).
			Dur("done_ms", time.Since(lv.start)).
			Dur("since_change_ms", time.Since(lv.changed)).
			Msg("Live")
	}
	if published {
		s.wakeSwitcher()
	}
}

// wakeSwitcher sends the overlay the ClientMessage of LiveAtom, at most one
// between two frames of the switcher: a switcher at rest blocks on its X
// connection
func (s *Snapshotter) wakeSwitcher() {
	s.mu.Lock()
	woken := s.woken
	s.woken = true
	s.mu.Unlock()
	if woken || s.live.overlay == 0 {
		return
	}
	ev := xproto.ClientMessageEvent{
		Format: 32,
		Window: s.live.overlay,
		Type:   s.atoms.live,
		Data:   xproto.ClientMessageDataUnionData32New(make([]uint32, 5)),
	}
	xproto.SendEvent(s.conn, false, s.live.overlay, 0, string(ev.Bytes()))
}

// newLiveTextures makes the two textures of a window's live thumbnails, of
// w×h, and their framebuffers
func newLiveTextures(w, h int) *liveTextures {
	lv := &liveTextures{width: w, height: h, cur: -1}
	gl.GenTextures(2, &lv.texture[0])
	gl.GenFramebuffers(2, &lv.fbo[0])
	for i := range lv.texture {
		gl.BindTexture(gl.TEXTURE_2D, lv.texture[i])
		gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, int32(w), int32(h), 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
		gl.BindFramebuffer(gl.FRAMEBUFFER, lv.fbo[i])
		gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, lv.texture[i], 0)
	}
	return lv
}

// delete deletes the textures, their framebuffers and the fence of a pass
// under way
func (lv *liveTextures) delete() {
	if lv.pending != 0 {
		gl.DeleteSync(lv.pending)
	}
	gl.DeleteFramebuffers(2, &lv.fbo[0])
	gl.DeleteTextures(2, &lv.texture[0])
}

// dropLive withdraws the window's live picture and deletes its textures, at
// once if no frame is being drawn, else once none is: the frame may bind them,
// and a name deleted may be given again
func (s *Snapshotter) dropLive(w *window) {
	if w.live == nil {
		return
	}
	s.mu.Lock()
	delete(s.pics, w.id)
	busy := s.inFrame
	s.mu.Unlock()
	if busy {
		s.trash = append(s.trash, w.live)
	} else {
		w.live.delete()
	}
	w.live = nil
}

// emptyTrash deletes the textures of windows gone while a frame was drawn
func (s *Snapshotter) emptyTrash() {
	if len(s.trash) == 0 {
		return
	}
	s.mu.Lock()
	busy := s.inFrame
	s.mu.Unlock()
	if busy {
		return
	}
	for _, lv := range s.trash {
		lv.delete()
	}
	s.trash = nil
}
