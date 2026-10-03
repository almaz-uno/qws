package snapshot

import (
	"fmt"
	"image"
	"strings"
	"unsafe"

	"github.com/go-gl/gl/v4.6-core/gl"
)

// maxSide bounds a thumbnail, as CaptureWindow(window, 512, 512) of
// pkg/composite does
const maxSide = 512

// thumbSize is the size of the thumbnail of a window of W×H: scaled down,
// never up, to fit maxSide×maxSide, its aspect kept
func thumbSize(w, h int) (int, int) {
	scale := min(1, float64(maxSide)/float64(w), float64(maxSide)/float64(h))
	return max(1, int(float64(w)*scale)), max(1, int(float64(h)*scale))
}

// One triangle covers the viewport. Each pixel of the thumbnail is the area
// average of the rectangle of the window it covers: every texel weighted by
// its overlap with the rectangle. The top row of the window goes to the
// bottom row of the framebuffer, the first glReadPixels returns.
const (
	vertexShader = `#version 460 core
void main() {
	vec2 p = vec2((gl_VertexID << 1) & 2, gl_VertexID & 2);
	gl_Position = vec4(p * 2.0 - 1.0, 0.0, 1.0);
}` + "\x00"

	fragmentShader = `#version 460 core
uniform sampler2D window;
uniform vec2 scale;     // pixels of the window per pixel of the thumbnail
uniform int yInverted;  // the first row of the texture is the top of the window
out vec4 color;
void main() {
	ivec2 size = textureSize(window, 0);
	vec2 lo = floor(gl_FragCoord.xy) * scale;
	vec2 hi = lo + scale;
	vec3 sum = vec3(0.0);
	float area = 0.0;
	for (int y = int(lo.y); float(y) < hi.y && y < size.y; y++) {
		float wy = min(float(y + 1), hi.y) - max(float(y), lo.y);
		int ty = yInverted == 1 ? y : size.y - 1 - y;
		for (int x = int(lo.x); float(x) < hi.x && x < size.x; x++) {
			float wx = min(float(x + 1), hi.x) - max(float(x), lo.x);
			sum += wx * wy * texelFetch(window, ivec2(x, ty), 0).rgb;
			area += wx * wy;
		}
	}
	color = vec4(sum / area, 1.0);
}` + "\x00"
)

// gpu draws thumbnails on the thread of the offscreen context
type gpu struct {
	program     uint32
	scale, yInv int32 // uniform locations
	vao, fbo    uint32
	target      uint32 // texture of maxSide×maxSide the thumbnails are drawn into
}

// newGPU creates the GL objects; the offscreen context must be current
func newGPU() (*gpu, error) {
	if err := gl.Init(); err != nil {
		return nil, fmt.Errorf("failed to initialize OpenGL: %w", err)
	}
	program, err := compileProgram(vertexShader, fragmentShader)
	if err != nil {
		return nil, err
	}
	g := &gpu{program: program}
	gl.UseProgram(program)
	gl.Uniform1i(gl.GetUniformLocation(program, gl.Str("window\x00")), 0)
	g.scale = gl.GetUniformLocation(program, gl.Str("scale\x00"))
	g.yInv = gl.GetUniformLocation(program, gl.Str("yInverted\x00"))

	// Core profile draws only with a vertex array bound, even without attributes
	gl.GenVertexArrays(1, &g.vao)
	gl.BindVertexArray(g.vao)

	gl.GenTextures(1, &g.target)
	gl.BindTexture(gl.TEXTURE_2D, g.target)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, maxSide, maxSide, 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
	gl.GenFramebuffers(1, &g.fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, g.fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, g.target, 0)
	if s := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); s != gl.FRAMEBUFFER_COMPLETE {
		return nil, fmt.Errorf("thumbnail framebuffer incomplete: 0x%x", s)
	}
	gl.Disable(gl.BLEND)
	gl.Disable(gl.DITHER)
	gl.PixelStorei(gl.PACK_ALIGNMENT, 1)
	return g, nil
}

// thumbnail averages the window of w×h, bound as the texture of unit 0, into
// a thumbnail and reads it back
func (g *gpu) thumbnail(w, h int, yInverted bool) *image.RGBA {
	tw, th := g.average(g.fbo, w, h, yInverted)
	img := image.NewRGBA(image.Rect(0, 0, tw, th))
	gl.ReadPixels(0, 0, int32(tw), int32(th), gl.RGBA, gl.UNSIGNED_BYTE, unsafe.Pointer(&img.Pix[0]))
	return img
}

// average averages the window of w×h, bound as the texture of unit 0, into
// the bottom-left thumbSize(w, h) of the framebuffer fbo, the top row of the
// window in its bottom row; it returns that size
func (g *gpu) average(fbo uint32, w, h int, yInverted bool) (int, int) {
	tw, th := thumbSize(w, h)
	gl.BindFramebuffer(gl.FRAMEBUFFER, fbo)
	gl.Viewport(0, 0, int32(tw), int32(th))
	gl.UseProgram(g.program)
	gl.Uniform2f(g.scale, float32(float64(w)/float64(tw)), float32(float64(h)/float64(th)))
	yInv := int32(0)
	if yInverted {
		yInv = 1
	}
	gl.Uniform1i(g.yInv, yInv)
	gl.DrawArrays(gl.TRIANGLES, 0, 3)
	return tw, th
}

// newWindowTexture makes the texture a window's pixmap is bound to and
// leaves it bound to unit 0. Without mipmaps, a minifying filter that uses
// them would leave the texture incomplete, and texelFetch would read black.
func newWindowTexture() uint32 {
	var tex uint32
	gl.GenTextures(1, &tex)
	gl.ActiveTexture(gl.TEXTURE0)
	gl.BindTexture(gl.TEXTURE_2D, tex)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	return tex
}

// close deletes the GL objects
func (g *gpu) close() {
	gl.DeleteFramebuffers(1, &g.fbo)
	gl.DeleteTextures(1, &g.target)
	gl.DeleteVertexArrays(1, &g.vao)
	gl.DeleteProgram(g.program)
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
		msg := strings.Repeat("\x00", int(length+1))
		gl.GetProgramInfoLog(program, length, nil, gl.Str(msg))
		gl.DeleteProgram(program)
		return 0, fmt.Errorf("failed to link program: %s", strings.TrimRight(msg, "\x00"))
	}
	gl.DetachShader(program, vertex)
	gl.DetachShader(program, fragment)
	return program, nil
}

func compileShader(source string, kind uint32) (uint32, error) {
	shader := gl.CreateShader(kind)
	csources, free := gl.Strs(source)
	gl.ShaderSource(shader, 1, csources, nil)
	free()
	gl.CompileShader(shader)
	var status int32
	gl.GetShaderiv(shader, gl.COMPILE_STATUS, &status)
	if status == gl.FALSE {
		var length int32
		gl.GetShaderiv(shader, gl.INFO_LOG_LENGTH, &length)
		msg := strings.Repeat("\x00", int(length+1))
		gl.GetShaderInfoLog(shader, length, nil, gl.Str(msg))
		gl.DeleteShader(shader)
		return 0, fmt.Errorf("failed to compile: %s", strings.TrimRight(msg, "\x00"))
	}
	return shader, nil
}
