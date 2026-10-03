package glx

import "testing"

// TestShareError checks the sharing of specs/020-live-thumbnails: a context
// created with the snapshots' context as its share context, on a display of
// its own, shares its objects and says so; one created without says it does
// not. Needs an X display with direct GLX 4.6; skips where the driver shares
// no objects between two displays.
func TestShareError(t *testing.T) {
	off, err := NewOffscreen()
	if err != nil {
		t.Skipf("no offscreen GLX: %v", err)
	}
	defer off.Destroy()

	shared, err := NewContext(off.Share())
	if err != nil {
		t.Skipf("no GLX context: %v", err)
	}
	defer shared.Destroy()
	if err := shared.ShareError(); err != nil {
		t.Skipf("the context does not share the offscreen one's objects: %v", err)
	}

	alone, err := NewContext(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer alone.Destroy()
	if alone.ShareError() == nil {
		t.Error("a context created without a share context says it shares")
	}
}
