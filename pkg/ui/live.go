package ui

import (
	"time"

	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/almaz-uno/qws/pkg/snapshot"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog/log"
)

// The live thumbnails of specs/020-live-thumbnails as the switcher shows
// them. While its overlay is shown in full — from the end of its appearance
// to the start of its disappearance — the snapshotter averages the windows
// of the cards and tiles in view that change into pictures the presenter's
// context shares; each frame takes the pictures newer than the snapshots of
// their cards (BeginFrame), draws them over the thumbnails — over the frame
// at rest, or among the items of a scene — and gives back the fence the
// presenter made after it (EndFrame). At rest the switcher blocks on its X
// connection: the snapshotter wakes it with a ClientMessage, and a frame is
// presented again with the pictures, no sooner than a refresh period after
// the one before.

// causeLive is the cause of a frame at rest presented again for a live
// picture, in the frame records
const causeLive = "live"

// liveSource is what the selector needs of the snapshotter
type liveSource interface {
	SetLive(overlay xproto.Window, interval time.Duration)
	BeginFrame(windows []xproto.Window) map[xproto.Window]snapshot.Picture
	EndFrame(fence uintptr) uintptr
}

// liveThumbnails is the part of the selector that shows live thumbnails
type liveThumbnails struct {
	snap      liveSource // nil: live thumbnails off
	presenter carousel.LivePresenter
	interval  time.Duration // between two passes of a window; 0: a refresh period
	atom      xproto.Atom   // of the ClientMessage of the snapshotter

	wanted    bool      // a picture published waits for a frame at rest
	lastFrame time.Time // the end of the last frame presented

	// The frame being presented: whether it has taken the pictures, the
	// pictures, those it draws, and where the lag of the latest pass it draws
	// for the first time counts from
	begun   bool
	pics    map[xproto.Window]snapshot.Picture
	drawing map[xproto.Window]uint64
	changed time.Time
	drawn   map[xproto.Window]uint64 // the generation each window last drew
	lag     time.Duration            // of the last frame, for its record; 0: none

	// The windows of the cards and tiles in view at rest, for the passes,
	// kept while the layers' generation, the layout and the selection stay,
	// and since when each is in view, within the generation: the windows and
	// their thumbnails are those of a new activation, or have changed
	shown    []xproto.Window
	shownKey [3]int
	since    map[xproto.Window]time.Time

	// The live items of the frame at rest, kept while what they depend on
	// stays: the layers' generation and the layout, and for the carousel the
	// selection and the hover
	rest    []carousel.LiveItem
	restKey [4]int
}

// initLive turns the live thumbnails on, with the snapshotter and the
// presenter when its context shares the snapshotter's; it says once why they
// are off when they are asked for on glx and cannot be had
func (s *Selector) initLive(snap *snapshot.Snapshotter, presenter carousel.Presenter) {
	t := s.appearance.Thumbnail
	if !t.Live || s.appearance.Renderer != "glx" {
		return
	}
	lp, ok := presenter.(carousel.LivePresenter)
	switch {
	case snap == nil:
		log.Info().Msg("Live thumbnails off: no snapshots on the GPU")
		return
	case !ok || !lp.Live():
		// The presenter fell back to cpu, which says so, or does not share
		return
	}
	atom, err := xproto.InternAtom(s.conn, false, uint16(len(snapshot.LiveAtom)), snapshot.LiveAtom).Reply()
	if err != nil {
		log.Info().Err(err).Msg("Live thumbnails off")
		return
	}
	interval, warning := t.LivePeriod()
	if warning != "" {
		log.Warn().Msg(warning)
	}
	s.live = liveThumbnails{
		snap:      snap,
		presenter: lp,
		interval:  interval,
		atom:      atom.Atom,
		drawn:     map[xproto.Window]uint64{},
	}
	log.Debug().Dur("interval", interval).Msg("Live thumbnails on")
}

// liveOn reports whether the live thumbnails are on
func (s *Selector) liveOn() bool {
	return s.live.snap != nil
}

// setLive starts the live passes once the overlay is shown in full — at
// the end of its appearance, or after its first frame when it appears at
// once — or ends them as it starts to disappear, or is unmapped. The fades
// take none: in S5 a pass beside the first frames of an appearance, which
// upload the layers, held them up (D5)
func (s *Selector) setLive(on bool) {
	if !s.liveOn() {
		return
	}
	if !on {
		s.live.snap.SetLive(0, 0)
		s.live.wanted = false
		return
	}
	interval := s.live.interval
	if interval == 0 {
		interval = s.period
	}
	s.live.snap.SetLive(s.window.GetWindowID(), interval)
}

// liveEvent takes the ClientMessage of the snapshotter: a picture waits for a
// frame
func (s *Selector) liveEvent(ev xproto.ClientMessageEvent) bool {
	if !s.liveOn() || ev.Type != s.live.atom {
		return false
	}
	s.live.wanted = true
	return true
}

// liveBegin takes the pictures for the frame about to be presented, and
// tells the snapshotter the windows in view
func (s *Selector) liveBegin() {
	if !s.liveOn() {
		return
	}
	s.live.pics = s.live.snap.BeginFrame(s.liveShown(time.Now()))
	s.live.begun, s.live.wanted = true, false
	s.live.changed = time.Time{}
	clear(s.live.drawing)
}

// liveShown is the windows whose cards or tiles are in view at rest, at
// now: every tile of the grid; the cards of the carousel with a live
// rectangle at the selection, eleven at most — those the snapshotter
// passes. A slice given is never changed: the snapshotter keeps it.
func (s *Selector) liveShown(now time.Time) []xproto.Window {
	key := [3]int{s.layers.gen, s.selectedIndex, 0}
	if s.grid() {
		key = [3]int{s.layers.gen, -1, 1}
	}
	if s.live.shown != nil && s.live.shownKey == key {
		return s.live.shown
	}
	var data []carousel.WindowData
	if !s.grid() {
		data = s.prepareWindowData()
	}
	shown := make([]xproto.Window, 0, len(s.windows))
	since := make(map[xproto.Window]time.Time, len(s.windows))
	for i, w := range s.windows {
		if !s.grid() {
			if _, ok := carousel.CardLive(data, i, float64(i-s.selectedIndex), s.config); !ok {
				continue
			}
		}
		shown = append(shown, w.ID)
		since[w.ID] = now
		if t, ok := s.live.since[w.ID]; ok && s.live.shownKey[0] == key[0] {
			since[w.ID] = t
		}
	}
	s.live.shown, s.live.shownKey, s.live.since = shown, key, since
	return shown
}

// liveEnd follows the frame presented, which ended at end: the presenter's
// fence goes to the snapshotter, and the lag of the latest pass the frame
// drew for the first time to its record
func (s *Selector) liveEnd(end time.Time) {
	if !s.live.begun {
		return
	}
	s.live.begun = false
	fence := s.live.presenter.TakeLiveFence()
	s.live.presenter.ReleaseFence(s.live.snap.EndFrame(fence))
	s.live.lastFrame, s.live.lag = end, 0
	if fence != 0 {
		for id, gen := range s.live.drawing {
			s.live.drawn[id] = max(s.live.drawn[id], gen)
		}
		if !s.live.changed.IsZero() {
			s.live.lag = end.Sub(s.live.changed)
		}
	}
	s.live.pics = nil
}

// picture is the live picture of window i for the frame being presented, and
// whether its card can show it: one of the size of its thumbnail
func (s *Selector) picture(i int, data []carousel.WindowData) (snapshot.Picture, bool) {
	if s.live.pics == nil || i >= len(s.windows) {
		return snapshot.Picture{}, false
	}
	p, ok := s.live.pics[s.windows[i].ID]
	if !ok || data[i].Thumbnail == nil {
		return snapshot.Picture{}, false
	}
	if b := data[i].Thumbnail.Bounds(); b.Dx() != p.Width || b.Dy() != p.Height {
		return snapshot.Picture{}, false
	}
	return p, true
}

// livePicture makes the item draw the picture of window i, and counts it
// for the lag when the window has not drawn it yet and its card or tile is
// in view: from its change, or from when the card came into view if later
// (Metrics, L)
func (s *Selector) livePicture(i int, it *carousel.LiveItem, p snapshot.Picture) {
	it.Texture = p.Texture
	id := s.windows[i].ID
	if since, ok := s.live.since[id]; ok && p.Gen > s.live.drawn[id] {
		from := p.Changed
		if since.After(from) {
			from = since
		}
		if from.After(s.live.changed) {
			s.live.changed = from
		}
	}
	if s.live.drawing == nil {
		s.live.drawing = map[xproto.Window]uint64{}
	}
	s.live.drawing[id] = p.Gen
}

// liveRest gives the presenter the live items of the frame at rest shown,
// with the pictures of liveBegin
func (s *Selector) liveRest() {
	if !s.liveOn() || len(s.live.pics) == 0 {
		return
	}
	data := s.prepareWindowData()
	var items []carousel.LiveItem
	for i, it := range s.liveGeometry(data) {
		p, ok := s.picture(i, data)
		if !ok || it.Rect.Empty() {
			continue
		}
		s.livePicture(i, &it, p)
		items = append(items, it)
	}
	s.live.presenter.SetLiveItems(items)
}

// liveCard is the scene item of the live picture of card k at the offset o
// of a scene of the carousel
func (s *Selector) liveCard(data []carousel.WindowData, k int, o float64) (carousel.SceneItem, bool) {
	p, ok := s.picture(k, data)
	if !ok {
		return carousel.SceneItem{}, false
	}
	it, ok := carousel.CardLive(data, k, o, s.config)
	if !ok {
		return carousel.SceneItem{}, false
	}
	s.livePicture(k, &it, p)
	return carousel.SceneItem{Live: &it}, true
}

// liveTiles are the scene items of the live pictures of the tiles of the
// grid, which do not move
func (s *Selector) liveTiles() []carousel.SceneItem {
	if !s.liveOn() || len(s.live.pics) == 0 {
		return nil
	}
	data := s.prepareWindowData()
	var items []carousel.SceneItem
	for i, it := range s.liveGeometry(data) {
		p, ok := s.picture(i, data)
		if !ok || it.Rect.Empty() {
			continue
		}
		s.livePicture(i, &it, p)
		items = append(items, carousel.SceneItem{Live: &it})
	}
	return items
}

// liveGeometry is the live items, but their textures, of the frame at rest
// of the layout shown, kept while what they depend on stays
func (s *Selector) liveGeometry(data []carousel.WindowData) []carousel.LiveItem {
	key := [4]int{s.layers.gen, -1, -1, 1}
	if !s.grid() {
		key = [4]int{s.layers.gen, s.selectedIndex, s.hoverIndex, 0}
	}
	if s.live.rest == nil || s.live.restKey != key {
		if s.grid() {
			s.live.rest = carousel.GridLive(data, s.config)
		} else {
			s.live.rest = carousel.CarouselLive(data, s.selectedIndex, s.hoverIndex, s.config)
		}
		s.live.restKey = key
	}
	return s.live.rest
}

// liveDue reports whether a picture waits for a frame at rest: nothing moves,
// so no frame of an animation will show it
func (s *Selector) liveDue() bool {
	return s.live.wanted && !s.step.active && !s.fade.active && !s.hover.active
}

// liveIdle presents the frame shown again with its live pictures once a
// refresh period has passed since the frame before; until then it waits a
// millisecond at most, for the loop to read the events meanwhile
func (s *Selector) liveIdle() {
	now := time.Now()
	if wait := s.live.lastFrame.Add(s.period).Sub(now); wait > 0 {
		time.Sleep(min(wait, time.Millisecond))
		return
	}
	s.markFrameCause(causeLive)
	s.liveBegin()
	var err error
	if s.rest.awaited {
		// The scene at the target, its frame at rest still being drawn
		err = s.animator.PresentScene(baseLayer, s.sceneItems(now), carousel.Opaque)
	} else {
		s.liveRest()
		_, err = s.presenter.Refresh()
	}
	drawEnd := time.Now()
	if err != nil {
		log.Error().Err(err).Msg("Failed to present the live thumbnails")
	}
	end := time.Now()
	s.liveEnd(end)
	s.logFrame(now, drawEnd, end)
}

// takeLag is the lag of the last frame for its record, once
func (s *Selector) takeLag() time.Duration {
	lag := s.live.lag
	s.live.lag = 0
	return lag
}
