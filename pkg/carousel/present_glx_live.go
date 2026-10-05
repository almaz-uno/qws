package carousel

import (
	"image"
	"math"

	"github.com/go-gl/gl/v4.6-core/gl"
)

// The live pictures of specs/020-live-thumbnails, drawn by the GLX presenter
// with the kernel the cpu renderer draws thumbnails with: for each pixel of
// the live rectangle, the point of the picture its centre maps to by the
// inverse of the thumbnail's matrix, a tent of support 1 — widened to the
// scale when shrinking — over the texels, its weights normalized, the sum in
// 16 bits a channel truncated to 8, as transform_RGBA_RGBA_Src of
// x/image/draw. In single precision it is within 1 a channel of the cpu, and
// equal in some 99.9 % of the pixels (research, "The kernel on the GPU").
// Through a fade, the pixel is first taken back through the zoom, and the
// kernel widens with it.
const liveFragmentShader = `#version 460 core
uniform sampler2D picture;
uniform vec2 viewport;
uniform float scale;   // of the fade, about the centre of the window
uniform vec4 rect;     // the live rectangle: left, top, right, bottom
uniform int holes;
uniform vec4 hole[16];
uniform vec2 origin;   // the point of the picture the centre of the pixel at the rectangle's corner maps to
uniform mat2 d2s;      // the linear part of the inverse of the matrix
uniform vec2 kscale;   // texels a pixel along each axis, with the zoom
uniform float alpha;
out vec4 color;
void main() {
	vec2 q = vec2(gl_FragCoord.x, viewport.y - gl_FragCoord.y);
	if (scale != 1.0) {
		vec2 c = viewport * 0.5;
		q = c + (q - c) / scale;
	}
	if (q.x < rect.x || q.y < rect.y || q.x >= rect.z || q.y >= rect.w) {
		discard;
	}
	for (int i = 0; i < holes; i++) {
		vec4 h = hole[i];
		if (q.x >= h.x && q.y >= h.y && q.x < h.z && q.y < h.w) {
			discard;
		}
	}
	ivec2 size = textureSize(picture, 0);
	vec2 s = origin + d2s * (q - rect.xy - 0.5) - 0.5;
	float xhw = 1.0, xas = 1.0, yhw = 1.0, yas = 1.0;
	if (kscale.x > 1.0) { xhw = kscale.x; xas = 1.0 / kscale.x; }
	if (kscale.y > 1.0) { yhw = kscale.y; yas = 1.0 / kscale.y; }
	int ix = max(int(floor(s.x - xhw)), 0), jx = min(int(ceil(s.x + xhw)), size.x);
	int iy = max(int(floor(s.y - yhw)), 0), jy = min(int(ceil(s.y + yhw)), size.y);
	float tx = 0.0, ty = 0.0;
	for (int kx = ix; kx < jx; kx++) {
		float t = abs((s.x - float(kx)) * xas);
		if (t < 1.0) tx += 1.0 - t;
	}
	for (int ky = iy; ky < jy; ky++) {
		float t = abs((s.y - float(ky)) * yas);
		if (t < 1.0) ty += 1.0 - t;
	}
	vec3 sum = vec3(0.0);
	for (int ky = iy; ky < jy; ky++) {
		float t = abs((s.y - float(ky)) * yas);
		if (t >= 1.0) continue;
		float wy = (1.0 - t) / ty;
		for (int kx = ix; kx < jx; kx++) {
			float u = abs((s.x - float(kx)) * xas);
			if (u >= 1.0) continue;
			sum += round(texelFetch(picture, ivec2(kx, ky), 0).rgb * 255.0) * 257.0 * ((1.0 - u) / tx * wy);
		}
	}
	color = vec4(floor(min(floor(sum), vec3(65535.0)) / 256.0) / 255.0, 1.0) * alpha;
}` + "\x00"

// maxHoles is how many holes a live item has at most; more are taken
// together, the last one their union
const maxHoles = 16

// liveState is the part of the GLX presenter that draws live pictures
type liveState struct {
	on    bool       // the context shares the snapshotter's objects
	items []LiveItem // over the next frame that is not a scene
	drew  bool       // the frame being presented drew a live picture
	fence uintptr    // after the last frame that drew one, for TakeLiveFence

	program                                                              uint32
	quad, viewport, scale, rect, holes, hole, origin, d2s, kscale, alpha int32 // uniform locations
}

func (p *glxPresenter) initLive() error {
	program, err := compileProgram(sceneVertexShader, liveFragmentShader)
	if err != nil {
		return err
	}
	l := &p.live
	l.program = program
	gl.UseProgram(program)
	gl.Uniform1i(gl.GetUniformLocation(program, gl.Str("picture\x00")), 0)
	uniform := func(name string) int32 { return gl.GetUniformLocation(program, gl.Str(name+"\x00")) }
	l.quad, l.viewport, l.scale = uniform("quad"), uniform("viewport"), uniform("scale")
	l.rect, l.holes, l.hole = uniform("rect"), uniform("holes"), uniform("hole")
	l.origin, l.d2s, l.kscale, l.alpha = uniform("origin"), uniform("d2s"), uniform("kscale"), uniform("alpha")
	gl.UseProgram(p.program)
	return nil
}

func (p *glxPresenter) closeLive() {
	if p.live.fence != 0 {
		gl.DeleteSync(p.live.fence)
		p.live.fence = 0
	}
	gl.DeleteProgram(p.live.program)
}

func (p *glxPresenter) Live() bool { return p.live.on }

func (p *glxPresenter) SetLiveItems(items []LiveItem) {
	p.live.items = items
}

func (p *glxPresenter) TakeLiveFence() uintptr {
	f := p.live.fence
	p.live.fence = 0
	return f
}

func (p *glxPresenter) ReleaseFence(fence uintptr) {
	if fence != 0 {
		gl.DeleteSync(fence)
	}
}

// drawLiveItems draws the items set by SetLiveItems over the frame in the
// back buffer, through the fade f, in place of what the frame has there
func (p *glxPresenter) drawLiveItems(f Fade) {
	items := p.live.items
	p.live.items = nil
	for i := range items {
		p.drawLive(&items[i], f, false)
	}
}

// drawLive draws a live item through the fade f: in place of the pixels
// under it, or, blending, over them — as a card of a scene is drawn
func (p *glxPresenter) drawLive(it *LiveItem, f Fade, blend bool) {
	if it.Texture == 0 || it.Rect.Empty() {
		return
	}
	m := it.Matrix
	det := m[0]*m[4] - m[1]*m[3]
	if det == 0 || math.IsNaN(det) {
		return
	}
	// The inverse of the matrix, from the frame to the picture
	a, b, c, d := m[4]/det, -m[1]/det, -m[3]/det, m[0]/det
	cx := float64(it.Rect.Min.X) + 0.5 - m[2]
	cy := float64(it.Rect.Min.Y) + 0.5 - m[5]
	ox, oy := a*cx+b*cy, c*cx+d*cy
	xs, ys := math.Max(math.Abs(a), math.Abs(b)), math.Max(math.Abs(c), math.Abs(d))
	if f.Scale > 0 {
		xs, ys = xs/f.Scale, ys/f.Scale
	}

	l := &p.live
	gl.UseProgram(l.program)
	r := it.Rect
	gl.Uniform4f(l.quad, float32(r.Min.X), float32(r.Min.Y), float32(r.Dx()), float32(r.Dy()))
	gl.Uniform2f(l.viewport, float32(p.width), float32(p.height))
	gl.Uniform1f(l.scale, float32(f.Scale))
	gl.Uniform4f(l.rect, float32(r.Min.X), float32(r.Min.Y), float32(r.Max.X), float32(r.Max.Y))
	holes := liveHoles(it.Holes)
	gl.Uniform1i(l.holes, int32(len(holes)/4))
	if len(holes) > 0 {
		gl.Uniform4fv(l.hole, int32(len(holes)/4), &holes[0])
	}
	gl.Uniform2f(l.origin, float32(ox), float32(oy))
	d2s := [4]float32{float32(a), float32(c), float32(b), float32(d)} // by columns
	gl.UniformMatrix2fv(l.d2s, 1, false, &d2s[0])
	gl.Uniform2f(l.kscale, float32(xs), float32(ys))
	gl.Uniform1f(l.alpha, float32(f.Alpha))
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, it.Texture)
	if blend && f.Alpha < 1 {
		gl.Enable(gl.BLEND)
		gl.BlendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA)
	} else {
		gl.Disable(gl.BLEND)
	}
	gl.DrawArrays(gl.TRIANGLE_STRIP, 0, 4)
	p.live.drew = true
}

// liveHoles is the holes as the uniform takes them, left, top, right,
// bottom; beyond maxHoles, the last is the union of the rest
func liveHoles(holes []image.Rectangle) []float32 {
	if len(holes) > maxHoles {
		last := holes[maxHoles-1]
		for _, h := range holes[maxHoles:] {
			last = last.Union(h)
		}
		holes = append(holes[:maxHoles-1:maxHoles-1], last)
	}
	v := make([]float32, 0, 4*len(holes))
	for _, h := range holes {
		v = append(v, float32(h.Min.X), float32(h.Min.Y), float32(h.Max.X), float32(h.Max.Y))
	}
	return v
}
