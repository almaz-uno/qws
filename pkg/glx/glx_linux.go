// Package glx is a minimal cgo wrapper around GLX: it creates an OpenGL context
// that can be made current on, and presented to, an X window created through
// github.com/jezek/xgb.
//
// go-gl/gl resolves every GL entry point through libGL, so the context must be
// a real direct GLX context, and GLX needs an Xlib Display. X resource IDs are
// server-global, so a window created on the xgb connection is a valid GLX
// drawable on a separate Xlib Display opened to the same server. The package
// owns that GL-only Display and never reads events from it; window management,
// input and the event loop stay on xgb.
package glx

/*
#cgo pkg-config: gl x11
#include <X11/Xlib.h>
#include <GL/glx.h>
#include <stdlib.h>
#include <string.h>
#include <stdio.h>

// ARB context creation constants, in case <GL/glx.h> does not pull in
// <GL/glxext.h>
#ifndef GLX_CONTEXT_MAJOR_VERSION_ARB
#define GLX_CONTEXT_MAJOR_VERSION_ARB          0x2091
#define GLX_CONTEXT_MINOR_VERSION_ARB          0x2092
#define GLX_CONTEXT_FLAGS_ARB                  0x2094
#define GLX_CONTEXT_PROFILE_MASK_ARB           0x9126
#define GLX_CONTEXT_CORE_PROFILE_BIT_ARB       0x00000001
#define GLX_CONTEXT_FORWARD_COMPATIBLE_BIT_ARB 0x00000002
#endif

#ifndef GLX_SAMPLE_BUFFERS
#define GLX_SAMPLE_BUFFERS 0x186a0
#endif

typedef GLXContext (*glXCreateContextAttribsARBProc)(Display*, GLXFBConfig, GLXContext, Bool, const int*);
typedef void (*glXSwapIntervalEXTProc)(Display*, GLXDrawable, int);

typedef struct {
	Display      *dpy;
	GLXContext    ctx;
	unsigned long visualid;
	int           ok;
	char          err[256];
	int           shareFailed; // created without the share context, which it could not share
	char          shareErr[256];
} GLXSetup;

// Set by the X error handler installed around context creation: GLX reports
// that failure asynchronously, as an X error. Errors of other displays — the
// snapshots have one of their own — go to the handler it replaced.
static Display *gGLXDpy = 0;
static int gGLXErr = 0;
static int (*gGLXPrev)(Display*, XErrorEvent*) = 0;
static int glxErrorHandler(Display *dpy, XErrorEvent *ev) {
	if (dpy == gGLXDpy) {
		gGLXErr = ev->error_code ? ev->error_code : 1;
		return 0;
	}
	return gGLXPrev ? gGLXPrev(dpy, ev) : 0;
}

// glxChooseConfig returns a double-buffered FBConfig without multisampling
// whose visual is depth-32 ARGB. A frame is copied to the window pixel for
// pixel, so samples would only cost memory.
static GLXFBConfig glxChooseConfig(Display *dpy, int screen, int *found) {
	int attribs[] = {
		GLX_X_RENDERABLE,   True,
		GLX_DRAWABLE_TYPE,  GLX_WINDOW_BIT,
		GLX_RENDER_TYPE,    GLX_RGBA_BIT,
		GLX_X_VISUAL_TYPE,  GLX_TRUE_COLOR,
		GLX_RED_SIZE,       8,
		GLX_GREEN_SIZE,     8,
		GLX_BLUE_SIZE,      8,
		GLX_ALPHA_SIZE,     8,
		GLX_DEPTH_SIZE,     0,
		GLX_STENCIL_SIZE,   0,
		GLX_DOUBLEBUFFER,   True,
		GLX_SAMPLE_BUFFERS, 0,
		None
	};

	*found = 0;
	int n = 0;
	GLXFBConfig *cfgs = glXChooseFBConfig(dpy, screen, attribs, &n);
	if (!cfgs) {
		return 0;
	}
	GLXFBConfig chosen = 0;
	for (int i = 0; i < n && !chosen; i++) {
		int samples = 0;
		glXGetFBConfigAttrib(dpy, cfgs[i], GLX_SAMPLE_BUFFERS, &samples);
		if (samples != 0) {
			continue;
		}
		XVisualInfo *vi = glXGetVisualFromFBConfig(dpy, cfgs[i]);
		if (vi) {
			if (vi->depth == 32) {
				chosen = cfgs[i];
			}
			XFree(vi);
		}
	}
	XFree(cfgs);
	if (chosen) {
		*found = 1;
	}
	return chosen;
}

// glxSetup opens the display and creates the context, sharing the objects of
// share when it is given; where a context cannot be created sharing them, it
// is created without and shareFailed says why
static GLXSetup glxSetup(GLXContext share) {
	GLXSetup s;
	memset(&s, 0, sizeof(s));

	s.dpy = XOpenDisplay(NULL);
	if (!s.dpy) {
		snprintf(s.err, sizeof(s.err), "XOpenDisplay(NULL) failed (is DISPLAY set?)");
		return s;
	}

	int found = 0;
	GLXFBConfig fbc = glxChooseConfig(s.dpy, DefaultScreen(s.dpy), &found);
	if (!found) {
		snprintf(s.err, sizeof(s.err), "no double-buffered depth-32 ARGB GLX FBConfig without multisampling");
		return s;
	}

	XVisualInfo *vi = glXGetVisualFromFBConfig(s.dpy, fbc);
	if (!vi) {
		snprintf(s.err, sizeof(s.err), "glXGetVisualFromFBConfig returned NULL for the chosen config");
		return s;
	}
	s.visualid = (unsigned long)vi->visualid;
	XFree(vi);

	glXCreateContextAttribsARBProc createCtx =
		(glXCreateContextAttribsARBProc)glXGetProcAddressARB((const GLubyte*)"glXCreateContextAttribsARB");
	if (!createCtx) {
		snprintf(s.err, sizeof(s.err), "glXCreateContextAttribsARB unavailable (GLX_ARB_create_context missing)");
		return s;
	}

	int ctxAttribs[] = {
		GLX_CONTEXT_MAJOR_VERSION_ARB, 4,
		GLX_CONTEXT_MINOR_VERSION_ARB, 6,
		GLX_CONTEXT_PROFILE_MASK_ARB,  GLX_CONTEXT_CORE_PROFILE_BIT_ARB,
		GLX_CONTEXT_FLAGS_ARB,         GLX_CONTEXT_FORWARD_COMPATIBLE_BIT_ARB,
		None
	};

	gGLXDpy = s.dpy;
	gGLXErr = 0;
	gGLXPrev = XSetErrorHandler(glxErrorHandler);
	s.ctx = createCtx(s.dpy, fbc, share, True, ctxAttribs);
	XSync(s.dpy, False); // deliver an asynchronous X error now
	if (share && (s.ctx == 0 || gGLXErr)) {
		snprintf(s.shareErr, sizeof(s.shareErr),
			"glXCreateContextAttribsARB with a share context failed (X error %d)", gGLXErr);
		s.shareFailed = 1;
		if (s.ctx) glXDestroyContext(s.dpy, s.ctx);
		gGLXErr = 0;
		s.ctx = createCtx(s.dpy, fbc, 0, True, ctxAttribs);
		XSync(s.dpy, False);
	}
	XSetErrorHandler(gGLXPrev);
	gGLXDpy = 0;

	if (s.ctx == 0 || gGLXErr) {
		s.ctx = 0;
		snprintf(s.err, sizeof(s.err), "glXCreateContextAttribsARB failed (no direct 4.6 core context)");
		return s;
	}

	if (!glXIsDirect(s.dpy, s.ctx)) {
		glXDestroyContext(s.dpy, s.ctx);
		s.ctx = 0;
		snprintf(s.err, sizeof(s.err), "GLX context is indirect (no direct rendering)");
		return s;
	}

	s.ok = 1;
	return s;
}

static int glxMakeCurrent(Display *dpy, GLXContext ctx, unsigned long win) {
	return glXMakeCurrent(dpy, (GLXDrawable)win, ctx) ? 1 : 0;
}

static int glxSwapInterval(Display *dpy, unsigned long win, int interval) {
	const char *ext = glXQueryExtensionsString(dpy, DefaultScreen(dpy));
	if (!ext || !strstr(ext, "GLX_EXT_swap_control")) {
		return 0;
	}
	glXSwapIntervalEXTProc swapInterval =
		(glXSwapIntervalEXTProc)glXGetProcAddressARB((const GLubyte*)"glXSwapIntervalEXT");
	if (!swapInterval) {
		return 0;
	}
	swapInterval(dpy, (GLXDrawable)win, interval);
	return 1;
}

static void glxSwapBuffers(Display *dpy, unsigned long win) {
	glXSwapBuffers(dpy, (GLXDrawable)win);
}

static void glxTeardown(Display *dpy, GLXContext ctx) {
	if (dpy) {
		glXMakeCurrent(dpy, None, NULL);
		if (ctx) glXDestroyContext(dpy, ctx);
		XCloseDisplay(dpy);
	}
}
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
)

// Share names a GL context that other contexts can be created to share the
// objects of — textures, buffers, sync objects — on displays of their own
// (specs/020-live-thumbnails). It stays valid while that context lives.
type Share struct {
	ctx C.GLXContext
}

// Context owns a GL-only Xlib Display and a direct GLX 4.6 core context whose
// FBConfig has a depth-32 ARGB visual. It is not current on any window until
// MakeCurrent.
type Context struct {
	dpy      *C.Display
	ctx      C.GLXContext
	visualID uint32
	shareErr error
}

// errNoShare is the ShareError of a context created without a share context
var errNoShare = errors.New("glx: no context to share with")

// NewContext opens the GL display, chooses the FBConfig and creates the
// context. The window the context is made current on must be created with
// VisualID, or glXMakeCurrent fails with BadMatch.
//
// With share, the context shares the objects of share's context. Where it
// cannot be created so — the driver shares no objects between the two — it is
// created without, and ShareError says why: a failure to share is not a
// failure to create.
//
// The calling goroutine is locked to its OS thread until Destroy: the context
// and every GL call must stay on one thread. An error is expected on machines
// without direct GLX 4.6; callers fall back to presenting without GL.
func NewContext(share *Share) (*Context, error) {
	runtime.LockOSThread()

	var shareCtx C.GLXContext
	if share != nil {
		shareCtx = share.ctx
	}
	s := C.glxSetup(shareCtx)
	if s.ok == 0 {
		C.glxTeardown(s.dpy, s.ctx)
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("glx: %s", C.GoString(&s.err[0]))
	}

	c := &Context{
		dpy:      s.dpy,
		ctx:      s.ctx,
		visualID: uint32(s.visualid),
	}
	switch {
	case share == nil:
		c.shareErr = errNoShare
	case s.shareFailed != 0:
		c.shareErr = fmt.Errorf("glx: %s", C.GoString(&s.shareErr[0]))
	}
	return c, nil
}

// ShareError is nil when the context shares the objects of the context it
// was created to share with, and says why it does not otherwise
func (c *Context) ShareError() error { return c.shareErr }

// VisualID is the X visual of the chosen FBConfig
func (c *Context) VisualID() uint32 { return c.visualID }

// MakeCurrent binds the context to the window on the calling thread
func (c *Context) MakeCurrent(window uint32) error {
	if C.glxMakeCurrent(c.dpy, c.ctx, C.ulong(window)) == 0 {
		return fmt.Errorf("glx: glXMakeCurrent failed for window 0x%x (visual mismatch?)", window)
	}
	return nil
}

// SetSwapInterval sets how many vertical blanks a swap of the window waits for
func (c *Context) SetSwapInterval(window uint32, interval int) error {
	if C.glxSwapInterval(c.dpy, C.ulong(window), C.int(interval)) == 0 {
		return fmt.Errorf("glx: GLX_EXT_swap_control unavailable")
	}
	return nil
}

// SwapBuffers presents the back buffer of the window
func (c *Context) SwapBuffers(window uint32) {
	C.glxSwapBuffers(c.dpy, C.ulong(window))
}

// Destroy releases the context, closes the GL display and unlocks the thread
func (c *Context) Destroy() {
	if c == nil || c.dpy == nil {
		return
	}
	C.glxTeardown(c.dpy, c.ctx)
	c.dpy = nil
	c.ctx = nil
	runtime.UnlockOSThread()
}
