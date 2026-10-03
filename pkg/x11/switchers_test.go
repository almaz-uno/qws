package x11

import (
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// TestSwitchers checks criterion K1 of specs/011-snapshot-pause: switchers
// listed by one connection and followed from another are shown while mapped,
// not once unmapped or destroyed, and listing a switcher drops the destroyed
// ones from the list. The windows are override-redirect and off the screen;
// the list is a property of the test's own.
func TestSwitchers(t *testing.T) {
	owner, err := xgb.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer owner.Close()
	follower, err := xgb.NewConn()
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Close()

	saved := switchersName
	switchersName = fmt.Sprintf("_QWS_SWITCHERS_TEST_%d", os.Getpid())
	defer func() { switchersName = saved }()
	root := xproto.Setup(owner).DefaultScreen(owner).Root
	atom, err := switchersAtom(owner)
	if err != nil {
		t.Fatal(err)
	}
	defer xproto.DeleteProperty(owner, root, atom)

	window := func() xproto.Window {
		id, err := xproto.NewWindowId(owner)
		if err != nil {
			t.Fatal(err)
		}
		if err := xproto.CreateWindowChecked(owner, xproto.WindowClassCopyFromParent, id, root,
			-100, -100, 10, 10, 0, xproto.WindowClassInputOutput, xproto.WindowClassCopyFromParent,
			xproto.CwOverrideRedirect, []uint32{1}).Check(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { xproto.DestroyWindow(owner, id) })
		return id
	}
	w1, w2 := window(), window()

	if err := xproto.ChangeWindowAttributesChecked(follower, root, xproto.CwEventMask,
		[]uint32{xproto.EventMaskPropertyChange}).Check(); err != nil {
		t.Fatal(err)
	}
	s, err := NewSwitchers(follower, root)
	if err != nil {
		t.Fatal(err)
	}
	// until handles the follower's events until cond holds, for a second
	until := func(what string, cond func() bool) {
		t.Helper()
		owner.Sync()
		for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
			ev, _ := follower.PollForEvent()
			if ev != nil {
				s.Handle(ev)
				continue
			}
			if cond() {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("%s: not within a second", what)
	}
	shown := func() bool { return s.Shown(0) }
	hidden := func() bool { return !s.Shown(0) }

	for _, w := range []xproto.Window{w1, w2} {
		if err := ListSwitcher(owner, root, w); err != nil {
			t.Fatal(err)
		}
	}
	until("both followed", func() bool { return len(s.mapped) == 2 })
	if s.Shown(0) {
		t.Error("shown with no switcher mapped")
	}

	xproto.MapWindow(owner, w1)
	until("mapped, shown", shown)
	xproto.UnmapWindow(owner, w1)
	until("unmapped, not shown", hidden)
	xproto.MapWindow(owner, w1)
	until("mapped again, shown", shown)
	xproto.DestroyWindow(owner, w1)
	until("destroyed, not shown", func() bool { _, ok := s.mapped[w1]; return !ok && !s.Shown(0) })

	w3 := window()
	if err := ListSwitcher(owner, root, w3); err != nil {
		t.Fatal(err)
	}
	ids, err := readSwitchers(owner, root, atom)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ids, []xproto.Window{w2, w3}) {
		t.Errorf("listed %v, want %v: the destroyed %v dropped", ids, []xproto.Window{w2, w3}, w1)
	}
	until("the third followed", func() bool { _, ok := s.mapped[w3]; return ok })
}

// TestSwitchersOwnOverlay checks D5 of specs/020-live-thumbnails: the
// switchers shown but the one left out — the instance's own overlay, whose
// live thumbnails do not pause for it — are those of the other instances
func TestSwitchersOwnOverlay(t *testing.T) {
	conn, err := xgb.NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	saved := switchersName
	switchersName = fmt.Sprintf("_QWS_SWITCHERS_TEST_OWN_%d", os.Getpid())
	defer func() { switchersName = saved }()
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	atom, err := switchersAtom(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer xproto.DeleteProperty(conn, root, atom)

	var own, other xproto.Window
	for _, w := range []*xproto.Window{&own, &other} {
		id, err := xproto.NewWindowId(conn)
		if err != nil {
			t.Fatal(err)
		}
		if err := xproto.CreateWindowChecked(conn, xproto.WindowClassCopyFromParent, id, root,
			-100, -100, 10, 10, 0, xproto.WindowClassInputOutput, xproto.WindowClassCopyFromParent,
			xproto.CwOverrideRedirect, []uint32{1}).Check(); err != nil {
			t.Fatal(err)
		}
		defer xproto.DestroyWindow(conn, id)
		if err := ListSwitcher(conn, root, id); err != nil {
			t.Fatal(err)
		}
		*w = id
	}
	s, err := NewSwitchers(conn, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.mapped) != 2 {
		t.Fatalf("%d switchers followed, want 2", len(s.mapped))
	}

	// The states the events give, set as the events would
	for _, c := range []struct {
		ownMapped, otherMapped bool
		all, butOwn            bool
	}{
		{false, false, false, false},
		{true, false, true, false},
		{false, true, true, true},
		{true, true, true, true},
	} {
		s.mapped[own], s.mapped[other] = c.ownMapped, c.otherMapped
		if got := s.Shown(0); got != c.all {
			t.Errorf("own %v, other %v: shown %v, want %v", c.ownMapped, c.otherMapped, got, c.all)
		}
		if got := s.Shown(own); got != c.butOwn {
			t.Errorf("own %v, other %v: shown but the own %v, want %v", c.ownMapped, c.otherMapped, got, c.butOwn)
		}
	}
}
