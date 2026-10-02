package carousel

import (
	"bytes"
	"fmt"
	"image"
	"unsafe"

	"github.com/almaz-uno/qws/pkg/glx"
	"github.com/go-gl/gl/v4.6-core/gl"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// The present pass copies the frame texture to the window pixel for pixel:
// one triangle covers the viewport, texelFetch addresses texels by integer
// coordinates, so no filtering is involved, and rows are flipped because GL
// counts window rows from the bottom
const (
	presentVertexShader = `#version 460 core
void main() {
	vec2 p = vec2((gl_VertexID << 1) & 2, gl_VertexID & 2);
	gl_Position = vec4(p * 2.0 - 1.0, 0.0, 1.0);
}` + "\x00"

	// alpha is 1 but for the fades of specs/007-animation, and a texel times
	// 1.0 is the texel. scale is 1 but for the zoom of
	// specs/010-animation-options: the fragment then samples the frame, through
	// a linear sampler, at the point the scale about the centre maps it to, and
	// is transparent outside the frame.
	presentFragmentShader = `#version 460 core
uniform sampler2D frame;
uniform float alpha;
uniform float scale;
out vec4 color;
void main() {
	ivec2 size = textureSize(frame, 0);
	if (scale == 1.0) {
		ivec2 p = ivec2(gl_FragCoord.xy);
		color = texelFetch(frame, ivec2(p.x, size.y - 1 - p.y), 0) * alpha;
		return;
	}
	vec2 c = vec2(size) * 0.5;
	vec2 q = c + (gl_FragCoord.xy - c) / scale;
	if (q.x < 0.0 || q.y < 0.0 || q.x > float(size.x) || q.y > float(size.y)) {
		color = vec4(0.0);
		return;
	}
	color = texture(frame, vec2(q.x, float(size.y) - q.y) / vec2(size)) * alpha;
}` + "\x00"
)

// glxPresenter uploads each frame into a texture of the window size and draws
// it into the window's back buffer through GLX, then swaps. With blending and
// dithering disabled the window gets the frame's bytes unchanged.
type glxPresenter struct {
	ctx       *glx.Context
	window    uint32
	width     int
	height    int
	ready     bool        // GL objects created
	presented bool        // the texture holds the last frame of the bound window
	last      *image.RGBA // that frame
	program   uint32
	alpha     int32  // location of the alpha of program
	scale     int32  // location of its scale
	linear    uint32 // sampler of the frame while it is zoomed
	vao       uint32
	texture   uint32
	pbo       uint32 // pixel unpack buffer the frames are uploaded through
	scene     sceneState

	// The frame at rest of an animation, uploaded in parts before it is
	// presented (specs/007-animation): the texture it goes into, the frame,
	// and the rows [stageNext, stageEnd) still to go
	stage               uint32
	staged              *image.RGBA
	stageNext, stageEnd int
}

// newGLXPresenter creates the GLX presenter; a variable, so that tests can make
// the initialisation fail
var newGLXPresenter = func() (Presenter, error) {
	ctx, err := glx.NewContext(nil)
	if err != nil {
		return nil, err
	}
	return &glxPresenter{ctx: ctx}, nil
}

func (p *glxPresenter) VisualID() xproto.Visualid {
	return xproto.Visualid(p.ctx.VisualID())
}

func (p *glxPresenter) Bind(w *Window) error {
	id := uint32(w.GetWindowID())
	if err := p.ctx.MakeCurrent(id); err != nil {
		return err
	}
	if err := p.ctx.SetSwapInterval(id, 0); err != nil {
		log.Debug().Err(err).Msg("Swap interval not set, swaps may wait for vertical blank")
	}
	if !p.ready {
		if err := p.init(); err != nil {
			return err
		}
	}
	p.window = id
	p.presented = false
	p.last = nil
	p.staged = nil

	width, height := int(w.width), int(w.height)
	if width != p.width || height != p.height {
		for _, texture := range []uint32{p.texture, p.stage} {
			gl.BindTexture(gl.TEXTURE_2D, texture)
			gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, int32(width), int32(height), 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
		}
		p.width, p.height = width, height
	}
	gl.Viewport(0, 0, int32(width), int32(height))
	return nil
}

// init creates the GL objects once a context is current
func (p *glxPresenter) init() error {
	if err := gl.Init(); err != nil {
		return fmt.Errorf("failed to initialize OpenGL: %w", err)
	}
	log.Debug().
		Str("version", gl.GoStr(gl.GetString(gl.VERSION))).
		Str("renderer", gl.GoStr(gl.GetString(gl.RENDERER))).
		Msg("OpenGL presenter initialized on the overlay window")

	program, err := compileProgram(presentVertexShader, presentFragmentShader)
	if err != nil {
		return err
	}
	p.program = program
	gl.UseProgram(p.program)
	gl.Uniform1i(gl.GetUniformLocation(p.program, gl.Str("frame\x00")), 0)
	p.alpha = gl.GetUniformLocation(p.program, gl.Str("alpha\x00"))
	p.scale = gl.GetUniformLocation(p.program, gl.Str("scale\x00"))
	gl.Uniform1f(p.alpha, 1)
	gl.Uniform1f(p.scale, 1)
	gl.GenSamplers(1, &p.linear)
	gl.SamplerParameteri(p.linear, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.SamplerParameteri(p.linear, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.SamplerParameteri(p.linear, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.SamplerParameteri(p.linear, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	if err := p.initScene(); err != nil {
		return err
	}

	// Core profile draws only with a vertex array bound, even without attributes
	gl.GenVertexArrays(1, &p.vao)
	gl.BindVertexArray(p.vao)

	gl.GenBuffers(1, &p.pbo)
	gl.GenTextures(1, &p.texture)
	gl.GenTextures(1, &p.stage)
	gl.ActiveTexture(gl.TEXTURE0)
	for _, texture := range []uint32{p.texture, p.stage} {
		gl.BindTexture(gl.TEXTURE_2D, texture)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
		gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	}

	// The frame is copied as it is
	gl.Disable(gl.BLEND)
	gl.Disable(gl.DITHER)
	gl.Disable(gl.DEPTH_TEST)
	gl.Disable(gl.MULTISAMPLE)
	gl.PixelStorei(gl.UNPACK_ALIGNMENT, 1)

	p.ready = true
	return nil
}

func (p *glxPresenter) Present(img *image.RGBA) error {
	if err := p.draw(img); err != nil {
		return err
	}
	p.presented = true
	p.swap()
	return nil
}

func (p *glxPresenter) Refresh() (bool, error) {
	if !p.presented {
		return false, nil
	}
	p.drawTexture(p.texture, Opaque)
	p.swap()
	return true, nil
}

// drawTexture copies a texture of the window size to the back buffer through
// the fade f, without blending
func (p *glxPresenter) drawTexture(texture uint32, f Fade) {
	gl.Disable(gl.BLEND)
	gl.UseProgram(p.program)
	gl.Uniform1f(p.alpha, float32(f.Alpha))
	gl.Uniform1f(p.scale, float32(f.Scale))
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, texture)
	if f.Scale != 1 {
		gl.BindSampler(0, p.linear)
		defer gl.BindSampler(0, 0)
	}
	gl.DrawArrays(gl.TRIANGLES, 0, 3)
}

func (p *glxPresenter) swap() {
	p.ctx.SwapBuffers(p.window)
	if timingEnabled() {
		// The end of presentation, P, of the frame timings: the swap returns
		// before the GPU is done
		gl.Finish()
	}
}

// draw uploads the frame and draws it into the back buffer
func (p *glxPresenter) draw(img *image.RGBA) error {
	b := img.Bounds()
	if b.Dx() != p.width || b.Dy() != p.height {
		return fmt.Errorf("frame %dx%d does not match the window %dx%d", b.Dx(), b.Dy(), p.width, p.height)
	}
	y0, y1 := 0, p.height
	if p.last != nil && &p.last.Pix[0] != &img.Pix[0] {
		// The texture holds the last frame: only the rows that differ go
		y0, y1 = changedRows(p.last, img)
	}
	if y0 < y1 {
		if err := p.upload(p.texture, img, y0, y1); err != nil {
			p.last = nil
			return err
		}
	}
	p.last = img
	p.drawTexture(p.texture, Opaque)
	return nil
}

// changedRows is the band of rows [y0, y1) outside which two frames of one
// size are equal; y0 == y1 when they are equal everywhere
func changedRows(a, b *image.RGBA) (int, int) {
	h := b.Bounds().Dy()
	row := func(img *image.RGBA, y int) []byte {
		r := img.Bounds()
		return img.Pix[img.PixOffset(r.Min.X, r.Min.Y+y):][:4*r.Dx()]
	}
	y0 := 0
	for y0 < h && bytes.Equal(row(a, y0), row(b, y0)) {
		y0++
	}
	y1 := h
	for y1 > y0 && bytes.Equal(row(a, y1-1), row(b, y1-1)) {
		y1--
	}
	return y0, y1
}

// upload copies rows [y0, y1) of the frame into a texture of the window size
// through the pixel buffer: the driver takes the rows from its own memory
// rather than from the process's. The buffer is orphaned first, so that the
// copy does not wait for the previous upload.
func (p *glxPresenter) upload(texture uint32, img *image.RGBA, y0, y1 int) error {
	b := img.Bounds()
	rowBytes := 4 * p.width
	n := rowBytes * (y1 - y0)

	gl.BindBuffer(gl.PIXEL_UNPACK_BUFFER, p.pbo)
	defer gl.BindBuffer(gl.PIXEL_UNPACK_BUFFER, 0)
	gl.BufferData(gl.PIXEL_UNPACK_BUFFER, n, nil, gl.STREAM_DRAW)
	ptr := gl.MapBufferRange(gl.PIXEL_UNPACK_BUFFER, 0, n, gl.MAP_WRITE_BIT|gl.MAP_INVALIDATE_BUFFER_BIT)
	if ptr == nil {
		return fmt.Errorf("failed to map the pixel buffer: GL error 0x%x", gl.GetError())
	}
	dst := unsafe.Slice((*byte)(ptr), n)
	for y := y0; y < y1; y++ {
		copy(dst[(y-y0)*rowBytes:][:rowBytes], img.Pix[img.PixOffset(b.Min.X, b.Min.Y+y):])
	}
	gl.UnmapBuffer(gl.PIXEL_UNPACK_BUFFER)

	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, texture)
	gl.PixelStorei(gl.UNPACK_ROW_LENGTH, 0)
	gl.TexSubImage2D(gl.TEXTURE_2D, 0, 0, int32(y0), int32(p.width), int32(y1-y0), gl.RGBA, gl.UNSIGNED_BYTE, nil)
	return nil
}

// stageRows is how many rows StageFrame uploads at a time, 0.6 MB on E1
const stageRows = 64

func (p *glxPresenter) StageFrame(img *image.RGBA, maxBytes int) (int, bool, error) {
	b := img.Bounds()
	if b.Dx() != p.width || b.Dy() != p.height {
		return 0, false, fmt.Errorf("frame %dx%d does not match the window %dx%d", b.Dx(), b.Dy(), p.width, p.height)
	}
	if p.staged != img {
		// The staging texture starts as the texture of the last frame, and
		// takes the rows of img that differ from it; frames presented later
		// change neither
		p.staged = img
		p.stageNext, p.stageEnd = 0, p.height
		if p.last != nil && &p.last.Pix[0] != &img.Pix[0] {
			gl.CopyImageSubData(p.texture, gl.TEXTURE_2D, 0, 0, 0, 0,
				p.stage, gl.TEXTURE_2D, 0, 0, 0, 0, int32(p.width), int32(p.height), 1)
			p.stageNext, p.stageEnd = changedRows(p.last, img)
		}
	}
	// A piece at least, whatever the budget
	uploaded := 0
	for p.stageNext < p.stageEnd {
		y1 := min(p.stageNext+stageRows, p.stageEnd)
		if err := p.upload(p.stage, img, p.stageNext, y1); err != nil {
			p.staged = nil
			return uploaded, false, err
		}
		// The transfer starts now, in the pause it is made in
		gl.Flush()
		uploaded += 4 * p.width * (y1 - p.stageNext)
		p.stageNext = y1
		if p.stageNext < p.stageEnd && uploaded+4*p.width*stageRows > maxBytes {
			return uploaded, false, nil
		}
	}
	return uploaded, true, nil
}

func (p *glxPresenter) PresentStaged(f Fade) error {
	if err := p.drawStaged(f); err != nil {
		return err
	}
	p.presented = true
	p.swap()
	return nil
}

// drawStaged makes the frame staged in full the frame of the texture and draws
// it into the back buffer through the fade f
func (p *glxPresenter) drawStaged(f Fade) error {
	if p.staged == nil || p.stageNext < p.stageEnd {
		return fmt.Errorf("no frame staged in full")
	}
	p.texture, p.stage = p.stage, p.texture
	p.last, p.staged = p.staged, nil
	p.drawTexture(p.texture, f)
	return nil
}

func (p *glxPresenter) Close() {
	if p.ready {
		p.closeScene()
		gl.DeleteBuffers(1, &p.pbo)
		gl.DeleteTextures(1, &p.texture)
		gl.DeleteTextures(1, &p.stage)
		gl.DeleteSamplers(1, &p.linear)
		gl.DeleteVertexArrays(1, &p.vao)
		gl.DeleteProgram(p.program)
		p.ready = false
	}
	p.ctx.Destroy()
}

// timingEnabled reports whether frame timings are logged, at debug level
func timingEnabled() bool {
	return zerolog.GlobalLevel() <= zerolog.DebugLevel && log.Logger.GetLevel() <= zerolog.DebugLevel
}

// compileProgram compiles and links a vertex and a fragment shader
func compileProgram(vertexSource, fragmentSource string) (uint32, error) {
	vertex, err := compileShader(vertexSource, gl.VERTEX_SHADER)
	if err != nil {
		return 0, fmt.Errorf("vertex shader: %w", err)
	}
	defer gl.DeleteShader(vertex)
	fragment, err := compileShader(fragmentSource, gl.FRAGMENT_SHADER)
	if err != nil {
		return 0, fmt.Errorf("fragment shader: %w", err)
	}
	defer gl.DeleteShader(fragment)

	program := gl.CreateProgram()
	gl.AttachShader(program, vertex)
	gl.AttachShader(program, fragment)
	gl.LinkProgram(program)

	var status int32
	gl.GetProgramiv(program, gl.LINK_STATUS, &status)
	if status == gl.FALSE {
		var length int32
		gl.GetProgramiv(program, gl.INFO_LOG_LENGTH, &length)
		msg := make([]byte, length+1)
		gl.GetProgramInfoLog(program, length, nil, &msg[0])
		gl.DeleteProgram(program)
		return 0, fmt.Errorf("failed to link program: %s", gl.GoStr(&msg[0]))
	}
	gl.DetachShader(program, vertex)
	gl.DetachShader(program, fragment)
	return program, nil
}

func compileShader(source string, kind uint32) (uint32, error) {
	shader := gl.CreateShader(kind)
	src, free := gl.Strs(source)
	gl.ShaderSource(shader, 1, src, nil)
	free()
	gl.CompileShader(shader)

	var status int32
	gl.GetShaderiv(shader, gl.COMPILE_STATUS, &status)
	if status == gl.FALSE {
		var length int32
		gl.GetShaderiv(shader, gl.INFO_LOG_LENGTH, &length)
		msg := make([]byte, length+1)
		gl.GetShaderInfoLog(shader, length, nil, &msg[0])
		gl.DeleteShader(shader)
		return 0, fmt.Errorf("failed to compile: %s", gl.GoStr(&msg[0]))
	}
	return shader, nil
}
