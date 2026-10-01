package ui

import (
	"image"
	"math"
	"runtime"
	"sync"
	"time"

	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/rs/zerolog/log"
)

// The step of the carousel and of the grid, animated on a presenter that
// composes layers (specs/007-animation): the position of the selection moves
// to its target; every refresh period a scene of layers is presented — the
// cards of the carousel, or the tiles of the grid and its selection frame; at
// rest, the frame the renderer draws. Layers are drawn in the background,
// ahead of the steps that need them, so that no key waits for drawing.

// animationLog is what the frame records of one animation need (metrics
// A1–A3 of specs/007-animation)
type animationLog struct {
	id        int       // number of the animation in the session
	cause     time.Time // when the event that set its current target was read
	fresh     bool      // no frame presented since that event
	lastP     time.Time // end of the presentation of the last frame
	first     time.Time // end of the presentation of the first frame
	retargets int       // targets set while it moved
}

// begin makes it the log of animation id, caused by an event read at cause
func (a *animationLog) begin(id int, cause time.Time) {
	*a = animationLog{id: id, cause: cause, fresh: true}
}

// stepAnimation is the step in progress
type stepAnimation struct {
	animationLog
	active bool
	pos    motion // position of the selection, in windows
	gx, gy motion // place of the selection frame of the grid, in pixels
}

// restDrawing is the frame at rest drawn in the background, one at a time: a
// target set while one is drawn is drawn after it, and only the latest. A
// drawing takes 40–70 ms of a CPU and 14 MB on E1; one per key of a held step
// would run a dozen at once, and their collection delays the frames of the
// animation (research).
type restDrawing struct {
	busy    bool       // a drawing runs
	want    bool       // the current target is to be drawn after it
	ready   *restFrame // drawn for the current target
	staged  bool       // ready is on the GPU in full
	failed  bool       // staging ready failed: it is presented as a frame
	awaited bool       // the step ended before it: presented when it comes
	results chan restFrame
}

// restFrame is the frame at rest drawn in the background
type restFrame struct {
	img      *image.RGBA
	gen      int // of the layers: the window list and geometry it was drawn for
	selected int
	hover    int
	draw     time.Duration
}

// layerCache is what the animator holds, and what is being drawn for it: the
// base and the card layers of the current window list and geometry
type layerCache struct {
	gen     int // changes when the layers are dropped; older results are stale
	base    bool
	cards   map[cardKey]cardLayer
	pending map[cardKey]bool
	results chan layerResult
	next    carousel.LayerID
}

// cardKey names a card layer: the card of window index at an integer offset
// from the selection — or, with a negative index, a layer of the grid
type cardKey struct{ index, offset int }

// Layers of the grid among the card layers
const (
	gridTiles     = -1 - iota // the tiles, offset the hover
	gridShadow                // the shadow of the selected tile
	gridSelection             // the selection frame
)

type cardLayer struct {
	id     carousel.LayerID // 0: the card is not drawn at that offset
	bounds image.Rectangle
}

type layerResult struct {
	gen int
	key cardKey
	img *image.RGBA
}

const baseLayer carousel.LayerID = 1

// fontSets lends a FontSet to each drawing that runs in parallel
var fontSets = sync.Pool{New: func() any { return carousel.NewFontSet() }}

// layerSlots bounds the layers drawn at once to half the CPUs: the loop of the
// animation, locked to the thread of the GL context, then finds a free one
// when its frame is due (research)
var layerSlots = make(chan struct{}, max(1, runtime.NumCPU()/2))

// animates reports whether a step now would be animated
func (s *Selector) animates() bool {
	return s.animator != nil
}

// grid reports whether the grid is shown
func (s *Selector) grid() bool {
	return s.config.LayoutMode == "grid"
}

// stepTo moves the selection to target: animated when it can be, at once
// otherwise
func (s *Selector) stepTo(target int, thumbnails []image.Image) {
	wrap := target-s.selectedIndex > 1 || s.selectedIndex-target > 1
	// The grid moves over its tiles: without them, at once
	_, noTiles := s.tilesLayer()
	if !s.animates() || wrap || s.grid() && noTiles {
		s.cancelStep()
		s.animOffset = 0
		s.selectedIndex = target
		s.render(thumbnails)
		s.prefetch()
		return
	}

	now := time.Now()
	if !s.step.active {
		s.animations++
		s.step.begin(s.animations, s.timing.start)
		s.step.active = true
		s.step.pos.rest(float64(s.selectedIndex))
		x, y, _, _ := carousel.GridTile(len(s.windows), s.selectedIndex, s.config)
		s.step.gx.rest(x)
		s.step.gy.rest(y)
		if !s.fade.active {
			s.frameDue = time.Time{}
		}
	}
	if s.step.pos.moving(now) {
		s.step.retargets++
	}
	s.step.cause = s.timing.start
	s.step.fresh = true
	from := s.step.pos.at(now)
	s.step.pos.moveTo(now, float64(target))
	x, y, _, _ := carousel.GridTile(len(s.windows), target, s.config)
	s.step.gx.moveTo(now, x)
	s.step.gy.moveTo(now, y)
	s.selectedIndex = target

	s.ensureBase()
	if !s.grid() {
		s.requestLayers(s.motionLayers(from, float64(target)))
	}
	s.prefetch()
	s.rest.ready, s.rest.awaited = nil, false
	if s.rest.busy {
		s.rest.want = true
	} else {
		s.startRest()
	}
}

// cancelStep ends the step in progress without its frame at rest
func (s *Selector) cancelStep() {
	s.step.active = false
	s.rest.want = false
	s.rest.ready = nil
	s.rest.awaited = false
}

// dropLayers forgets the layers: the window list, the geometry or the
// thumbnails they were drawn from changed
func (s *Selector) dropLayers() {
	s.cancelStep()
	if s.animator != nil {
		s.animator.DropLayers()
	}
	results := s.layers.results
	if results == nil {
		results = make(chan layerResult, 256)
	}
	s.layers = layerCache{
		gen:     s.layers.gen + 1,
		cards:   make(map[cardKey]cardLayer),
		pending: make(map[cardKey]bool),
		results: results,
		next:    baseLayer + 1,
	}
}

// startRest draws the frame at rest for the current selection in the
// background
func (s *Selector) startRest() {
	if s.rest.results == nil {
		s.rest.results = make(chan restFrame, 1)
	}
	s.rest.busy, s.rest.want = true, false
	data := s.prepareWindowData()
	cfg := s.config
	frame := restFrame{gen: s.layers.gen, selected: s.selectedIndex, hover: s.hoverIndex}
	renderer, results := s.renderer, s.rest.results
	go func() {
		fonts := fontSets.Get().(*carousel.FontSet)
		defer fontSets.Put(fonts)
		cfg.Fonts = fonts
		start := time.Now()
		if cfg.LayoutMode == "grid" {
			frame.img = renderer.DrawGridLayout(data, frame.selected, frame.hover, cfg)
		} else {
			frame.img = renderer.Draw3DCarouselWithData(data, frame.selected, frame.hover, 0, cfg)
		}
		frame.draw = time.Since(start)
		results <- frame
	}()
}

// collectRest takes a frame at rest drawn in the background: the frame of the
// current target, or else the start of the drawing of that target
func (s *Selector) collectRest(r restFrame) {
	s.rest.busy = false
	if r.gen == s.layers.gen && r.selected == s.selectedIndex {
		s.rest.ready, s.rest.want = &r, false
		s.rest.staged, s.rest.failed = false, false
	} else if s.rest.want {
		s.startRest()
	}
}

// ensureBase uploads the base layer
func (s *Selector) ensureBase() {
	if s.layers.base {
		return
	}
	if err := s.animator.SetLayer(baseLayer, carousel.CarouselBase(s.config)); err != nil {
		log.Error().Err(err).Msg("Failed to set the base layer")
		return
	}
	s.layers.base = true
}

// motionLayers are the card layers a motion from p to target passes
func (s *Selector) motionLayers(p, target float64) []cardKey {
	lo, hi := math.Min(p, target), math.Max(p, target)
	var keys []cardKey
	for k := range s.windows {
		for n := int(math.Floor(float64(k) - hi)); n <= int(math.Ceil(float64(k)-lo)); n++ {
			if n >= -5 && n <= 5 {
				keys = append(keys, cardKey{k, n})
			}
		}
	}
	return keys
}

// prefetch requests the layers of the next step either way from the
// selection: of the carousel, the cards it passes; of the grid, the tiles
// with the current hover, the shadow and the selection frame
func (s *Selector) prefetch() {
	if !s.animates() {
		return
	}
	if s.grid() {
		s.requestLayers([]cardKey{{gridTiles, s.hoverIndex}, {gridShadow, 0}, {gridSelection, 0}})
		return
	}
	sel := float64(s.selectedIndex)
	s.requestLayers(append(s.motionLayers(sel-1, sel), s.motionLayers(sel, sel+1)...))
}

// tilesLayer is the key of the tiles layer of the grid held, of the current
// hover if there is one; false when none is held
func (s *Selector) tilesLayer() (cardKey, bool) {
	key := cardKey{gridTiles, s.hoverIndex}
	if l, ok := s.layers.cards[key]; ok && l.id != 0 {
		return key, false
	}
	for k, l := range s.layers.cards {
		if k.index == gridTiles && l.id != 0 {
			return k, false
		}
	}
	return cardKey{}, true
}

// drawLayer draws the layer named by key
func drawLayer(data []carousel.WindowData, key cardKey, cfg carousel.Config) *image.RGBA {
	switch key.index {
	case gridTiles:
		return carousel.GridTiles(data, key.offset, cfg)
	case gridShadow, gridSelection:
		_, _, w, h := carousel.GridTile(len(data), 0, cfg)
		if key.index == gridShadow {
			return carousel.GridShadow(w, h, cfg)
		}
		return carousel.GridSelection(w, h, cfg)
	}
	return carousel.CardLayer(data, key.index, key.offset, cfg)
}

// requestLayers draws in the background the layers that are neither held
// nor being drawn
func (s *Selector) requestLayers(keys []cardKey) {
	var data []carousel.WindowData
	gen, results := s.layers.gen, s.layers.results
	for _, key := range keys {
		if _, ok := s.layers.cards[key]; ok || s.layers.pending[key] {
			continue
		}
		if data == nil {
			data = s.prepareWindowData()
		}
		s.layers.pending[key] = true
		cfg := s.config
		go func() {
			layerSlots <- struct{}{}
			defer func() { <-layerSlots }()
			fonts := fontSets.Get().(*carousel.FontSet)
			defer fontSets.Put(fonts)
			cfg.Fonts = fonts
			results <- layerResult{gen, key, drawLayer(data, key, cfg)}
		}()
	}
}

// drainLayers uploads the layers drawn so far
func (s *Selector) drainLayers() {
	for {
		select {
		case r := <-s.layers.results:
			s.setLayer(r)
		default:
			return
		}
	}
}

// setLayer uploads a layer drawn in the background; it returns the bytes it
// uploaded
func (s *Selector) setLayer(r layerResult) int {
	if r.gen != s.layers.gen {
		return 0
	}
	delete(s.layers.pending, r.key)
	if r.key.index == gridTiles {
		// One tiles layer at a time: each is about a frame
		for k, l := range s.layers.cards {
			if k.index == gridTiles {
				if l.id != 0 {
					s.animator.DropLayer(l.id)
				}
				delete(s.layers.cards, k)
			}
		}
	}
	layer := cardLayer{}
	if r.img != nil {
		layer = cardLayer{id: s.layers.next, bounds: r.img.Rect}
		s.layers.next++
		if err := s.animator.SetLayer(layer.id, r.img); err != nil {
			log.Error().Err(err).Msg("Failed to set a card layer")
			layer = cardLayer{}
		}
	}
	s.layers.cards[r.key] = layer
	if r.img == nil {
		return 0
	}
	return len(r.img.Pix)
}

// backgroundDue reports whether the background has work for the loop while
// nothing moves: the base or card layers to go to the animator, or the frame
// at rest of a step that has ended
func (s *Selector) backgroundDue() bool {
	return s.animates() && (!s.layers.base || len(s.layers.pending) > 0) || s.rest.awaited
}

// uploadIdle uploads the base, or waits a moment for a card layer or the
// frame at rest and takes it, while no event waits: then the first frame of a
// step has the layers it needs and uploads none, and a frame at rest that
// came after its step is presented when it comes
func (s *Selector) uploadIdle() {
	if !s.layers.base {
		s.ensureBase()
		return
	}
	timer := time.NewTimer(time.Millisecond)
	defer timer.Stop()
	select {
	case r := <-s.layers.results:
		s.setLayer(r)
	case r := <-s.rest.results:
		s.collectRest(r)
		if s.rest.awaited && s.rest.ready != nil {
			start, end := s.presentRest(1)
			s.logAnimationFrame(&s.step.animationLog, s.config.LayoutMode, 1, true, start, start, end)
		}
	case <-timer.C:
	}
}

// waitFrame waits for the frame due at t and uploads the layers drawn
// meanwhile. An upload takes the GPU up to a couple of milliseconds; made in
// a frame, it delays the frame past its vertical blank (research), so the
// layers that come later than spinAhead before t wait for the next pause.
// The first frame of a step, due at once, uploads what there is: it may need
// it.
//
// The frame at rest goes to the GPU in the pauses as well, a piece at a time:
// presented as a frame, it would upload up to 14 MB when it is due, and come
// a vertical blank or two late (research). The uploads of a pause, layers
// first, stop at pauseUpload: the frame that follows waits on the GPU for
// them, and at about a gigabyte a second on E1 a megabyte takes the spin
// before the frame.
//
// The last spinAhead is spun, not slept: a goroutine locked to the thread of
// the GL context wakes from a sleep up to several milliseconds late when the
// CPUs are busy, and one wake-up a frame is less exposed than two (research).
func (s *Selector) waitFrame(t time.Time) time.Time {
	const (
		spinAhead   = 3 * time.Millisecond
		pauseUpload = 1 << 20
	)
	if t.IsZero() {
		s.drainLayers()
		return time.Now()
	}
	uploaded := 0
	defer func() { s.uploaded = uploaded }()
	for {
		d := time.Until(t) - spinAhead
		if d <= 0 {
			break
		}
		layers := s.layers.results
		if uploaded >= pauseUpload {
			layers = nil
		} else if r := s.rest.ready; r != nil && !s.rest.staged && !s.rest.failed && len(layers) == 0 {
			n, done, err := s.animator.StageFrame(r.img, pauseUpload-uploaded)
			if err != nil {
				log.Error().Err(err).Msg("Failed to stage the frame at rest")
				s.rest.failed = true
			}
			uploaded += n
			s.rest.staged = done
			continue
		}
		timer := time.NewTimer(d)
		select {
		case r := <-layers:
			uploaded += s.setLayer(r)
		case r := <-s.rest.results:
			s.collectRest(r)
		case <-timer.C:
		}
		timer.Stop()
	}
	now := time.Now()
	for now.Before(t) {
		now = time.Now()
	}
	return now
}

// frame presents the next frame of the animations when it is due, at the alpha
// of the fade if one runs: the scene of the step that moves; at the end of a
// step, its frame at rest, or while that is drawn, its scene at the target;
// with a fade alone, the picture shown
func (s *Selector) frame() {
	now := s.waitFrame(s.frameDue)
	alpha := 1.0
	if s.fade.active {
		alpha = s.fade.alpha.at(now)
	}

	var err error
	stepped, atRest := false, false // a frame of the step; its last
	drawStart, drawEnd := now, now
	switch {
	case s.step.active && s.step.pos.moving(now):
		stepped = true
		items := s.sceneItems(now)
		drawEnd = time.Now()
		err = s.animator.PresentScene(baseLayer, items, alpha)
	case s.step.active || s.rest.awaited:
		stepped = s.step.active
		s.step.active = false
		if s.rest.ready != nil {
			stepped, atRest = true, true
			drawStart, drawEnd = s.presentRest(alpha)
			break
		}
		// The loop does not wait for the frame at rest — the grid takes some
		// 350 ms to draw on E1 (research) — but presents it when it comes
		s.rest.awaited = true
		if !s.rest.busy {
			s.startRest()
		}
		items := s.sceneItems(now)
		drawEnd = time.Now()
		err = s.animator.PresentScene(baseLayer, items, alpha)
	default:
		err = s.animator.PresentFaded(nil, alpha)
	}
	if err != nil {
		log.Error().Err(err).Msg("Failed to present an animation frame")
	}
	end := time.Now()
	if stepped {
		s.logAnimationFrame(&s.step.animationLog, s.config.LayoutMode, s.step.pos.progress(now), atRest, drawStart, drawEnd, end)
	}
	if s.fade.active {
		done := !s.fade.alpha.moving(now)
		s.logAnimationFrame(&s.fade.animationLog, s.fade.kind(), s.fade.alpha.progress(now), done, drawStart, drawEnd, end)
		s.fade.active = !done
	}

	// Frames are due at fixed times, a period apart; one that falls behind
	// by more than a period starts the schedule anew
	if s.frameDue.IsZero() || end.Sub(s.frameDue) > s.period {
		s.frameDue = now
	}
	s.frameDue = s.frameDue.Add(s.period)
}

// sceneItems is the scene of the step at now, of the layout shown
func (s *Selector) sceneItems(now time.Time) []carousel.SceneItem {
	if s.grid() {
		return s.gridItems(now)
	}
	return s.carouselItems(now)
}

// gridItems is the scene of the grid at now: the shadow of the selected tile,
// the tiles, the selection frame, the first and the last where the selection
// frame is on its way
func (s *Selector) gridItems(now time.Time) []carousel.SceneItem {
	x, y := s.step.gx.at(now), s.step.gy.at(now)
	var items []carousel.SceneItem
	add := func(key cardKey, dx, dy float64) {
		l, ok := s.layers.cards[key]
		if !ok || l.id == 0 {
			return
		}
		b := l.bounds
		items = append(items, carousel.SceneItem{A: l.id, Alpha: 1, RectA: carousel.Rect{
			X: dx + float64(b.Min.X), Y: dy + float64(b.Min.Y), W: float64(b.Dx()), H: float64(b.Dy()),
		}})
	}
	tiles, _ := s.tilesLayer()
	add(cardKey{gridShadow, 0}, x, y)
	add(tiles, 0, 0)
	add(cardKey{gridSelection, 0}, x, y)
	return items
}

// carouselItems is the scene of the carousel at now: each card as its layers
// at the integer offsets around its fractional one, cross-faded
func (s *Selector) carouselItems(now time.Time) []carousel.SceneItem {
	p := s.step.pos.at(now)
	data := s.prepareWindowData()
	var items []carousel.SceneItem
	for k := range s.windows {
		o := float64(k) - p
		x, y, scale, ok := carousel.CardCenter(data, k, o, s.config)
		if !ok {
			continue
		}
		n0 := math.Floor(o)
		item := carousel.SceneItem{WeightB: o - n0, Alpha: 1}
		item.A, item.RectA = s.placeLayer(data, k, int(n0), x, y, scale)
		if item.WeightB > 0 {
			item.B, item.RectB = s.placeLayer(data, k, int(n0)+1, x, y, scale)
		}
		items = append(items, item)
	}
	return items
}

// placeLayer is the layer of card index nearest to the integer offset among
// those held, placed and scaled for a card whose centre is at x, y with the
// scale
func (s *Selector) placeLayer(data []carousel.WindowData, index, offset int, x, y, scale float64) (carousel.LayerID, carousel.Rect) {
	for d := 0; d <= 2; d++ {
		for _, n := range []int{offset + d, offset - d} {
			layer, ok := s.layers.cards[cardKey{index, n}]
			if !ok || layer.id == 0 {
				continue
			}
			xn, yn, sn, ok := carousel.CardCenter(data, index, float64(n), s.config)
			if !ok || sn == 0 {
				continue
			}
			f := scale / sn
			b := layer.bounds
			return layer.id, carousel.Rect{
				X: x + (float64(b.Min.X)-xn)*f,
				Y: y + (float64(b.Min.Y)-yn)*f,
				W: float64(b.Dx()) * f,
				H: float64(b.Dy()) * f,
			}
		}
	}
	return 0, carousel.Rect{}
}

// presentRest presents the frame at rest drawn for the current target, scaled
// by alpha; it returns when the presentation started and ended
func (s *Selector) presentRest(alpha float64) (time.Time, time.Time) {
	s.rest.awaited = false
	// A frame at rest that came too late to be staged in the pauses is staged
	// now: presented as a frame it would upload no less
	if !s.rest.staged && !s.rest.failed {
		_, done, err := s.animator.StageFrame(s.rest.ready.img, math.MaxInt)
		if err != nil {
			log.Error().Err(err).Msg("Failed to stage the frame at rest")
		}
		s.rest.staged = done
	}
	rest, staged := s.rest.ready, s.rest.staged
	s.rest.ready, s.rest.staged = nil, false

	presentStart := time.Now()
	var err error
	if staged {
		err = s.animator.PresentStaged(alpha)
	} else {
		err = s.animator.PresentFaded(rest.img, alpha)
	}
	if err != nil {
		log.Error().Err(err).Msg("Failed to present frame")
	}
	end := time.Now()
	s.timing.cause, s.timing.start = causeKey, s.step.cause
	s.logFrame(presentStart.Add(-rest.draw), presentStart, end)

	// The mouse moved during the step: the frame at rest shows the old hover
	if rest.hover != s.hoverIndex {
		s.render(nil)
	}
	return presentStart, end
}
