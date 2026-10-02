package glx

/*
#cgo pkg-config: gl x11
#include <X11/Xlib.h>
#include <GL/glx.h>
#include <string.h>
#include <stdio.h>

#ifndef GLX_BIND_TO_TEXTURE_RGB_EXT
#define GLX_BIND_TO_TEXTURE_RGB_EXT     0x20D0
#define GLX_BIND_TO_TEXTURE_RGBA_EXT    0x20D1
#define GLX_BIND_TO_TEXTURE_TARGETS_EXT 0x20D3
#define GLX_Y_INVERTED_EXT              0x20D4
#define GLX_TEXTURE_FORMAT_EXT          0x20D5
#define GLX_TEXTURE_TARGET_EXT          0x20D6
#define GLX_TEXTURE_FORMAT_RGB_EXT      0x20D9
#define GLX_TEXTURE_FORMAT_RGBA_EXT     0x20DA
#define GLX_TEXTURE_2D_BIT_EXT          0x00000002
#define GLX_TEXTURE_2D_EXT              0x20DC
#define GLX_FRONT_LEFT_EXT              0x20DE
#endif

#ifndef GLX_CONTEXT_MAJOR_VERSION_ARB
#define GLX_CONTEXT_MAJOR_VERSION_ARB          0x2091
#define GLX_CONTEXT_MINOR_VERSION_ARB          0x2092
#define GLX_CONTEXT_FLAGS_ARB                  0x2094
#define GLX_CONTEXT_PROFILE_MASK_ARB           0x9126
#define GLX_CONTEXT_CORE_PROFILE_BIT_ARB       0x00000001
#define GLX_CONTEXT_FORWARD_COMPATIBLE_BIT_ARB 0x00000002
#endif

typedef GLXContext (*offCreateCtxProc)(Display*, GLXFBConfig, GLXContext, Bool, const int*);
typedef void (*offBindProc)(Display*, GLXDrawable, int, const int*);
typedef void (*offReleaseProc)(Display*, GLXDrawable, int);

typedef struct {
	Display       *dpy;
	GLXContext     ctx;
	GLXPbuffer     pbuf;
	offBindProc    bind;
	offReleaseProc release;
	int            ok;
	char           err[256];
} OffscreenSetup;

// X errors on the display of the snapshots are counted, not fatal: a window
// can be destroyed between its event and its capture, and the default
// handler of Xlib would end the process. The first since the count was reset
// is kept with its request: the errors after it are often its consequences.
static Display *gOffDpy = 0;
static int gOffErr = 0, gOffMajor = 0, gOffMinor = 0;
static int (*gPrevHandler)(Display*, XErrorEvent*) = 0;
static int gInstalled = 0;
static int offErrorHandler(Display *dpy, XErrorEvent *ev) {
	if (dpy == gOffDpy) {
		if (!gOffErr) {
			gOffErr = ev->error_code;
			gOffMajor = ev->request_code;
			gOffMinor = ev->minor_code;
		}
		return 0;
	}
	return gPrevHandler ? gPrevHandler(dpy, ev) : 0;
}

static OffscreenSetup offSetup() {
	OffscreenSetup s;
	memset(&s, 0, sizeof(s));

	s.dpy = XOpenDisplay(NULL);
	if (!s.dpy) {
		snprintf(s.err, sizeof(s.err), "XOpenDisplay(NULL) failed (is DISPLAY set?)");
		return s;
	}
	const char *ext = glXQueryExtensionsString(s.dpy, DefaultScreen(s.dpy));
	if (!ext || !strstr(ext, "GLX_EXT_texture_from_pixmap")) {
		snprintf(s.err, sizeof(s.err), "GLX_EXT_texture_from_pixmap unavailable");
		return s;
	}
	s.bind = (offBindProc)glXGetProcAddressARB((const GLubyte*)"glXBindTexImageEXT");
	s.release = (offReleaseProc)glXGetProcAddressARB((const GLubyte*)"glXReleaseTexImageEXT");
	offCreateCtxProc create =
		(offCreateCtxProc)glXGetProcAddressARB((const GLubyte*)"glXCreateContextAttribsARB");
	if (!s.bind || !s.release || !create) {
		snprintf(s.err, sizeof(s.err), "GLX entry points missing");
		return s;
	}

	int attribs[] = {
		GLX_DRAWABLE_TYPE, GLX_PBUFFER_BIT,
		GLX_RENDER_TYPE,   GLX_RGBA_BIT,
		GLX_RED_SIZE, 8, GLX_GREEN_SIZE, 8, GLX_BLUE_SIZE, 8,
		None
	};
	int n = 0;
	GLXFBConfig *cfgs = glXChooseFBConfig(s.dpy, DefaultScreen(s.dpy), attribs, &n);
	if (!cfgs || n == 0) {
		snprintf(s.err, sizeof(s.err), "no GLX FBConfig for a pbuffer");
		return s;
	}
	GLXFBConfig fbc = cfgs[0];
	XFree(cfgs);

	gOffDpy = s.dpy;
	gOffErr = 0;
	if (!gInstalled) {
		gPrevHandler = XSetErrorHandler(offErrorHandler);
		gInstalled = 1;
	}

	int pbAttribs[] = {GLX_PBUFFER_WIDTH, 1, GLX_PBUFFER_HEIGHT, 1, None};
	s.pbuf = glXCreatePbuffer(s.dpy, fbc, pbAttribs);
	int ctxAttribs[] = {
		GLX_CONTEXT_MAJOR_VERSION_ARB, 4,
		GLX_CONTEXT_MINOR_VERSION_ARB, 6,
		GLX_CONTEXT_PROFILE_MASK_ARB,  GLX_CONTEXT_CORE_PROFILE_BIT_ARB,
		GLX_CONTEXT_FLAGS_ARB,         GLX_CONTEXT_FORWARD_COMPATIBLE_BIT_ARB,
		None
	};
	s.ctx = create(s.dpy, fbc, 0, True, ctxAttribs);
	XSync(s.dpy, False);
	if (!s.pbuf || !s.ctx || gOffErr) {
		snprintf(s.err, sizeof(s.err), "no pbuffer or no direct 4.6 core context");
		return s;
	}
	if (!glXIsDirect(s.dpy, s.ctx)) {
		snprintf(s.err, sizeof(s.err), "GLX context is indirect (no direct rendering)");
		return s;
	}
	if (!glXMakeContextCurrent(s.dpy, s.pbuf, s.pbuf, s.ctx)) {
		snprintf(s.err, sizeof(s.err), "glXMakeContextCurrent failed on the pbuffer");
		return s;
	}
	s.ok = 1;
	return s;
}

static void offTeardown(Display *dpy, GLXContext ctx, GLXPbuffer pbuf) {
	if (!dpy) {
		return;
	}
	glXMakeContextCurrent(dpy, None, None, NULL);
	if (ctx) glXDestroyContext(dpy, ctx);
	if (pbuf) glXDestroyPbuffer(dpy, pbuf);
	XCloseDisplay(dpy);
	if (gOffDpy == dpy) gOffDpy = 0;
}

// offPixmapConfig is an FBConfig to bind a pixmap of the depth as a 2D
// texture, and whether its rows run from the top
static GLXFBConfig offPixmapConfig(Display *dpy, int depth, int *yInverted) {
	int attribs[] = {
		GLX_DRAWABLE_TYPE, GLX_PIXMAP_BIT,
		depth == 32 ? GLX_BIND_TO_TEXTURE_RGBA_EXT : GLX_BIND_TO_TEXTURE_RGB_EXT, True,
		GLX_BIND_TO_TEXTURE_TARGETS_EXT, GLX_TEXTURE_2D_BIT_EXT,
		GLX_DOUBLEBUFFER, False,
		None
	};
	int n = 0;
	GLXFBConfig *cfgs = glXChooseFBConfig(dpy, DefaultScreen(dpy), attribs, &n);
	GLXFBConfig chosen = 0;
	for (int i = 0; i < n && !chosen; i++) {
		XVisualInfo *vi = glXGetVisualFromFBConfig(dpy, cfgs[i]);
		if (vi) {
			if (vi->depth == depth) chosen = cfgs[i];
			XFree(vi);
		}
	}
	if (chosen) {
		*yInverted = 0;
		glXGetFBConfigAttrib(dpy, chosen, GLX_Y_INVERTED_EXT, yInverted);
	}
	if (cfgs) XFree(cfgs);
	return chosen;
}

// offBindPixmap binds the pixmap to the texture bound to GL_TEXTURE_2D; 0 and
// the first X error with the major and minor code of its request on failure
static GLXPixmap offBindPixmap(Display *dpy, offBindProc bind, unsigned long pixmap, int depth, int *yInverted, int *xerr, int *xmajor, int *xminor) {
	*xerr = 0;
	GLXFBConfig fbc = offPixmapConfig(dpy, depth, yInverted);
	if (!fbc) {
		return 0;
	}
	int attribs[] = {
		GLX_TEXTURE_TARGET_EXT, GLX_TEXTURE_2D_EXT,
		GLX_TEXTURE_FORMAT_EXT, depth == 32 ? GLX_TEXTURE_FORMAT_RGBA_EXT : GLX_TEXTURE_FORMAT_RGB_EXT,
		None
	};
	gOffErr = 0;
	GLXPixmap gp = glXCreatePixmap(dpy, fbc, (Pixmap)pixmap, attribs);
	if (gp) bind(dpy, gp, GLX_FRONT_LEFT_EXT, NULL);
	XSync(dpy, False);
	if (gOffErr) {
		*xerr = gOffErr;
		*xmajor = gOffMajor;
		*xminor = gOffMinor;
		if (gp) glXDestroyPixmap(dpy, gp);
		XSync(dpy, False);
		return 0;
	}
	return gp;
}

// offRebind releases and binds the pixmap again, so that the texture holds
// its current contents. No round trip: the X pixmap stays valid while bound,
// and an error would be counted, not fatal.
static void offRebind(Display *dpy, offBindProc bind, offReleaseProc release, GLXPixmap gp) {
	release(dpy, gp, GLX_FRONT_LEFT_EXT);
	bind(dpy, gp, GLX_FRONT_LEFT_EXT, NULL);
}

static void offWaitX(void) {
	glXWaitX();
}

static void offRelease(Display *dpy, offReleaseProc release, GLXPixmap gp) {
	release(dpy, gp, GLX_FRONT_LEFT_EXT);
	glXDestroyPixmap(dpy, gp);
	XSync(dpy, False);
	gOffErr = 0;
}
*/
import "C"

import (
	"fmt"
	"runtime"
)

// Offscreen is a direct GLX 4.6 core context of its own, current on a
// pbuffer of 1×1, for work that draws into framebuffers and reads them back:
// the snapshots of windows (specs/008-window-snapshots). It has an Xlib
// Display of its own, whose X errors are returned, not fatal. One at a time.
type Offscreen struct {
	dpy     *C.Display
	ctx     C.GLXContext
	pbuf    C.GLXPbuffer
	bind    C.offBindProc
	release C.offReleaseProc
}

// NewOffscreen creates the context and makes it current. The calling
// goroutine is locked to its OS thread until Destroy. An error is expected
// without direct GLX 4.6 or GLX_EXT_texture_from_pixmap.
func NewOffscreen() (*Offscreen, error) {
	runtime.LockOSThread()
	s := C.offSetup()
	if s.ok == 0 {
		C.offTeardown(s.dpy, s.ctx, s.pbuf)
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("glx: %s", C.GoString(&s.err[0]))
	}
	return &Offscreen{dpy: s.dpy, ctx: s.ctx, pbuf: s.pbuf, bind: s.bind, release: s.release}, nil
}

// Share names the context, for a context of another display and thread to
// share its objects: the presenter's draws the live thumbnails the snapshots
// make (specs/020-live-thumbnails)
func (o *Offscreen) Share() *Share {
	return &Share{ctx: o.ctx}
}

// Destroy releases the context, closes its display and unlocks the thread
func (o *Offscreen) Destroy() {
	if o == nil || o.dpy == nil {
		return
	}
	C.offTeardown(o.dpy, o.ctx, o.pbuf)
	o.dpy = nil
	runtime.UnlockOSThread()
}

// TexturePixmap is an X pixmap bound as a 2D texture
type TexturePixmap struct {
	o  *Offscreen
	gp C.GLXPixmap

	// YInverted: the first row of the texture is the top row of the pixmap
	YInverted bool
}

// BindPixmap binds the pixmap, of the depth, to the texture bound to
// GL_TEXTURE_2D of the active unit. It fails while another client holds a GLX
// pixmap of the same storage — BadAlloc on glXCreatePixmap on NVIDIA
// (specs/018-snapshot-bind-conflicts); an X error is told with the major and
// minor code of its request.
func (o *Offscreen) BindPixmap(pixmap uint32, depth int) (*TexturePixmap, error) {
	var yInverted, xerr, xmajor, xminor C.int
	gp := C.offBindPixmap(o.dpy, o.bind, C.ulong(pixmap), C.int(depth), &yInverted, &xerr, &xmajor, &xminor)
	if gp == 0 {
		if xerr != 0 {
			return nil, fmt.Errorf("glx: binding pixmap 0x%x: X error %d on request %d.%d",
				pixmap, int(xerr), int(xmajor), int(xminor))
		}
		return nil, fmt.Errorf("glx: no FBConfig to bind a pixmap of depth %d", depth)
	}
	return &TexturePixmap{o: o, gp: gp, YInverted: yInverted != 0}, nil
}

// Rebind binds the pixmap again to the texture bound to GL_TEXTURE_2D, so
// that it holds the current contents of the pixmap
func (p *TexturePixmap) Rebind() {
	C.offRebind(p.o.dpy, p.o.bind, p.o.release, p.gp)
}

// WaitX waits, as glXWaitX, until the X server has done the drawing asked
// of it before, so that a pixmap it was asked to draw — through another
// connection, as RENDER of the snapshotter into a pixmap of its own — is read
// as drawn once bound again. Without it, the X server's work still on the GPU
// of NVIDIA, 5 % of the live passes of a window scaled into such a pixmap read
// it as it was before (specs/020-live-thumbnails, research); with it, none
// of 200. The context must be current.
func (o *Offscreen) WaitX() {
	C.offWaitX()
}

// Release unbinds the pixmap from its texture; the X pixmap itself stays
func (p *TexturePixmap) Release() {
	if p == nil || p.gp == 0 {
		return
	}
	C.offRelease(p.o.dpy, p.o.release, p.gp)
	p.gp = 0
}
