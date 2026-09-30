package carousel

import (
	"fmt"
	"image"

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

	presentFragmentShader = `#version 460 core
uniform sampler2D frame;
out vec4 color;
void main() {
	ivec2 size = textureSize(frame, 0);
	ivec2 p = ivec2(gl_FragCoord.xy);
	color = texelFetch(frame, ivec2(p.x, size.y - 1 - p.y), 0);
}` + "\x00"
)

// glxPresenter uploads each frame into a texture of the window size and draws
// it into the window's back buffer through GLX, then swaps. With blending and
// dithering disabled the window gets the frame's bytes unchanged.
type glxPresenter struct {
	ctx     *glx.Context
	window  uint32
	width   int
	height  int
	ready   bool // GL objects created
	program uint32
	vao     uint32
	texture uint32
}

// newGLXPresenter creates the GLX presenter; a variable, so that tests can make
// the initialisation fail
var newGLXPresenter = func() (Presenter, error) {
	ctx, err := glx.NewContext()
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

	width, height := int(w.width), int(w.height)
	if width != p.width || height != p.height {
		gl.BindTexture(gl.TEXTURE_2D, p.texture)
		gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, int32(width), int32(height), 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
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

	// Core profile draws only with a vertex array bound, even without attributes
	gl.GenVertexArrays(1, &p.vao)
	gl.BindVertexArray(p.vao)

	gl.GenTextures(1, &p.texture)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, p.texture)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)

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
	p.ctx.SwapBuffers(p.window)
	if timingEnabled() {
		// The end of presentation, P, of the frame timings: the swap returns
		// before the GPU is done
		gl.Finish()
	}
	return nil
}

// draw uploads the frame and draws it into the back buffer
func (p *glxPresenter) draw(img *image.RGBA) error {
	b := img.Bounds()
	if b.Dx() != p.width || b.Dy() != p.height {
		return fmt.Errorf("frame %dx%d does not match the window %dx%d", b.Dx(), b.Dy(), p.width, p.height)
	}
	gl.BindTexture(gl.TEXTURE_2D, p.texture)
	gl.PixelStorei(gl.UNPACK_ROW_LENGTH, int32(img.Stride/4))
	gl.TexSubImage2D(gl.TEXTURE_2D, 0, 0, 0, int32(p.width), int32(p.height), gl.RGBA, gl.UNSIGNED_BYTE,
		gl.Ptr(img.Pix[img.PixOffset(b.Min.X, b.Min.Y):]))
	gl.DrawArrays(gl.TRIANGLES, 0, 3)
	return nil
}

func (p *glxPresenter) Close() {
	if p.ready {
		gl.DeleteTextures(1, &p.texture)
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
