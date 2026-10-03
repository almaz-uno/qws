package carousel

import (
	"fmt"
	"image"
	"math"

	"github.com/go-gl/gl/v4.6-core/gl"
)

// The scene of specs/007-animation: layers as textures, drawn as quads with
// premultiplied blending. A quad covers both rectangles of its item; each
// fragment samples each layer where the layer's rectangle maps it, and
// nothing outside.
const (
	// pos is where a corner lies in the frame; the zoom of
	// specs/010-animation-options scales the frame about the centre of the
	// window, so the corner is drawn where the scale takes it
	sceneVertexShader = `#version 460 core
uniform vec4 quad;
uniform vec2 viewport;
uniform float scale;
out vec2 pos;
void main() {
	vec2 corner = vec2(gl_VertexID & 1, (gl_VertexID >> 1) & 1);
	pos = quad.xy + corner * quad.zw;
	vec2 at = pos;
	if (scale != 1.0) {
		vec2 c = viewport * 0.5;
		at = c + (pos - c) * scale;
	}
	gl_Position = vec4(at.x / viewport.x * 2.0 - 1.0, 1.0 - at.y / viewport.y * 2.0, 0.0, 1.0);
}` + "\x00"

	sceneFragmentShader = `#version 460 core
uniform sampler2D layerA;
uniform sampler2D layerB;
uniform vec4 rectA;
uniform vec4 rectB;
uniform float weightB;
uniform float alpha;
in vec2 pos;
out vec4 color;
vec4 sampleLayer(sampler2D layer, vec4 rect) {
	if (rect.z <= 0.0 || rect.w <= 0.0) {
		return vec4(0.0);
	}
	vec2 uv = (pos - rect.xy) / rect.zw;
	if (uv.x < 0.0 || uv.y < 0.0 || uv.x > 1.0 || uv.y > 1.0) {
		return vec4(0.0);
	}
	return texture(layer, uv);
}
void main() {
	color = ((1.0 - weightB) * sampleLayer(layerA, rectA) + weightB * sampleLayer(layerB, rectB)) * alpha;
}` + "\x00"
)

type layerTexture struct {
	texture uint32
	bounds  image.Rectangle
}

// sceneState is the part of the GLX presenter that draws scenes
type sceneState struct {
	program                                        uint32
	quad, viewport, rectA, rectB, weight, a, scale int32
	layers                                         map[LayerID]layerTexture
}

func (p *glxPresenter) initScene() error {
	program, err := compileProgram(sceneVertexShader, sceneFragmentShader)
	if err != nil {
		return err
	}
	s := &p.scene
	s.program = program
	gl.UseProgram(program)
	gl.Uniform1i(gl.GetUniformLocation(program, gl.Str("layerA\x00")), 0)
	gl.Uniform1i(gl.GetUniformLocation(program, gl.Str("layerB\x00")), 1)
	uniform := func(name string) int32 { return gl.GetUniformLocation(program, gl.Str(name+"\x00")) }
	s.quad, s.viewport = uniform("quad"), uniform("viewport")
	s.rectA, s.rectB = uniform("rectA"), uniform("rectB")
	s.weight, s.a, s.scale = uniform("weightB"), uniform("alpha"), uniform("scale")
	s.layers = map[LayerID]layerTexture{}
	gl.UseProgram(p.program)
	return nil
}

func (p *glxPresenter) closeScene() {
	p.DropLayers()
	gl.DeleteProgram(p.scene.program)
}

func (p *glxPresenter) SetLayer(id LayerID, img *image.RGBA) error {
	if id == 0 {
		return fmt.Errorf("layer 0 is none")
	}
	l, ok := p.scene.layers[id]
	if !ok {
		gl.GenTextures(1, &l.texture)
	}
	l.bounds = img.Rect
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, l.texture)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.PixelStorei(gl.UNPACK_ROW_LENGTH, int32(img.Stride/4))
	var pix []byte
	if len(img.Pix) > 0 {
		pix = img.Pix[img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y):]
	}
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, int32(img.Rect.Dx()), int32(img.Rect.Dy()), 0,
		gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(pix))
	gl.PixelStorei(gl.UNPACK_ROW_LENGTH, 0)
	// The transfer starts now, in the pause it is made in, not with the next
	// frame
	gl.Flush()
	p.scene.layers[id] = l
	return nil
}

func (p *glxPresenter) HasLayer(id LayerID) bool {
	_, ok := p.scene.layers[id]
	return ok
}

func (p *glxPresenter) DropLayers() {
	for id, l := range p.scene.layers {
		gl.DeleteTextures(1, &l.texture)
		delete(p.scene.layers, id)
	}
}

func (p *glxPresenter) PresentScene(base LayerID, items []SceneItem, f Fade) error {
	// A scene draws the live pictures its items carry
	p.live.items = nil
	if err := p.drawScene(base, items, f); err != nil {
		return err
	}
	p.swap()
	return nil
}

// drawScene draws a scene into the back buffer through the fade f
func (p *glxPresenter) drawScene(base LayerID, items []SceneItem, f Fade) error {
	b, ok := p.scene.layers[base]
	if !ok || b.bounds.Dx() != p.width || b.bounds.Dy() != p.height {
		return fmt.Errorf("base layer %d is not a frame of the window", base)
	}
	p.drawTexture(b.texture, f)

	s := &p.scene
	gl.Enable(gl.BLEND)
	gl.BlendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA)
	gl.UseProgram(s.program)
	gl.Uniform2f(s.viewport, float32(p.width), float32(p.height))
	gl.Uniform1f(s.scale, float32(f.Scale))
	for _, it := range items {
		if it.Live != nil {
			p.drawLive(it.Live, f, true)
			gl.Enable(gl.BLEND)
			gl.BlendFunc(gl.ONE, gl.ONE_MINUS_SRC_ALPHA)
			gl.UseProgram(s.program)
			continue
		}
		ra, ta := p.layerRect(it.A, it.RectA)
		rb, tb := p.layerRect(it.B, it.RectB)
		quad := union(ra, rb)
		if quad.W <= 0 || quad.H <= 0 {
			continue
		}
		gl.ActiveTexture(gl.TEXTURE0)
		gl.BindTexture(gl.TEXTURE_2D, ta)
		gl.ActiveTexture(gl.TEXTURE1)
		gl.BindTexture(gl.TEXTURE_2D, tb)
		gl.Uniform4f(s.quad, float32(quad.X), float32(quad.Y), float32(quad.W), float32(quad.H))
		gl.Uniform4f(s.rectA, float32(ra.X), float32(ra.Y), float32(ra.W), float32(ra.H))
		gl.Uniform4f(s.rectB, float32(rb.X), float32(rb.Y), float32(rb.W), float32(rb.H))
		gl.Uniform1f(s.weight, float32(it.WeightB))
		gl.Uniform1f(s.a, float32(it.Alpha*f.Alpha))
		gl.DrawArrays(gl.TRIANGLE_STRIP, 0, 4)
	}
	gl.ActiveTexture(gl.TEXTURE0)
	gl.Disable(gl.BLEND)
	return nil
}

// layerRect is the rectangle of an item's layer and its texture; an empty
// rectangle for a missing layer
func (p *glxPresenter) layerRect(id LayerID, r Rect) (Rect, uint32) {
	l, ok := p.scene.layers[id]
	if !ok {
		return Rect{}, 0
	}
	return r, l.texture
}

func (p *glxPresenter) PresentFaded(img *image.RGBA, f Fade) error {
	if img != nil {
		if err := p.draw(img); err != nil {
			p.live.items = nil
			return err
		}
		p.presented = true
	}
	if !p.presented {
		p.live.items = nil
		return fmt.Errorf("no frame to fade")
	}
	p.drawTexture(p.texture, f)
	p.drawLiveItems(f)
	p.swap()
	return nil
}

// union is the smallest rectangle holding both; an empty one does not count
func union(a, b Rect) Rect {
	if a.W <= 0 || a.H <= 0 {
		return b
	}
	if b.W <= 0 || b.H <= 0 {
		return a
	}
	x0, y0 := math.Min(a.X, b.X), math.Min(a.Y, b.Y)
	x1, y1 := math.Max(a.X+a.W, b.X+b.W), math.Max(a.Y+a.H, b.Y+b.H)
	return Rect{x0, y0, x1 - x0, y1 - y0}
}
