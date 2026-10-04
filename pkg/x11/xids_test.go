package x11

import (
	"flag"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
)

// xidRequests makes TestXIDsReused make and free a pixmap with every XID it
// takes, not one in madeEvery past the first range: some 15 s of the X
// server's CPU on E1 (specs/024-xid-reuse), so off unless asked for, as by
// go test ./pkg/x11 -run TestXIDsReused -args -xid-requests
var xidRequests = flag.Bool("xid-requests", false,
	"TestXIDsReused: a pixmap of 1×1 made and freed with every XID")

// TestXIDsReused checks criterion K1 of specs/024-xid-reuse: a connection of
// NewConn goes on giving XIDs past the end of its first range — every XID of
// its resource-id-mask, which xgb gives in turn — taken from the X server
// through XC-MISC, and the X server takes them for new resources. Before the
// change NewId failed there: xgb reuses no XID unless a range function is
// set. Pixmaps kept from the first range, one every keptEvery XIDs, are left
// out of the ranges the X server gives. The XIDs alone are taken, but for the
// pixmaps made with some of them: xgb does not know whether an XID it gave
// names a resource, and the X server's CPU stays out of the test.
func TestXIDsReused(t *testing.T) {
	conn, err := NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	name := "XC-MISC"
	ext, err := xproto.QueryExtension(conn, uint16(len(name)), name).Reply()
	if err != nil {
		t.Fatal(err)
	}
	if !ext.Present {
		t.Skip("no XC-MISC on the X server")
	}
	// The errors of the requests not checked, read so that the reader of the
	// connection never waits for the queue of events
	var failed atomic.Int64
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			ev, xerr := conn.WaitForEvent()
			if ev == nil && xerr == nil {
				return // closed
			}
			if xerr != nil && failed.Add(1) == 1 {
				t.Errorf("a request failed: %v", xerr)
			}
		}
	}()
	defer func() { conn.Close(); <-drained }()

	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)
	root, depth := xproto.Drawable(screen.Root), screen.RootDepth
	base, mask := setup.ResourceIdBase, setup.ResourceIdMask
	inc := mask & -mask
	// xgb gives inc, 2·inc, … up to mask, each ORed with base, then asks for a
	// range
	first := mask / inc
	const (
		keptEvery = 1 << 18 // a pixmap kept to the end, at the first XID and every keptEvery
		madeEvery = 1 << 12 // a pixmap made and freed, checked, every madeEvery XIDs past the first range
		beyond    = 2 * keptEvery
	)

	kept := make(map[uint32]bool)
	var made []xproto.CreatePixmapCookie
	jumps, prev := 0, uint32(0)
	begin := time.Now()
	for i := uint32(1); i <= first+beyond; i++ {
		id, err := conn.NewId()
		if err != nil {
			t.Fatalf("XID %d, %d past the first range of %d: %v", i, int64(i)-int64(first), first, err)
		}
		if i > first {
			if id&^mask != base {
				t.Fatalf("XID 0x%x not of the connection, base 0x%x and mask 0x%x", id, base, mask)
			}
			if kept[id] {
				t.Fatalf("XID 0x%x given again while its pixmap is kept", id)
			}
			if id != prev+inc {
				jumps++
			}
		}
		prev = id
		p := xproto.Pixmap(id)
		switch {
		case i <= first && (i == 1 || i%keptEvery == 0):
			kept[id] = true
			made = append(made, xproto.CreatePixmapChecked(conn, depth, p, root, 1, 1))
		case i > first && (i-first)%madeEvery == 0:
			made = append(made, xproto.CreatePixmapChecked(conn, depth, p, root, 1, 1))
			xproto.FreePixmap(conn, p)
		case *xidRequests:
			xproto.CreatePixmap(conn, depth, p, root, 1, 1)
			xproto.FreePixmap(conn, p)
		}
	}
	for _, c := range made {
		if err := c.Check(); err != nil {
			t.Fatalf("a pixmap of the test: %v", err)
		}
	}
	elapsed := time.Since(begin)
	conn.Close()
	<-drained
	t.Logf("%d XIDs of the first range, %d past it from %d ranges of XC-MISC; %d pixmaps kept, %d made "+
		"with reused XIDs, every XID with a pixmap: %v; %.2f s",
		first, beyond, jumps, len(kept), len(made)-len(kept), *xidRequests, elapsed.Seconds())
}

// TestXIDsAheadNotGivenAgain checks criterion K6 of specs/024-xid-reuse: xgb
// asks for a range right after it has put the last XID of the one before in
// the buffer of NewId, and the X server takes for free the XIDs it has given
// and not yet seen named. Here the last 20 XIDs of the first range are taken a
// millisecond apart and each named by a pixmap a millisecond after its NewId,
// as by a goroutine slower than the X server: the five in the buffer and the
// one taken last when xgb asks. Their pixmaps live on, and the next two
// million XIDs, the walk through the X server's range, must hold none of
// theirs: before #74 xgb gave the six again, and the X server answered
// BadIDChoice.
func TestXIDsAheadNotGivenAgain(t *testing.T) {
	conn, err := NewConn()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	name := "XC-MISC"
	ext, err := xproto.QueryExtension(conn, uint16(len(name)), name).Reply()
	if err != nil {
		t.Fatal(err)
	}
	if !ext.Present {
		t.Skip("no XC-MISC on the X server")
	}
	var failed atomic.Int64
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			ev, xerr := conn.WaitForEvent()
			if ev == nil && xerr == nil {
				return // closed
			}
			if xerr != nil && failed.Add(1) == 1 {
				t.Errorf("a request failed: %v", xerr)
			}
		}
	}()
	defer func() { conn.Close(); <-drained }()

	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)
	root, depth := xproto.Drawable(screen.Root), screen.RootDepth
	mask := setup.ResourceIdMask
	first := mask / (mask & -mask)
	const (
		slow = 20 // the last XIDs of the first range taken a millisecond apart
		kept = 6  // the last of them, with a pixmap that lives on
	)
	held := make(map[uint32]uint32) // XID → the NewId that gave it
	begin := time.Now()
	for i := uint32(1); i <= 2*first+16; i++ {
		id, err := conn.NewId()
		if err != nil {
			t.Fatalf("XID %d: %v", i, err)
		}
		if at, ok := held[id]; ok {
			t.Fatalf("XID %d, 0x%x, given again while the pixmap of XID %d lives", i, id, at)
		}
		if i > first-slow && i <= first {
			time.Sleep(time.Millisecond)
			if i > first-kept {
				held[id] = i
				xproto.CreatePixmap(conn, depth, xproto.Pixmap(id), root, 1, 1)
			}
		}
	}
	if _, err := xproto.GetInputFocus(conn).Reply(); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(begin)
	conn.Close()
	<-drained
	t.Logf("%d XIDs, the last %d of the first range a millisecond apart, %d of them kept with a pixmap; %.2f s",
		2*first+16, slow, len(held), elapsed.Seconds())
}
