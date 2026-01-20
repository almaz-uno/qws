package main

import (
	"fmt"
	"image"
	"image/draw"
	"log"
	"runtime"

	"github.com/go-gl/gl/v4.6-core/gl"
	"github.com/go-gl/glfw/v3.3/glfw"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	xdraw "golang.org/x/image/draw"
)

const (
	targetSize = 512
	windowSize = 800
)

// Vertex shader for rendering textured quad
const vertexShaderSource = `
#version 460 core
layout (location = 0) in vec3 aPos;
layout (location = 1) in vec2 aTexCoord;

out vec2 TexCoord;

void main() {
    gl_Position = vec4(aPos, 1.0);
    TexCoord = aTexCoord;
}
` + "\x00"

// Fragment shader for rendering textured quad
const fragmentShaderSource = `
#version 460 core
out vec4 FragColor;
in vec2 TexCoord;

uniform sampler2D texture1;

void main() {
    FragColor = texture(texture1, TexCoord);
}
` + "\x00"

func main() {
	// Capture screenshot
	screenshot, err := captureScreenshot()
	if err != nil {
		log.Fatalf("Failed to capture screenshot: %v", err)
	}

	// Scale screenshot to 512x512 preserving aspect ratio
	scaled := scaleImage(screenshot, targetSize, targetSize)

	// Display in OpenGL window
	if err := displayImage(scaled); err != nil {
		log.Fatalf("Failed to display image: %v", err)
	}
}

// captureScreenshot captures the root window screenshot using X11
func captureScreenshot() (image.Image, error) {
	// Connect to X server
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to X server: %w", err)
	}
	defer conn.Close()

	// Get root window
	setup := xproto.Setup(conn)
	if setup == nil || len(setup.Roots) == 0 {
		return nil, fmt.Errorf("failed to get root window information")
	}

	screen := setup.Roots[0]
	root := screen.Root
	width := int(screen.WidthInPixels)
	height := int(screen.HeightInPixels)

	// Capture root window with XGetImage
	img, err := xproto.GetImage(conn,
		xproto.ImageFormatZPixmap,
		xproto.Drawable(root),
		0, 0,
		uint16(width), uint16(height),
		^uint32(0),
	).Reply()
	if err != nil {
		return nil, fmt.Errorf("failed to capture screenshot: %w", err)
	}

	// Convert to image.RGBA
	rgba := image.NewRGBA(image.Rect(0, 0, width, height))

	// Parse pixel data - assuming 32 bits per pixel (BGRA format typical for X11)
	dataSize := len(img.Data)
	bytesPerPixel := 4
	expectedSize := width * height * bytesPerPixel

	if dataSize < expectedSize {
		return nil, fmt.Errorf("unexpected data size: got %d, expected at least %d", dataSize, expectedSize)
	}

	// Convert BGRA to RGBA
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			srcOffset := (y*width + x) * bytesPerPixel
			dstOffset := y*rgba.Stride + x*4

			// X11 typically uses BGRA byte order
			rgba.Pix[dstOffset+0] = img.Data[srcOffset+2] // R
			rgba.Pix[dstOffset+1] = img.Data[srcOffset+1] // G
			rgba.Pix[dstOffset+2] = img.Data[srcOffset+0] // B
			rgba.Pix[dstOffset+3] = 255                   // A
		}
	}

	return rgba, nil
}

// scaleImage scales image to fit within maxWidth x maxHeight preserving aspect ratio
func scaleImage(src image.Image, maxWidth, maxHeight int) image.Image {
	bounds := src.Bounds()
	srcWidth := bounds.Dx()
	srcHeight := bounds.Dy()

	// Calculate scaling factor to fit within target size
	scaleX := float64(maxWidth) / float64(srcWidth)
	scaleY := float64(maxHeight) / float64(srcHeight)
	scale := scaleX
	if scaleY < scale {
		scale = scaleY
	}

	// Calculate new dimensions
	newWidth := int(float64(srcWidth) * scale)
	newHeight := int(float64(srcHeight) * scale)

	// Create destination image
	dst := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))

	// Use high-quality scaling
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, xdraw.Over, nil)

	return dst
}

// displayImage displays the image in an OpenGL window
func displayImage(img image.Image) error {
	runtime.LockOSThread()

	// Initialize GLFW
	if err := glfw.Init(); err != nil {
		return fmt.Errorf("failed to initialize GLFW: %w", err)
	}
	defer glfw.Terminate()

	// Configure GLFW
	glfw.WindowHint(glfw.ContextVersionMajor, 4)
	glfw.WindowHint(glfw.ContextVersionMinor, 6)
	glfw.WindowHint(glfw.OpenGLProfile, glfw.OpenGLCoreProfile)
	glfw.WindowHint(glfw.OpenGLForwardCompatible, glfw.True)
	glfw.WindowHint(glfw.Resizable, glfw.False)

	// Create window
	window, err := glfw.CreateWindow(windowSize, windowSize, "GLX Screenshot Test", nil, nil)
	if err != nil {
		return fmt.Errorf("failed to create window: %w", err)
	}
	defer window.Destroy()

	window.MakeContextCurrent()

	// Initialize OpenGL
	if err := gl.Init(); err != nil {
		return fmt.Errorf("failed to initialize OpenGL: %w", err)
	}

	// Setup OpenGL
	gl.Viewport(0, 0, windowSize, windowSize)
	gl.ClearColor(0.1, 0.1, 0.1, 1.0)

	// Compile shaders
	program, err := compileShaderProgram(vertexShaderSource, fragmentShaderSource)
	if err != nil {
		return fmt.Errorf("failed to compile shaders: %w", err)
	}
	defer gl.DeleteProgram(program)

	// Upload texture
	texture := uploadTexture(img)
	defer gl.DeleteTextures(1, &texture)

	// Setup geometry for centered quad
	imgBounds := img.Bounds()
	imgWidth := float32(imgBounds.Dx())
	imgHeight := float32(imgBounds.Dy())

	// Calculate normalized coordinates to center the image
	aspectX := imgWidth / float32(windowSize)
	aspectY := imgHeight / float32(windowSize)

	vertices := []float32{
		// positions        // texture coords
		-aspectX, aspectY, 0.0, 0.0, 0.0, // top left
		aspectX, aspectY, 0.0, 1.0, 0.0, // top right
		aspectX, -aspectY, 0.0, 1.0, 1.0, // bottom right
		-aspectX, -aspectY, 0.0, 0.0, 1.0, // bottom left
	}

	indices := []uint32{
		0, 1, 2, // first triangle
		0, 2, 3, // second triangle
	}

	// Create VAO, VBO, EBO
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

	gl.BindVertexArray(0)

	// Setup key callback for ESC
	window.SetKeyCallback(func(w *glfw.Window, key glfw.Key, scancode int, action glfw.Action, mods glfw.ModifierKey) {
		if key == glfw.KeyEscape && action == glfw.Press {
			w.SetShouldClose(true)
		}
	})

	// Render loop
	for !window.ShouldClose() {
		// Clear
		gl.Clear(gl.COLOR_BUFFER_BIT)

		// Draw
		gl.UseProgram(program)
		gl.BindTexture(gl.TEXTURE_2D, texture)
		gl.BindVertexArray(vao)
		gl.DrawElements(gl.TRIANGLES, 6, gl.UNSIGNED_INT, nil)

		// Swap and poll
		window.SwapBuffers()
		glfw.PollEvents()
	}

	// Cleanup
	gl.DeleteVertexArrays(1, &vao)
	gl.DeleteBuffers(1, &vbo)
	gl.DeleteBuffers(1, &ebo)

	return nil
}

// compileShaderProgram compiles and links vertex and fragment shaders
func compileShaderProgram(vertexSource, fragmentSource string) (uint32, error) {
	// Compile vertex shader
	vertexShader, err := compileShader(vertexSource, gl.VERTEX_SHADER)
	if err != nil {
		return 0, fmt.Errorf("vertex shader: %w", err)
	}
	defer gl.DeleteShader(vertexShader)

	// Compile fragment shader
	fragmentShader, err := compileShader(fragmentSource, gl.FRAGMENT_SHADER)
	if err != nil {
		return 0, fmt.Errorf("fragment shader: %w", err)
	}
	defer gl.DeleteShader(fragmentShader)

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
		return 0, fmt.Errorf("failed to link program: %s", string(logMsg))
	}

	return program, nil
}

// compileShader compiles a shader
func compileShader(source string, shaderType uint32) (uint32, error) {
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

// uploadTexture uploads an image to OpenGL texture
func uploadTexture(img image.Image) uint32 {
	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), img, image.Point{}, draw.Src)

	var texture uint32
	gl.GenTextures(1, &texture)
	gl.BindTexture(gl.TEXTURE_2D, texture)

	// Set texture parameters
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE)

	// Upload texture data
	width := int32(rgba.Bounds().Dx())
	height := int32(rgba.Bounds().Dy())
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA, width, height, 0, gl.RGBA, gl.UNSIGNED_BYTE, gl.Ptr(rgba.Pix))

	return texture
}
