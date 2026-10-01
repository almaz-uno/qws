package snapshot

import (
	"errors"
	"testing"
	"time"

	"github.com/almaz-uno/qws/pkg/glx"
	"github.com/jezek/xgb"
)

// TestFallback checks criterion K6 of specs/008-window-snapshots: without a GL
// context for the snapshots, New fails, and qws keeps the snapshots of 1.0.0
func TestFallback(t *testing.T) {
	conn, err := xgb.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	conn.Close()

	saved := newOffscreen
	defer func() { newOffscreen = saved }()
	newOffscreen = func() (*glx.Offscreen, error) {
		return nil, errors.New("no GLX here")
	}
	s, err := New(time.Second, "bilinear")
	if err == nil {
		s.Close()
		t.Fatal("New succeeded without a GL context")
	}
}

// TestNewAndClose starts the snapshotter on the display there is and stops it:
// it follows the client windows and frees what it holds
func TestNewAndClose(t *testing.T) {
	s, err := New(time.Second, "bilinear")
	if err != nil {
		t.Skipf("no snapshots on the GPU here: %v", err)
	}
	s.Refresh(100 * time.Millisecond)
	s.Close()
}
