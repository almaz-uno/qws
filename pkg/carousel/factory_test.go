package carousel

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// TestGLXFallback checks criterion K4 of specs/001-rendering-speed: when GLX
// cannot be initialised, "glx" gives the cpu renderer and presenter and warns.
func TestGLXFallback(t *testing.T) {
	saved := newGLXPresenter
	defer func() { newGLXPresenter = saved }()
	newGLXPresenter = func() (Presenter, error) {
		return nil, errors.New("injected GLX failure")
	}

	var out bytes.Buffer
	savedLogger := log.Logger
	defer func() { log.Logger = savedLogger }()
	log.Logger = zerolog.New(&out)

	renderer, presenter, err := NewBackend("glx", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := renderer.(*CPURenderer); !ok {
		t.Errorf("renderer %T, want *CPURenderer", renderer)
	}
	if _, ok := presenter.(*x11Presenter); !ok {
		t.Errorf("presenter %T, want *x11Presenter", presenter)
	}
	if !strings.Contains(out.String(), `"level":"warn"`) || !strings.Contains(out.String(), "falling back to CPU") {
		t.Errorf("no fallback warning in the log: %s", out.String())
	}
}
