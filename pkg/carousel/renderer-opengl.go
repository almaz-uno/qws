package carousel

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"runtime"
	"sync"

	"github.com/go-gl/gl/v4.6-core/gl"
	"github.com/go-gl/glfw/v3.3/glfw"
	"github.com/rs/zerolog/log"
	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
)

var (
	glfwInitOnce sync.Once
	glfwInitErr  error
)

// OpenGLRenderer provides hardware-accelerated rendering using OpenGL
type OpenGLRenderer struct {
	initialized  bool
	width        int
	height       int
	window       *glfw.Window // Hidden window for OpenGL context
	fbo          uint32       // Multisampled framebuffer object
	resolveFBO   uint32       // Resolve framebuffer for final image
	colorBuffer  uint32       // Multisampled renderbuffer
	colorTexture uint32       // Resolved color texture
	quadVAO      uint32       // VAO for rendering quads
	quadVBO      uint32       // VBO for quad vertices
	quadEBO      uint32       // EBO for quad indices
	program      uint32       // Shader program for textured quads
	textProgram  uint32       // Shader program for text rendering
	mu           sync.Mutex   // Mutex to serialize OpenGL calls
}

// Vertex shader for rendering textured quads (simplified, no matrices)
const glVertexShader = `
#version 460 core
layout (location = 0) in vec3 aPos;
layout (location = 1) in vec2 aTexCoord;

out vec2 TexCoord;

void main() {
    gl_Position = vec4(aPos, 1.0);
    TexCoord = aTexCoord;
}
` + "\x00"

// Fragment shader for rendering textured quads
const glFragmentShader = `
#version 460 core
out vec4 FragColor;
in vec2 TexCoord;

uniform sampler2D texture1;
uniform vec4 colorMod;

void main() {
    FragColor = texture(texture1, TexCoord) * colorMod;
}
` + "\x00"

// Text vertex shader
const glTextVertexShader = `
#version 460 core
layout (location = 0) in vec2 aPos;
layout (location = 1) in vec2 aTexCoord;

out vec2 TexCoord;

uniform mat4 projection;

void main() {
    gl_Position = projection * vec4(aPos, 0.0, 1.0);
    TexCoord = aTexCoord;
}
` + "\x00"

// Text fragment shader
const glTextFragmentShader = `
#version 460 core
out vec4 FragColor;
in vec2 TexCoord;

uniform sampler2D text;
uniform vec4 textColor;

void main() {
    vec4 sampled = vec4(1.0, 1.0, 1.0, texture(text, TexCoord).r);
    FragColor = textColor * sampled;
}
` + "\x00"

// NewOpenGLRenderer creates a new OpenGL renderer
func NewOpenGLRenderer(width, height int) (*OpenGLRenderer, error) {
	r := &OpenGLRenderer{
		width:  width,
		height: height,
	}

	if err := r.init(); err != nil {
		return nil, err
	}

	return r, nil
}

// init initializes OpenGL resources
func (r *OpenGLRenderer) init() error {
	if r.initialized {
		return nil
	}

	// Lock this goroutine to the current OS thread
	runtime.LockOSThread()

	// Initialize GLFW once
	glfwInitOnce.Do(func() {
		glfwInitErr = glfw.Init()
	})
	if glfwInitErr != nil {
		return fmt.Errorf("failed to initialize GLFW: %w", glfwInitErr)
	}

	// Configure GLFW for offscreen rendering
	glfw.WindowHint(glfw.ContextVersionMajor, 4)
	glfw.WindowHint(glfw.ContextVersionMinor, 6)
	glfw.WindowHint(glfw.OpenGLProfile, glfw.OpenGLCoreProfile)
	glfw.WindowHint(glfw.OpenGLForwardCompatible, glfw.True)
	glfw.WindowHint(glfw.Visible, glfw.False) // Hidden window
	glfw.WindowHint(glfw.Samples, 4)          // 4x MSAA

	// Create invisible window for OpenGL context
	var err error
	r.window, err = glfw.CreateWindow(1, 1, "", nil, nil)
	if err != nil {
		return fmt.Errorf("failed to create GLFW window: %w", err)
	}

	r.window.MakeContextCurrent()

	// Initialize OpenGL
	if err = gl.Init(); err != nil {
		r.window.Destroy()
		return fmt.Errorf("failed to initialize OpenGL: %w", err)
	}

	log.Debug().
		Str("version", gl.GoStr(gl.GetString(gl.VERSION))).
		Str("vendor", gl.GoStr(gl.GetString(gl.VENDOR))).
		Str("renderer", gl.GoStr(gl.GetString(gl.RENDERER))).
		Msg("OpenGL context created")

	// Enable multisampling
	gl.Enable(gl.MULTISAMPLE)

	// Enable line smoothing for better quality borders
	gl.Enable(gl.LINE_SMOOTH)
	gl.Hint(gl.LINE_SMOOTH_HINT, gl.NICEST)

	// Create multisampled framebuffer
	gl.GenFramebuffers(1, &r.fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, r.fbo)

	// Create multisampled color renderbuffer (4x MSAA)
	gl.GenRenderbuffers(1, &r.colorBuffer)
	gl.BindRenderbuffer(gl.RENDERBUFFER, r.colorBuffer)
	gl.RenderbufferStorageMultisample(gl.RENDERBUFFER, 4, gl.RGBA8, int32(r.width), int32(r.height))
	gl.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.RENDERBUFFER, r.colorBuffer)

	// Check multisampled framebuffer completeness
	status := gl.CheckFramebufferStatus(gl.FRAMEBUFFER)
	if status != gl.FRAMEBUFFER_COMPLETE {
		// Log detailed error
		var statusStr string
		switch status {
		case gl.FRAMEBUFFER_UNDEFINED:
			statusStr = "FRAMEBUFFER_UNDEFINED"
		case gl.FRAMEBUFFER_INCOMPLETE_ATTACHMENT:
			statusStr = "FRAMEBUFFER_INCOMPLETE_ATTACHMENT"
		case gl.FRAMEBUFFER_INCOMPLETE_MISSING_ATTACHMENT:
			statusStr = "FRAMEBUFFER_INCOMPLETE_MISSING_ATTACHMENT"
		case gl.FRAMEBUFFER_INCOMPLETE_DRAW_BUFFER:
			statusStr = "FRAMEBUFFER_INCOMPLETE_DRAW_BUFFER"
		case gl.FRAMEBUFFER_INCOMPLETE_READ_BUFFER:
			statusStr = "FRAMEBUFFER_INCOMPLETE_READ_BUFFER"
		case gl.FRAMEBUFFER_UNSUPPORTED:
			statusStr = "FRAMEBUFFER_UNSUPPORTED"
		case gl.FRAMEBUFFER_INCOMPLETE_MULTISAMPLE:
			statusStr = "FRAMEBUFFER_INCOMPLETE_MULTISAMPLE"
		case gl.FRAMEBUFFER_INCOMPLETE_LAYER_TARGETS:
			statusStr = "FRAMEBUFFER_INCOMPLETE_LAYER_TARGETS"
		default:
			statusStr = fmt.Sprintf("UNKNOWN(0x%X)", status)
		}
		return fmt.Errorf("multisampled framebuffer is not complete: %s", statusStr)
	}

	// Create resolve framebuffer (non-multisampled) for final output
	gl.GenFramebuffers(1, &r.resolveFBO)
	gl.BindFramebuffer(gl.FRAMEBUFFER, r.resolveFBO)

	// Create regular color texture for resolved image
	gl.GenTextures(1, &r.colorTexture)
	gl.BindTexture(gl.TEXTURE_2D, r.colorTexture)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, int32(r.width), int32(r.height), 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, r.colorTexture, 0)

	// Check resolve framebuffer completeness
	status = gl.CheckFramebufferStatus(gl.FRAMEBUFFER)
	if status != gl.FRAMEBUFFER_COMPLETE {
		return fmt.Errorf("resolve framebuffer is not complete")
	}

	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)

	// Compile shader programs
	r.program, err = compileShaderProgram(glVertexShader, glFragmentShader)
	if err != nil {
		return fmt.Errorf("failed to compile main shader: %w", err)
	}

	r.textProgram, err = compileShaderProgram(glTextVertexShader, glTextFragmentShader)
	if err != nil {
		return fmt.Errorf("failed to compile text shader: %w", err)
	}

	// Setup quad geometry
	r.setupQuadGeometry()

	r.initialized = true
	log.Debug().Int("width", r.width).Int("height", r.height).Msg("OpenGL renderer initialized")

	return nil
}

// setupQuadGeometry creates VAO/VBO/EBO for rendering quads
func (r *OpenGLRenderer) setupQuadGeometry() {
	vertices := []float32{
		// positions        // texture coords
		-1.0, 1.0, 0.0, 0.0, 0.0, // top left
		1.0, 1.0, 0.0, 1.0, 0.0, // top right
		1.0, -1.0, 0.0, 1.0, 1.0, // bottom right
		-1.0, -1.0, 0.0, 0.0, 1.0, // bottom left
	}

	indices := []uint32{
		0, 1, 2, // first triangle
		0, 2, 3, // second triangle
	}

	gl.GenVertexArrays(1, &r.quadVAO)
	gl.GenBuffers(1, &r.quadVBO)
	gl.GenBuffers(1, &r.quadEBO)

	gl.BindVertexArray(r.quadVAO)

	gl.BindBuffer(gl.ARRAY_BUFFER, r.quadVBO)
	gl.BufferData(gl.ARRAY_BUFFER, len(vertices)*4, gl.Ptr(vertices), gl.STATIC_DRAW)

	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, r.quadEBO)
	gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(indices)*4, gl.Ptr(indices), gl.STATIC_DRAW)

	// Position attribute
	gl.VertexAttribPointer(0, 3, gl.FLOAT, false, 5*4, gl.PtrOffset(0))
	gl.EnableVertexAttribArray(0)

	// Texture coord attribute
	gl.VertexAttribPointer(1, 2, gl.FLOAT, false, 5*4, gl.PtrOffset(3*4))
	gl.EnableVertexAttribArray(1)

	gl.BindVertexArray(0)
}

// Cleanup releases OpenGL resources
func (r *OpenGLRenderer) Cleanup() {
	if !r.initialized {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.window != nil {
		r.window.MakeContextCurrent()
	}

	gl.DeleteFramebuffers(1, &r.fbo)
	gl.DeleteTextures(1, &r.colorTexture)
	gl.DeleteVertexArrays(1, &r.quadVAO)
	gl.DeleteBuffers(1, &r.quadVBO)
	gl.DeleteBuffers(1, &r.quadEBO)
	gl.DeleteProgram(r.program)
	gl.DeleteProgram(r.textProgram)

	if r.window != nil {
		r.window.Destroy()
		r.window = nil
	}

	r.initialized = false

	// Unlock the OS thread
	runtime.UnlockOSThread()

	log.Debug().Msg("OpenGL renderer cleaned up")
}

// Draw3DCarousel renders a 2.5D carousel with perspective effect using OpenGL
func (r *OpenGLRenderer) Draw3DCarousel(thumbnails []image.Image, selected int, animOffset float64, cfg Config) *image.RGBA {
	// Convert thumbnails to WindowData format for unified rendering
	windowData := make([]WindowData, len(thumbnails))
	for i, thumb := range thumbnails {
		windowData[i] = WindowData{
			Thumbnail: thumb,
		}
	}
	return r.Draw3DCarouselWithDataGL(windowData, selected, -1, animOffset, cfg)
}

// Draw3DCarouselWithData renders carousel with window data using OpenGL
func (r *OpenGLRenderer) Draw3DCarouselWithData(windowData []WindowData, selected int, hover int, animOffset float64, cfg Config) *image.RGBA {
	return r.Draw3DCarouselWithDataGL(windowData, selected, hover, animOffset, cfg)
}

// DrawGridLayout renders windows in grid layout using OpenGL
func (r *OpenGLRenderer) DrawGridLayout(windowData []WindowData, selected int, hover int, cfg Config) *image.RGBA {
	return r.DrawGridLayoutGL(windowData, selected, hover, cfg)
}

// DrawPlaceholder creates a placeholder thumbnail (uses CPU rendering for simplicity)
func (r *OpenGLRenderer) DrawPlaceholder(width, height int, text string) image.Image {
	// Fallback to CPU renderer for placeholder - it's rarely used and simple
	return DrawPlaceholder(width, height, text)
}

// Draw3DCarouselWithDataGL renders carousel using OpenGL
func (r *OpenGLRenderer) Draw3DCarouselWithDataGL(windowData []WindowData, selected int, hoverIndex int, animOffset float64, cfg Config) *image.RGBA {
	if !r.initialized {
		log.Error().Msg("OpenGL renderer not initialized")
		return image.NewRGBA(image.Rect(0, 0, r.width, r.height))
	}

	// Serialize OpenGL calls with mutex
	r.mu.Lock()
	defer r.mu.Unlock()

	// Make context current before any OpenGL calls
	if r.window != nil {
		r.window.MakeContextCurrent()
	}

	// Bind framebuffer
	gl.BindFramebuffer(gl.FRAMEBUFFER, r.fbo)
	gl.Viewport(0, 0, int32(r.width), int32(r.height))

	// Clear with transparent background
	if cfg.WindowBackgroundEnabled {
		bgR, bgG, bgB, bgA := parseColor(cfg.BackgroundColor)
		gl.ClearColor(float32(bgR), float32(bgG), float32(bgB), float32(bgA*cfg.WindowBackgroundOpacity))
	} else {
		gl.ClearColor(0, 0, 0, 0)
	}
	gl.Clear(gl.COLOR_BUFFER_BIT)

	// Enable blending for transparency
	gl.Enable(gl.BLEND)
	gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)

	centerX := float64(r.width) / 2
	centerY := float64(r.height) / 2

	// Draw each window
	for i := range windowData {
		r.drawWindowWithDataGL(&windowData[i], i, selected, hoverIndex, animOffset, centerX, centerY, cfg)
	}

	// Resolve multisampled framebuffer to regular texture
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, r.fbo)
	gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, r.resolveFBO)
	gl.BlitFramebuffer(
		0, 0, int32(r.width), int32(r.height),
		0, 0, int32(r.width), int32(r.height),
		gl.COLOR_BUFFER_BIT, gl.NEAREST,
	)

	// Read pixels from resolved framebuffer
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, r.resolveFBO)
	result := image.NewRGBA(image.Rect(0, 0, r.width, r.height))
	gl.ReadPixels(0, 0, int32(r.width), int32(r.height), gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(result.Pix))

	// Unbind framebuffer
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)

	// Flip image vertically (OpenGL has origin at bottom-left)
	r.flipImageVertically(result)

	return result
}

// drawWindowWithDataGL draws a single window using OpenGL
func (r *OpenGLRenderer) drawWindowWithDataGL(data *WindowData, index, selected, hoverIndex int, animOffset, centerX, centerY float64, cfg Config) {
	if data == nil || data.Thumbnail == nil {
		return
	}

	// Position relative to center
	offset := float64(index-selected) - animOffset

	// Don't draw items too far from center
	if math.Abs(offset) > 5 {
		return
	}

	// Calculate transformation parameters
	var scale, x, y, alpha float64

	if math.Abs(offset) < 0.01 {
		// Central window
		scale = 1.0
		x = centerX
		y = centerY
		alpha = 1.0
	} else {
		// Side windows with perspective
		scale = cfg.PerspectiveFactor + (1.0-cfg.PerspectiveFactor)/(1.0+math.Abs(offset)*0.5)
		x = centerX + offset*cfg.Spacing*scale
		arcHeight := math.Abs(offset) * 10
		y = centerY + arcHeight
		alpha = 0.5 + 0.5*scale
	}

	// Calculate thumbnail dimensions
	thumbBounds := data.Thumbnail.Bounds()
	thumbW := float64(thumbBounds.Dx())
	thumbH := float64(thumbBounds.Dy())

	scaleW := float64(cfg.ThumbWidth) / thumbW
	scaleH := float64(cfg.ThumbHeight) / thumbH
	scaleMin := math.Min(scaleW, scaleH)

	finalW := thumbW * scaleMin * scale
	finalH := thumbH * scaleMin * scale

	// Icon position
	iconSize := 48.0 * scale
	iconY := y - finalH/2 - 80*scale

	// Title position
	titleY := y - finalH/2 - 30*scale

	// Workspace position
	workspaceY := y + finalH/2 + 30*scale

	// Draw shadow
	if math.Abs(offset) < 3 {
		r.drawShadowGL(x, y, finalW, finalH, scale, cfg)
	}

	// Draw icon
	if data.Icon != nil {
		iconBounds := data.Icon.Bounds()
		iconW := float64(iconBounds.Dx())
		iconH := float64(iconBounds.Dy())
		iconScale := iconSize / math.Max(iconW, iconH)

		r.drawImageGL(data.Icon, x, iconY, iconW*iconScale, iconH*iconScale, alpha)
	}

	// Draw title
	if data.Title != "" {
		title := data.Title
		// Truncate long titles
		maxLen := int(30 / scale)
		if maxLen < 12 {
			maxLen = 12
		}
		runes := []rune(title)
		if len(runes) > maxLen {
			title = string(runes[:maxLen]) + "..."
		}

		fontSize := float64(cfg.FontSize) * scale * 1.15
		r.drawTextGL(title, x, titleY, fontSize, cfg.TextColor, alpha, cfg)
	}

	// Draw workspace
	if data.Workspace != "" {
		workspace := data.Workspace
		// Truncate long workspace names
		maxLen := int(20 / scale)
		if maxLen < 8 {
			maxLen = 8
		}
		runes := []rune(workspace)
		if len(runes) > maxLen {
			workspace = string(runes[:maxLen]) + "..."
		}

		fontSize := float64(cfg.FontSize) * scale
		r.drawTextGL(workspace, x, workspaceY, fontSize, cfg.TextColor, alpha*0.9, cfg)
	}

	// Draw thumbnail
	r.drawImageGL(data.Thumbnail, x, y, finalW, finalH, alpha)

	// Draw border around thumbnail
	r.drawRectangleGL(x, y, finalW, finalH, 1.0, 1.0, 1.0, alpha*0.8, 2)

	// Draw selection indicator
	if math.Abs(offset) < 0.01 {
		r.drawSelectionIndicatorGL(x, y, finalW, finalH, cfg)
	}

	// Draw hover indicator
	if index == hoverIndex && hoverIndex != selected {
		r.drawHoverIndicatorGL(x, y, finalW, finalH, cfg)
	}
}

// drawImageGL draws an image at the specified position using OpenGL
func (r *OpenGLRenderer) drawImageGL(img image.Image, x, y, width, height, alpha float64) {
	if img == nil {
		return
	}

	// Convert image to RGBA
	rgba := imageToRGBA(img)

	// Upload texture
	var texture uint32
	gl.GenTextures(1, &texture)
	gl.BindTexture(gl.TEXTURE_2D, texture)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, int32(rgba.Bounds().Dx()), int32(rgba.Bounds().Dy()), 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(rgba.Pix))

	// Convert pixel coordinates to normalized device coordinates (NDC: -1 to 1)
	// Center the quad at (x, y) with size (width, height)
	ndcX := (x/float64(r.width))*2.0 - 1.0
	ndcY := 1.0 - (y/float64(r.height))*2.0 // Flip Y axis
	ndcWidth := (width / float64(r.width)) * 2.0
	ndcHeight := (height / float64(r.height)) * 2.0

	// Calculate quad corners (centered at ndcX, ndcY)
	left := ndcX - ndcWidth/2.0
	right := ndcX + ndcWidth/2.0
	top := ndcY + ndcHeight/2.0
	bottom := ndcY - ndcHeight/2.0

	// Create vertices for this specific quad
	vertices := []float32{
		// positions                    // texture coords
		float32(left), float32(top), 0.0, 0.0, 0.0, // top left
		float32(right), float32(top), 0.0, 1.0, 0.0, // top right
		float32(right), float32(bottom), 0.0, 1.0, 1.0, // bottom right
		float32(left), float32(bottom), 0.0, 0.0, 1.0, // bottom left
	}

	indices := []uint32{
		0, 1, 2, // first triangle
		0, 2, 3, // second triangle
	}

	// Create temporary VAO, VBO, EBO for this quad
	var vao, vbo, ebo uint32
	gl.GenVertexArrays(1, &vao)
	gl.GenBuffers(1, &vbo)
	gl.GenBuffers(1, &ebo)

	gl.BindVertexArray(vao)

	gl.BindBuffer(gl.ARRAY_BUFFER, vbo)
	gl.BufferData(gl.ARRAY_BUFFER, len(vertices)*4, gl.Ptr(vertices), gl.STATIC_DRAW)

	gl.BindBuffer(gl.ELEMENT_ARRAY_BUFFER, ebo)
	gl.BufferData(gl.ELEMENT_ARRAY_BUFFER, len(indices)*4, gl.Ptr(indices), gl.STATIC_DRAW)

	// Position attribute
	gl.VertexAttribPointer(0, 3, gl.FLOAT, false, 5*4, gl.PtrOffset(0))
	gl.EnableVertexAttribArray(0)

	// Texture coord attribute
	gl.VertexAttribPointer(1, 2, gl.FLOAT, false, 5*4, gl.PtrOffset(3*4))
	gl.EnableVertexAttribArray(1)

	// Use simplified shader (no matrices)
	gl.UseProgram(r.program)

	// Set color modulation for alpha
	colorLoc := gl.GetUniformLocation(r.program, gl.Str("colorMod\x00"))
	gl.Uniform4f(colorLoc, 1.0, 1.0, 1.0, float32(alpha))

	// Draw
	gl.BindTexture(gl.TEXTURE_2D, texture)
	gl.DrawElements(gl.TRIANGLES, 6, gl.UNSIGNED_INT, nil)

	// Cleanup
	gl.DeleteVertexArrays(1, &vao)
	gl.DeleteBuffers(1, &vbo)
	gl.DeleteBuffers(1, &ebo)
	gl.DeleteTextures(1, &texture)
}

// drawTextGL draws text at the specified position using OpenGL
func (r *OpenGLRenderer) drawTextGL(text string, x, y, fontSize float64, colorStr string, alpha float64, cfg Config) {
	// For simplicity, we'll render text to an image and then draw that image
	// This is not the most efficient approach but matches the CPU renderer behavior

	// Load font
	fallbackFace := NewMultiFallbackFace(cfg.FontPaths, fontSize)
	if fallbackFace == nil {
		return
	}
	defer fallbackFace.Close()

	// Measure text
	textWidth, textHeight := measureText(text, fallbackFace)

	// Create image for text rendering
	textImg := image.NewRGBA(image.Rect(0, 0, int(textWidth+10), int(textHeight+10)))

	// Draw text to image
	drawTextToImage(textImg, text, 5, int(fontSize), fallbackFace, colorStr, alpha)

	// Draw the text image
	r.drawImageGL(textImg, x, y, float64(textImg.Bounds().Dx()), float64(textImg.Bounds().Dy()), 1.0)
}

// drawShadowGL draws a shadow using OpenGL
func (r *OpenGLRenderer) drawShadowGL(x, y, w, h, scale float64, cfg Config) {
	shadowR, shadowG, shadowB, shadowA := parseColor(cfg.ShadowColor)
	shadowAlpha := shadowA * scale

	// Create a simple rectangle for shadow
	shadowImg := image.NewRGBA(image.Rect(0, 0, int(w), int(h)))
	shadowColor := color.RGBA{
		R: uint8(shadowR * 255),
		G: uint8(shadowG * 255),
		B: uint8(shadowB * 255),
		A: uint8(shadowAlpha * 255),
	}
	draw.Draw(shadowImg, shadowImg.Bounds(), &image.Uniform{shadowColor}, image.Point{}, draw.Src)

	r.drawImageGL(shadowImg, x+cfg.ShadowOffset, y+cfg.ShadowOffset, w, h, shadowAlpha)
}

// drawSelectionIndicatorGL draws selection indicator using OpenGL
func (r *OpenGLRenderer) drawSelectionIndicatorGL(x, y, w, h float64, cfg Config) {
	// Draw outer glow
	frameR, frameG, frameB, frameA := parseColor(cfg.SelectionFrame)
	r.drawRectangleGL(x, y, w+20, h+20, frameR, frameG, frameB, frameA*0.5, 6)

	// Draw inner highlight
	r.drawRectangleGL(x, y, w+10, h+10, frameR, frameG, frameB, frameA*0.8, 3)
}

// drawHoverIndicatorGL draws hover indicator using OpenGL
func (r *OpenGLRenderer) drawHoverIndicatorGL(x, y, w, h float64, cfg Config) {
	// Orange glow for hover
	r.drawRectangleGL(x, y, w+16, h+16, 1.0, 0.7, 0.2, 0.4, 4)
	r.drawRectangleGL(x, y, w+8, h+8, 1.0, 0.85, 0.4, 0.7, 2)
}

// drawRectangleGL draws a rectangle outline using OpenGL
func (r *OpenGLRenderer) drawRectangleGL(x, y, w, h, red, green, blue, alpha float64, lineWidth float32) {
	// Draw rounded rectangle outline by creating a border
	radius := 8.0
	if w > h {
		radius = math.Min(8.0, h/4)
	} else {
		radius = math.Min(8.0, w/4)
	}

	lw := float64(lineWidth) // Convert to float64 for calculations

	// Create outer rounded rect
	outerImg := createRoundedRect(int(w), int(h), radius, red, green, blue, alpha)

	// Create inner rounded rect (transparent) to cut out the middle
	if lw < w/2 && lw < h/2 {
		innerW := int(w - lw*2)
		innerH := int(h - lw*2)
		if innerW > 0 && innerH > 0 {
			// Clear inner area
			for py := int(lw); py < int(h)-int(lw); py++ {
				for px := int(lw); px < int(w)-int(lw); px++ {
					// Calculate distance from inner corner for rounded inner edge
					innerRadius := math.Max(0, radius-lw)
					dx := 0.0
					dy := 0.0

					// Check if we're in a corner region
					if float64(px) < lw+innerRadius && float64(py) < lw+innerRadius {
						// Top-left inner corner
						dx = (lw + innerRadius) - float64(px)
						dy = (lw + innerRadius) - float64(py)
					} else if float64(px) > w-lw-innerRadius && float64(py) < lw+innerRadius {
						// Top-right inner corner
						dx = float64(px) - (w - lw - innerRadius)
						dy = (lw + innerRadius) - float64(py)
					} else if float64(px) < lw+innerRadius && float64(py) > h-lw-innerRadius {
						// Bottom-left inner corner
						dx = (lw + innerRadius) - float64(px)
						dy = float64(py) - (h - lw - innerRadius)
					} else if float64(px) > w-lw-innerRadius && float64(py) > h-lw-innerRadius {
						// Bottom-right inner corner
						dx = float64(px) - (w - lw - innerRadius)
						dy = float64(py) - (h - lw - innerRadius)
					}

					// Clear pixel if it's inside the inner rounded area (with antialiasing)
					if dx != 0 || dy != 0 {
						dist := math.Sqrt(dx*dx + dy*dy)
						if dist <= innerRadius {
							outerImg.Set(px, py, color.Transparent)
						} else if dist < innerRadius+1.0 {
							// Antialiasing: gradually fade out at the inner edge
							fadeAlpha := (dist - innerRadius)
							currentColor := outerImg.RGBAAt(px, py)
							blendedColor := color.RGBA{
								R: currentColor.R,
								G: currentColor.G,
								B: currentColor.B,
								A: uint8(float64(currentColor.A) * fadeAlpha),
							}
							outerImg.Set(px, py, blendedColor)
						}
					} else {
						// Not in corner, clear it
						outerImg.Set(px, py, color.Transparent)
					}
				}
			}
		}
	}

	r.drawImageGL(outerImg, x, y, w, h, alpha)
}

// DrawGridLayoutGL renders windows in a grid layout using OpenGL
func (r *OpenGLRenderer) DrawGridLayoutGL(windowData []WindowData, selected int, hoverIndex int, cfg Config) *image.RGBA {
	if !r.initialized {
		log.Error().Msg("OpenGL renderer not initialized")
		return image.NewRGBA(image.Rect(0, 0, r.width, r.height))
	}

	// Serialize OpenGL calls with mutex
	r.mu.Lock()
	defer r.mu.Unlock()

	// Make context current before any OpenGL calls
	if r.window != nil {
		r.window.MakeContextCurrent()
	}

	// Bind framebuffer
	gl.BindFramebuffer(gl.FRAMEBUFFER, r.fbo)
	gl.Viewport(0, 0, int32(r.width), int32(r.height))

	// Clear background
	if cfg.WindowBackgroundEnabled {
		bgR, bgG, bgB, bgA := parseColor(cfg.BackgroundColor)
		gl.ClearColor(float32(bgR), float32(bgG), float32(bgB), float32(bgA*cfg.WindowBackgroundOpacity))
	} else {
		gl.ClearColor(0, 0, 0, 0)
	}
	gl.Clear(gl.COLOR_BUFFER_BIT)

	if len(windowData) == 0 {
		result := image.NewRGBA(image.Rect(0, 0, r.width, r.height))
		gl.ReadPixels(0, 0, int32(r.width), int32(r.height), gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(result.Pix))
		gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
		r.flipImageVertically(result)
		return result
	}

	// Enable blending
	gl.Enable(gl.BLEND)
	gl.BlendFunc(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA)

	// Calculate grid dimensions
	cols := cfg.GridColumns
	if cols <= 0 {
		cols = int(math.Ceil(math.Sqrt(float64(len(windowData)) * 1.5)))
		if cols < 2 {
			cols = 2
		}
		if cols > 6 {
			cols = 6
		}
	}

	rows := (len(windowData) + cols - 1) / cols

	spacing := cfg.GridSpacing
	if spacing == 0 {
		spacing = 20
	}

	// Calculate tile size
	availableWidth := float64(r.width) - spacing*(float64(cols)+1)
	availableHeight := float64(r.height) - spacing*(float64(rows)+1)

	tileW := availableWidth / float64(cols)
	tileH := availableHeight / float64(rows)

	maxTileW := float64(cfg.ThumbWidth) + 40
	maxTileH := float64(cfg.ThumbHeight) + 60
	if tileW > maxTileW {
		tileW = maxTileW
	}
	if tileH > maxTileH {
		tileH = maxTileH
	}

	// Center the grid
	totalGridW := float64(cols)*tileW + (float64(cols)+1)*spacing
	totalGridH := float64(rows)*tileH + (float64(rows)+1)*spacing
	offsetX := (float64(r.width) - totalGridW) / 2
	offsetY := (float64(r.height) - totalGridH) / 2

	// Draw each tile
	for i, win := range windowData {
		row := i / cols
		col := i % cols

		x := offsetX + spacing + float64(col)*(tileW+spacing)
		y := offsetY + spacing + float64(row)*(tileH+spacing)

		r.drawGridTileGL(&win, x, y, tileW, tileH, i == selected, i == hoverIndex, cfg)
	}

	// Resolve multisampled framebuffer to regular texture
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, r.fbo)
	gl.BindFramebuffer(gl.DRAW_FRAMEBUFFER, r.resolveFBO)
	gl.BlitFramebuffer(
		0, 0, int32(r.width), int32(r.height),
		0, 0, int32(r.width), int32(r.height),
		gl.COLOR_BUFFER_BIT, gl.NEAREST,
	)

	// Read pixels from resolved framebuffer
	gl.BindFramebuffer(gl.READ_FRAMEBUFFER, r.resolveFBO)
	result := image.NewRGBA(image.Rect(0, 0, r.width, r.height))
	gl.ReadPixels(0, 0, int32(r.width), int32(r.height), gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(result.Pix))

	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)

	// Flip image vertically
	r.flipImageVertically(result)

	return result
}

// drawGridTileGL draws a single grid tile using OpenGL
func (r *OpenGLRenderer) drawGridTileGL(win *WindowData, x, y, w, h float64, isSelected bool, isHovered bool, cfg Config) {
	if win == nil {
		return
	}

	// Draw shadow
	if isSelected || isHovered {
		shadowOffset := cfg.ShadowOffset
		if !isSelected {
			shadowOffset = shadowOffset * 0.5
		}
		r.drawShadowGL(x+w/2, y+h/2, w, h, 1.0, cfg)
	}

	// Draw tile background
	bgR, bgG, bgB, bgA := parseColor(cfg.BackgroundColor)
	tileImg := createRoundedRect(int(w), int(h), 8, bgR, bgG, bgB, bgA*0.3)
	r.drawImageGL(tileImg, x+w/2, y+h/2, w, h, 1.0)

	// Draw thumbnail
	thumbPadding := 10.0
	thumbW := w - 2*thumbPadding
	thumbH := h - 60

	if win.Thumbnail != nil {
		bounds := win.Thumbnail.Bounds()
		imgW := float64(bounds.Dx())
		imgH := float64(bounds.Dy())

		scale := math.Min(thumbW/imgW, thumbH/imgH)
		scaledW := imgW * scale
		scaledH := imgH * scale

		thumbX := x + thumbPadding + (thumbW-scaledW)/2 + scaledW/2
		thumbY := y + thumbPadding + (thumbH-scaledH)/2 + scaledH/2

		r.drawImageGL(win.Thumbnail, thumbX, thumbY, scaledW, scaledH, 1.0)

		// Draw border around thumbnail
		frameR, frameG, frameB, frameA := parseColor(cfg.InactiveFrame)
		r.drawRectangleGL(thumbX, thumbY, scaledW, scaledH, frameR, frameG, frameB, frameA*0.5, 1)
	}

	// Draw icon
	if win.Icon != nil {
		iconSize := 24.0
		iconPadding := 8.0
		r.drawImageGL(win.Icon, x+iconPadding+iconSize/2, y+iconPadding+iconSize/2, iconSize, iconSize, 1.0)
	}

	// Draw title
	if win.Title != "" {
		titleY := y + h - 40
		titleMaxWidth := w - 20
		fontSize := float64(cfg.FontSize)

		// Load font to measure text width
		fallbackFace := NewMultiFallbackFace(cfg.FontPaths, fontSize)
		if fallbackFace != nil {
			defer fallbackFace.Close()
			// Truncate title to fit within tile width
			title := truncateTextGL(win.Title, titleMaxWidth, fallbackFace)
			r.drawTextGL(title, x+w/2, titleY, fontSize, cfg.TextColor, 1.0, cfg)
		}
	}

	// Draw workspace
	if win.Workspace != "" {
		workspaceY := y + h - 40 + 26
		fontSize := float64(cfg.FontSize - 2)
		// Workspace labels are usually short, no need for truncation
		r.drawTextGL(win.Workspace, x+w/2, workspaceY, fontSize, cfg.TextColor, 0.6, cfg)
	}

	// Draw frame
	if isSelected {
		frameR, frameG, frameB, frameA := parseColor(cfg.SelectionFrame)
		r.drawRectangleGL(x+w/2, y+h/2, w+4, h+4, frameR, frameG, frameB, frameA*0.9, 4)
		r.drawRectangleGL(x+w/2, y+h/2, w, h, frameR, frameG, frameB, frameA*0.4, 2)
	} else if isHovered {
		r.drawRectangleGL(x+w/2, y+h/2, w+2, h+2, 1.0, 0.7, 0.2, 0.6, 3)
	} else {
		frameR, frameG, frameB, frameA := parseColor(cfg.InactiveFrame)
		r.drawRectangleGL(x+w/2, y+h/2, w, h, frameR, frameG, frameB, frameA*0.3, 1)
	}
}

// Helper functions

// flipImageVertically flips an image vertically in-place
func (r *OpenGLRenderer) flipImageVertically(img *image.RGBA) {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	for y := 0; y < height/2; y++ {
		for x := 0; x < width; x++ {
			y1 := y*img.Stride + x*4
			y2 := (height-1-y)*img.Stride + x*4

			// Swap pixels
			img.Pix[y1], img.Pix[y2] = img.Pix[y2], img.Pix[y1]
			img.Pix[y1+1], img.Pix[y2+1] = img.Pix[y2+1], img.Pix[y1+1]
			img.Pix[y1+2], img.Pix[y2+2] = img.Pix[y2+2], img.Pix[y1+2]
			img.Pix[y1+3], img.Pix[y2+3] = img.Pix[y2+3], img.Pix[y1+3]
		}
	}
}

// imageToRGBA converts any image to RGBA
func imageToRGBA(img image.Image) *image.RGBA {
	if rgba, ok := img.(*image.RGBA); ok {
		return rgba
	}

	bounds := img.Bounds()
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)
	return rgba
}

// compileShaderProgram compiles and links shaders
func compileShaderProgram(vertexSource, fragmentSource string) (uint32, error) {
	// Compile vertex shader
	vertexShader, err := compileGLShader(vertexSource, gl.VERTEX_SHADER)
	if err != nil {
		return 0, fmt.Errorf("vertex shader: %w", err)
	}

	// Compile fragment shader
	fragmentShader, err := compileGLShader(fragmentSource, gl.FRAGMENT_SHADER)
	if err != nil {
		gl.DeleteShader(vertexShader)
		return 0, fmt.Errorf("fragment shader: %w", err)
	}

	// Link program
	program := gl.CreateProgram()
	gl.AttachShader(program, vertexShader)
	gl.AttachShader(program, fragmentShader)
	gl.LinkProgram(program)

	// Check for linking errors
	var status int32
	gl.GetProgramiv(program, gl.LINK_STATUS, &status)
	if status == gl.FALSE {
		var logLength int32
		gl.GetProgramiv(program, gl.INFO_LOG_LENGTH, &logLength)
		logMsg := make([]byte, logLength)
		gl.GetProgramInfoLog(program, logLength, nil, &logMsg[0])

		// Cleanup on error
		gl.DeleteShader(vertexShader)
		gl.DeleteShader(fragmentShader)
		gl.DeleteProgram(program)
		return 0, fmt.Errorf("failed to link program: %s", string(logMsg))
	}

	// Shaders can be deleted after successful linking
	// They remain in use by the program until the program is deleted
	gl.DetachShader(program, vertexShader)
	gl.DetachShader(program, fragmentShader)
	gl.DeleteShader(vertexShader)
	gl.DeleteShader(fragmentShader)

	return program, nil
}

// compileGLShader compiles a single shader
func compileGLShader(source string, shaderType uint32) (uint32, error) {
	shader := gl.CreateShader(shaderType)
	csource, free := gl.Strs(source)
	defer free()
	gl.ShaderSource(shader, 1, csource, nil)
	gl.CompileShader(shader)

	// Check for compilation errors
	var status int32
	gl.GetShaderiv(shader, gl.COMPILE_STATUS, &status)
	if status == gl.FALSE {
		var logLength int32
		gl.GetShaderiv(shader, gl.INFO_LOG_LENGTH, &logLength)
		logMsg := make([]byte, logLength)
		gl.GetShaderInfoLog(shader, logLength, nil, &logMsg[0])
		return 0, fmt.Errorf("failed to compile shader: %s", string(logMsg))
	}

	return shader, nil
}

// Matrix math functions

// orthoMatrix creates an orthographic projection matrix
func orthoMatrix(left, right, bottom, top, near, far float32) [16]float32 {
	return [16]float32{
		2 / (right - left), 0, 0, 0,
		0, 2 / (top - bottom), 0, 0,
		0, 0, -2 / (far - near), 0,
		-(right + left) / (right - left), -(top + bottom) / (top - bottom), -(far + near) / (far - near), 1,
	}
}

// translationMatrix creates a translation matrix
func translationMatrix(x, y, z float32) [16]float32 {
	return [16]float32{
		1, 0, 0, 0,
		0, 1, 0, 0,
		0, 0, 1, 0,
		x, y, z, 1,
	}
}

// scaleMatrix creates a scale matrix
func scaleMatrix(x, y, z float32) [16]float32 {
	return [16]float32{
		x, 0, 0, 0,
		0, y, 0, 0,
		0, 0, z, 0,
		0, 0, 0, 1,
	}
}

// multiplyMatrix multiplies two 4x4 matrices
func multiplyMatrix(a, b [16]float32) [16]float32 {
	var result [16]float32
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			for k := 0; k < 4; k++ {
				result[i*4+j] += a[i*4+k] * b[k*4+j]
			}
		}
	}
	return result
}

// measureText measures text dimensions
func measureText(text string, face font.Face) (float64, float64) {
	var width fixed.Int26_6

	metrics := face.Metrics()
	maxHeight := metrics.Height

	for _, r := range text {
		_, advance, ok := face.GlyphBounds(r)
		if !ok {
			continue
		}
		width += advance
	}

	return float64(width) / 64.0, float64(maxHeight) / 64.0
}

// truncateTextGL truncates text to fit within maxWidth pixels
func truncateTextGL(text string, maxWidth float64, face font.Face) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return text
	}

	// Measure full text
	w, _ := measureText(text, face)
	if w <= maxWidth {
		return text
	}

	// Binary search for optimal length
	for length := len(runes) - 1; length > 0; length-- {
		truncated := string(runes[:length]) + "..."
		w, _ := measureText(truncated, face)
		if w <= maxWidth {
			return truncated
		}
	}

	return "..."
}

// drawTextToImage draws text onto an image
func drawTextToImage(img *image.RGBA, text string, x, y int, face font.Face, colorStr string, alpha float64) {
	r, g, b, a := parseColor(colorStr)
	textColor := color.RGBA{
		R: uint8(r * 255),
		G: uint8(g * 255),
		B: uint8(b * 255),
		A: uint8(a * alpha * 255),
	}

	drawer := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(textColor),
		Face: face,
		Dot:  fixed.P(x, y),
	}

	drawer.DrawString(text)
}

// createRoundedRect creates a rounded rectangle image
func createRoundedRect(width, height int, radius float64, r, g, b, a float64) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	rectColor := color.RGBA{
		R: uint8(r * 255),
		G: uint8(g * 255),
		B: uint8(b * 255),
		A: uint8(a * 255),
	}

	// Draw filled rounded rectangle
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			// Determine if pixel is inside rounded rectangle
			dx := 0.0
			dy := 0.0

			// Check corners
			if float64(x) < radius && float64(y) < radius {
				// Top-left corner
				dx = radius - float64(x)
				dy = radius - float64(y)
			} else if float64(x) > float64(width)-radius && float64(y) < radius {
				// Top-right corner
				dx = float64(x) - (float64(width) - radius)
				dy = radius - float64(y)
			} else if float64(x) < radius && float64(y) > float64(height)-radius {
				// Bottom-left corner
				dx = radius - float64(x)
				dy = float64(y) - (float64(height) - radius)
			} else if float64(x) > float64(width)-radius && float64(y) > float64(height)-radius {
				// Bottom-right corner
				dx = float64(x) - (float64(width) - radius)
				dy = float64(y) - (float64(height) - radius)
			}

			// If inside corner area, check distance from corner center
			if dx != 0 || dy != 0 {
				dist := math.Sqrt(dx*dx + dy*dy)
				if dist <= radius {
					// Apply antialiasing on edge (1 pixel smooth transition)
					edgeDist := radius - dist
					pixelAlpha := a
					if edgeDist < 1.0 {
						// Smooth falloff at edge
						pixelAlpha = a * edgeDist
					}
					antialiasedColor := color.RGBA{
						R: rectColor.R,
						G: rectColor.G,
						B: rectColor.B,
						A: uint8(pixelAlpha * 255),
					}
					img.Set(x, y, antialiasedColor)
				}
			} else {
				// Not in corner area, always draw
				img.Set(x, y, rectColor)
			}
		}
	}

	return img
}

// Public API functions that wrap the OpenGL renderer

// Draw3DCarouselWithDataOpenGL is the public API for OpenGL-based carousel rendering
func Draw3DCarouselWithDataOpenGL(windowData []WindowData, selected int, hoverIndex int, animOffset float64, cfg Config) *image.RGBA {
	renderer, err := NewOpenGLRenderer(cfg.Width, cfg.Height)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create OpenGL renderer, falling back to CPU")
		return Draw3DCarouselWithData(windowData, selected, hoverIndex, animOffset, cfg)
	}
	defer renderer.Cleanup()

	return renderer.Draw3DCarouselWithDataGL(windowData, selected, hoverIndex, animOffset, cfg)
}

// DrawGridLayoutOpenGL is the public API for OpenGL-based grid layout rendering
func DrawGridLayoutOpenGL(windowData []WindowData, selected int, hoverIndex int, cfg Config) *image.RGBA {
	renderer, err := NewOpenGLRenderer(cfg.Width, cfg.Height)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create OpenGL renderer, falling back to CPU")
		return DrawGridLayout(windowData, selected, hoverIndex, cfg)
	}
	defer renderer.Cleanup()

	return renderer.DrawGridLayoutGL(windowData, selected, hoverIndex, cfg)
}
