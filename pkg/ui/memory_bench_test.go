package ui

import (
	"bufio"
	"cmp"
	"io"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/almaz-uno/qws/pkg/carousel"
	"github.com/almaz-uno/qws/pkg/snapshot"
	"github.com/go-gl/gl/v4.6-core/gl"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// memoryScript is an activation of the scenario of BenchmarkE1Memory: a key,
// 0 for the activation itself, and the time from it to the next — the
// carousel stepped by Tab, the grid by the layout key q, its steps, and the
// carousel again
var memoryScript = []struct {
	keysym uint32
	wait   time.Duration
}{
	{0, 400 * time.Millisecond},
	{0xFF09, 200 * time.Millisecond}, {0xFF09, 200 * time.Millisecond}, {0xFF09, 200 * time.Millisecond},
	{0x0071, 600 * time.Millisecond},
	{0xFF53, 200 * time.Millisecond}, {0xFF54, 200 * time.Millisecond}, {0xFF53, 200 * time.Millisecond},
	{0x0071, 300 * time.Millisecond},
	{0xFF09, 200 * time.Millisecond}, {0xFF09, 200 * time.Millisecond},
}

// memoryPause is the time between the end of an activation, its overlay
// unmapped, and the next
const memoryPause = 500 * time.Millisecond

// memoryScenarioRan is set by the first run of BenchmarkE1Memory: the peaks
// of a process are those of its first run
var memoryScenarioRan bool

// BenchmarkE1Memory runs, off the screen, the scenario of M1 and M2 of
// specs/030-drawing-memory and reports the memory of the drawing. The
// selector of BenchmarkE1Switch — the overlay of E1, 2520×1400, 33 windows
// with thumbnails of 512×288, the author's appearance, the frames and the
// layers drawn as the switcher draws them — presents through the GLX
// presenter on a window mapped off the screen, with live thumbnails: a fake
// snapshotter whose pictures of four windows, textures made once in the
// presenter's context, change 30 times a second while the overlay is shown
// in full. MEMORY_ACTIVATIONS activations, 50 by default, each as Show and
// FadeOut make one: the layers dropped, the first frame through the fade-in,
// the keys of memoryScript on a fixed schedule — the loop of
// handleEventsSync run between them as it runs while no event comes — then
// the fade-out, the overlay unmapped (hide) and memoryPause.
//
// It reports, in MB: the peaks of HeapSys, of HeapSys less HeapReleased and
// of HeapInuse, runtime.ReadMemStats sampled every 100 ms during the run;
// 5 s after the last activation, HeapInuse, and VmRSS, RssAnon and VmHWM of
// /proc/self/status; then what the process holds for good (kept): the heap
// alive after two collections, and VmRSS once its idle heap is given back
// (debug.FreeOSMemory); the collections; and the bytes allocated per
// activation, from the heap profile scaled as pprof scales it — in all, the
// images (image.NewRGBA), the pictures prepared for the frames
// (carousel.scratchImage) and the glyph masks (truetype.NewFace) — over the
// first ten activations and over the rest, a collection after the tenth for
// the profile. The images by the function of qws that made them are logged.
// MEMORY_PROFILE names a file for the heap profile at the end. The scenario
// is run once a process, its peaks its own:
//
//	go test ./pkg/ui -run '^$' -bench E1Memory -benchtime 1x
func BenchmarkE1Memory(b *testing.B) {
	if memoryScenarioRan {
		b.Skip("one run of the scenario a process: -benchtime 1x")
	}
	memoryScenarioRan = true
	activations := 50
	if v := os.Getenv("MEMORY_ACTIVATIONS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 11 {
			b.Fatalf("MEMORY_ACTIVATIONS %q: a number of at least 11", v)
		}
		activations = n
	}
	level, logger := zerolog.GlobalLevel(), log.Logger
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	log.Logger = zerolog.New(io.Discard).Level(zerolog.InfoLevel)
	defer func() { zerolog.SetGlobalLevel(level); log.Logger = logger }()

	conn, err := xgb.NewConn()
	if err != nil {
		b.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	_, presenter, err := carousel.NewBackend("glx", nil)
	if err != nil {
		b.Fatal(err)
	}
	defer presenter.Close()
	animator, ok := presenter.(carousel.Animator)
	lp, live := presenter.(carousel.LivePresenter)
	if !ok || !live {
		b.Skip("no GLX presenter")
	}

	s := e1Selector(b, 33)
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	w, err := carousel.NewWindowAt(conn, root, -s.config.Width-100, 0, s.config.Width, s.config.Height, presenter.VisualID())
	if err != nil {
		b.Fatal(err)
	}
	defer w.Close()
	if err := presenter.Bind(w); err != nil {
		b.Fatal(err)
	}
	s.window, s.presenter, s.animator = w, presenter, animator
	src := newMemorySource(s, 4)
	defer src.close()
	s.live = liveThumbnails{snap: src, presenter: lp, interval: 33 * time.Millisecond, atom: 1, drawn: map[xproto.Window]uint64{}}

	sampler := startMemorySampler(100 * time.Millisecond)
	before := allocatedByKind()
	var warm map[string]float64
	for a := 1; a <= activations; a++ {
		memoryActivation(b, s, src)
		if a == 10 {
			warm = allocatedByKind()
		}
	}
	peaks := sampler.stop()

	// The overlay unmapped, nothing drawn: the memory as the service keeps it
	time.Sleep(5 * time.Second)
	var idle runtime.MemStats
	runtime.ReadMemStats(&idle)
	status := procStatus()
	end := allocatedByKind()
	// A second collection: what a sync.Pool held is gone, as in the service
	// a few minutes idle, its collections forced every two minutes
	runtime.GC()
	var kept runtime.MemStats
	runtime.ReadMemStats(&kept)
	// What the process holds for good: its idle heap given back
	debug.FreeOSMemory()
	held := procStatus()
	if path := os.Getenv("MEMORY_PROFILE"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			b.Fatal(err)
		}
		if err := pprof.Lookup("allocs").WriteTo(f, 0); err != nil {
			b.Fatal(err)
		}
		f.Close()
	}

	const mb = 1 << 20
	b.ReportMetric(float64(peaks.sys)/mb, "heapsys_peak_MB")
	b.ReportMetric(float64(peaks.mapped)/mb, "heapmapped_peak_MB")
	b.ReportMetric(float64(peaks.inuse)/mb, "heapinuse_peak_MB")
	b.ReportMetric(float64(idle.HeapInuse)/mb, "heapinuse_idle_MB")
	b.ReportMetric(float64(kept.HeapAlloc)/mb, "heap_kept_MB")
	b.ReportMetric(status["VmRSS"]/mb, "rss_MB")
	b.ReportMetric(status["RssAnon"]/mb, "rss_anon_MB")
	b.ReportMetric(held["VmRSS"]/mb, "rss_kept_MB")
	b.ReportMetric(status["VmHWM"]/mb, "hwm_MB")
	b.ReportMetric(float64(idle.NumGC), "gc_cycles")
	rest := float64(activations - 10)
	for _, kind := range []string{"all", "images", "prepared", "masks"} {
		b.ReportMetric((warm[kind]-before[kind])/10/mb, kind+"_first10_MB/act")
		b.ReportMetric((end[kind]-warm[kind])/rest/mb, kind+"_rest_MB/act")
	}
	var fns []string
	for k := range end {
		if strings.HasPrefix(k, "images:") {
			fns = append(fns, k)
		}
	}
	slices.SortFunc(fns, func(x, y string) int { return cmp.Compare(end[y]-before[y], end[x]-before[x]) })
	for _, k := range fns {
		b.Logf("%-40s %8.1f MB/act", strings.TrimPrefix(k, "images:"), (end[k]-before[k])/float64(activations)/mb)
	}
}

// memoryActivation is an activation of the scenario: as Show makes it, the
// keys of memoryScript on their schedule, then as FadeOut ends it
func memoryActivation(b *testing.B, s *Selector, src *memorySource) {
	s.dropLayers()
	s.selectedIndex = 1
	if err := s.window.Show(); err != nil {
		b.Fatal(err)
	}
	s.mapped = true
	s.markFrameCause(causeActivation)
	at := time.Now()
	for _, k := range memoryScript {
		if k.keysym == 0 {
			s.fade.pending = s.animator != nil && s.anim.show.any()
			s.render(s.prepareThumbnails())
			if !s.fade.active {
				s.setLive(true)
			}
			s.prefetch()
		} else {
			s.markFrameCause(causeKey)
			if s.handleKeyPressSimple(xproto.KeyPressEvent{Detail: benchKeycode(b, k.keysym)}, s.prepareThumbnails()) {
				b.Fatalf("key 0x%X cancelled the switcher", k.keysym)
			}
		}
		at = at.Add(k.wait)
		memoryLoop(s, src, at)
	}

	// The release of the modifier: the fade-out, as FadeOut makes it
	s.chosenAt = time.Now()
	s.setLive(false)
	s.beginFade(true, time.Now(), s.chosenAt)
	for s.fade.active {
		s.frame()
	}
	s.cancelStep()
	s.hide()
	memoryLoop(s, src, time.Now().Add(memoryPause))
}

// memoryLoop runs the loop of handleEventsSync, as it runs while no event
// comes, until the time until: the frames of what moves, the work of the
// background, the live frames; else it waits, as the loop blocks on its
// connection. The fake snapshotter publishes meanwhile.
func memoryLoop(s *Selector, src *memorySource, until time.Time) {
	for {
		now := time.Now()
		if !now.Before(until) {
			return
		}
		src.publish(now)
		switch {
		case !s.mapped:
			time.Sleep(time.Millisecond)
		case s.moving():
			s.frame()
		case s.backgroundDue() || s.liveDue():
			if s.liveDue() {
				s.liveIdle()
			} else {
				s.uploadIdle()
			}
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

// memorySource is the snapshotter of the live thumbnails of the scenario:
// the pictures of a few windows, textures made once in the presenter's
// context, newer every 1/30 s while the overlay is live, each publication
// waking the selector as the ClientMessage of the snapshotter does
type memorySource struct {
	s        *Selector
	pics     map[xproto.Window]snapshot.Picture
	textures []uint32
	on       bool
	next     time.Time
	fence    uintptr
}

// newMemorySource makes the pictures of n windows of s, of the size of
// their thumbnails; the presenter's context is current
func newMemorySource(s *Selector, n int) *memorySource {
	m := &memorySource{s: s, pics: map[xproto.Window]snapshot.Picture{}, textures: make([]uint32, n)}
	gl.GenTextures(int32(n), &m.textures[0])
	for i, t := range m.textures {
		win := s.windows[3*i+2]
		img := benchPattern(1000+i, win.Preview.Bounds().Dx(), win.Preview.Bounds().Dy())
		gl.ActiveTexture(gl.TEXTURE0)
		gl.BindTexture(gl.TEXTURE_2D, t)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
		gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, int32(img.Rect.Dx()), int32(img.Rect.Dy()), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(img.Pix))
		m.pics[win.ID] = snapshot.Picture{Texture: t, Width: img.Rect.Dx(), Height: img.Rect.Dy(), Gen: 1, Changed: time.Now()}
	}
	gl.BindTexture(gl.TEXTURE_2D, 0)
	return m
}

func (m *memorySource) SetLive(overlay xproto.Window, _ time.Duration) {
	m.on = overlay != 0
}

func (m *memorySource) BeginFrame([]xproto.Window) map[xproto.Window]snapshot.Picture {
	return m.pics
}

func (m *memorySource) EndFrame(fence uintptr) uintptr {
	if fence == 0 {
		return 0
	}
	old := m.fence
	m.fence = fence
	return old
}

// publish makes the pictures newer once 1/30 s has passed while the overlay
// is live
func (m *memorySource) publish(now time.Time) {
	if !m.on || now.Before(m.next) {
		return
	}
	m.next = now.Add(time.Second / 30)
	for id, p := range m.pics {
		p.Gen++
		p.Changed = now
		m.pics[id] = p
	}
	m.s.live.wanted = true
}

// close deletes the textures and the fence it holds
func (m *memorySource) close() {
	if m.fence != 0 {
		m.s.live.presenter.ReleaseFence(m.fence)
	}
	gl.DeleteTextures(int32(len(m.textures)), &m.textures[0])
}

// memoryPeaks are the largest values sampled
type memoryPeaks struct {
	sys, mapped, inuse uint64
}

// memorySampler samples runtime.ReadMemStats until stopped
type memorySampler struct {
	done  chan struct{}
	wg    sync.WaitGroup
	peaks memoryPeaks
}

func startMemorySampler(every time.Duration) *memorySampler {
	m := &memorySampler{done: make(chan struct{})}
	m.wg.Go(func() {
		t := time.NewTicker(every)
		defer t.Stop()
		var st runtime.MemStats
		for {
			runtime.ReadMemStats(&st)
			m.peaks.sys = max(m.peaks.sys, st.HeapSys)
			m.peaks.mapped = max(m.peaks.mapped, st.HeapSys-st.HeapReleased)
			m.peaks.inuse = max(m.peaks.inuse, st.HeapInuse)
			select {
			case <-m.done:
				return
			case <-t.C:
			}
		}
	})
	return m
}

// stop ends the sampling and returns its peaks
func (m *memorySampler) stop() memoryPeaks {
	close(m.done)
	m.wg.Wait()
	return m.peaks
}

// procStatus is the sizes of /proc/self/status, in bytes
func procStatus() map[string]float64 {
	f, err := os.Open("/proc/self/status")
	if err != nil {
		return nil
	}
	defer f.Close()
	sizes := map[string]float64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, value, ok := strings.Cut(sc.Text(), ":")
		fields := strings.Fields(value)
		if !ok || len(fields) != 2 || fields[1] != "kB" {
			continue
		}
		if v, err := strconv.ParseFloat(fields[0], 64); err == nil {
			sizes[name] = v * 1024
		}
	}
	return sizes
}

// allocatedByKind is the bytes allocated so far, from the heap profile as of
// a collection made now, scaled as pprof scales its samples: in all, and by
// kind — the glyph masks of the faces, the images, the pictures prepared for
// the frames — and the images by the function of qws that made them,
// "images:<function>"
func allocatedByKind() map[string]float64 {
	runtime.GC()
	var records []runtime.MemProfileRecord
	n, _ := runtime.MemProfile(nil, true)
	for {
		records = make([]runtime.MemProfileRecord, n+64)
		var ok bool
		if n, ok = runtime.MemProfile(records, true); ok {
			records = records[:n]
			break
		}
	}
	rate := float64(runtime.MemProfileRate)
	kinds := map[string]float64{}
	for _, r := range records {
		if r.AllocObjects == 0 {
			continue
		}
		bytes := float64(r.AllocBytes)
		if rate > 1 {
			bytes /= 1 - math.Exp(-bytes/float64(r.AllocObjects)/rate)
		}
		kinds["all"] += bytes
		kind, fn := allocationKind(r.Stack())
		if kind != "" {
			kinds[kind] += bytes
		}
		if kind == "images" {
			kinds["images:"+fn] += bytes
		}
	}
	return kinds
}

// allocationKind is the kind of an allocation by its stack, and for an
// image the innermost function of qws on it
func allocationKind(stack []uintptr) (kind, fn string) {
	const module = "github.com/almaz-uno/qws/"
	frames := runtime.CallersFrames(stack)
	for {
		f, more := frames.Next()
		switch {
		case f.Function == "github.com/golang/freetype/truetype.NewFace":
			return "masks", ""
		case f.Function == module+"pkg/carousel.scratchImage":
			return "prepared", ""
		case f.Function == "image.NewRGBA":
			kind = "images"
		case kind == "images" && strings.HasPrefix(f.Function, module):
			return kind, strings.TrimPrefix(f.Function, module)
		}
		if !more {
			return kind, "?"
		}
	}
}
