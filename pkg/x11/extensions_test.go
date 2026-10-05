package x11

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog"
)

// TestMonitorsUnderEvents checks criterion K1 of
// specs/021-xgb-extension-init: GetMonitors, called at every activation, is
// called again and again on one connection for a second while another
// connection reads a flood of events. Before the change each call initialised
// RandR, writing the tables of events and errors xgb shares among the
// connections of the process while the reader of the other connection read
// them: the Go runtime ended the process with "concurrent map read and map
// write", and the race detector reported the race. The events are
// ClientMessages sent with an empty event mask to an unmapped window of the
// test's own, which the X server delivers to the window's creator alone; the
// X server spends a core on them for the second.
func TestMonitorsUnderEvents(t *testing.T) {
	conn, err := Connect()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer conn.Close()
	level := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	defer zerolog.SetGlobalLevel(level)
	if _, err := GetMonitors(conn.Conn, conn.Root); err != nil {
		t.Skipf("no monitors through RandR: %v", err)
	}

	flood, err := xgb.NewConn()
	if err != nil {
		t.Fatal(err)
	}
	defer flood.Close()
	win, err := xproto.NewWindowId(flood)
	if err != nil {
		t.Fatal(err)
	}
	if err := xproto.CreateWindowChecked(flood, 0, win, conn.Root, -100, -100, 1, 1, 0,
		xproto.WindowClassInputOnly, 0, xproto.CwOverrideRedirect, []uint32{1}).Check(); err != nil {
		t.Fatal(err)
	}
	var received atomic.Int64
	stop, stopped, read := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(read)
		for {
			ev, xerr := flood.WaitForEvent()
			if ev == nil && xerr == nil {
				return // closed
			}
			received.Add(1)
		}
	}()
	go func() {
		defer close(stopped)
		message := string(xproto.ClientMessageEvent{Format: 32, Window: win, Type: xproto.AtomNone,
			Data: xproto.ClientMessageDataUnionData32New(make([]uint32, 5))}.Bytes())
		for {
			select {
			case <-stop:
				return
			default:
				xproto.SendEvent(flood, false, win, 0, message)
			}
		}
	}()
	// halt ends the flood, however the test ends
	halt := sync.OnceFunc(func() {
		close(stop)
		<-stopped
		xproto.DestroyWindow(flood, win)
		flood.Close()
		<-read
	})
	defer halt()

	calls := 0
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); calls++ {
		if _, err := GetMonitors(conn.Conn, conn.Root); err != nil {
			t.Fatal(err)
		}
	}
	halt()
	t.Logf("%d calls of GetMonitors, %d events read meanwhile", calls, received.Load())
	if calls < 100 || received.Load() < 10000 {
		t.Errorf("%d calls, %d events: too few to overlap", calls, received.Load())
	}
}
