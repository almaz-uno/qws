package x11

import (
	"fmt"
	"slices"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/rs/zerolog/log"

	// The packages of the extensions, for the constructors of their events
	// and errors their init functions put in xgb.NewExtEventFuncs and
	// xgb.NewExtErrorFuncs
	_ "github.com/jezek/xgb/composite"
	_ "github.com/jezek/xgb/damage"
	_ "github.com/jezek/xgb/randr"
	_ "github.com/jezek/xgb/render"
	_ "github.com/jezek/xgb/shm"
	_ "github.com/jezek/xgb/xcmisc"
	_ "github.com/jezek/xgb/xfixes"
)

// The extensions of the X server qws uses are set up once per process
// (specs/021-xgb-extension-init). The Init of an extension package of xgb
// writes the constructors of the extension's events and errors into
// xgb.NewEventFuncs and xgb.NewErrorFuncs, maps shared by every connection of
// the process and read without a lock by the reader goroutine of each
// connection for every event and error it reads: an Init while a connection
// reads events can end the process with "concurrent map read and map write".
// The first NewConn writes the maps before it makes its connection, with no
// reader of any connection running, and each connection gets only the major
// opcodes, which xgb keeps per connection under its lock; CheckExtension takes
// the place of Init.

// extensionNames are the extensions set up, by the names xgb gives them
var extensionNames = []string{"Composite", "DAMAGE", "MIT-SHM", "RANDR", "RENDER", "XC-MISC", "XFIXES"}

// extensionOpcodes are the major opcodes of the extensions the X server has,
// asked and set up at the first call; an error of that call is kept
var extensionOpcodes = sync.OnceValues(setupExtensions)

// NewConn connects to the X server of $DISPLAY, as xgb.NewConn does, with the
// extensions of the X server among extensionNames ready to use and, with
// XC-MISC, its XIDs reused (specs/024-xid-reuse). Every X connection of the
// program qws is made by it, so that its first call sets the extensions up
// for the process before any of them exists.
func NewConn() (*xgb.Conn, error) {
	opcodes, err := extensionOpcodes()
	if err != nil {
		return nil, err
	}
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, err
	}
	conn.ExtLock.Lock()
	for name, opcode := range opcodes {
		conn.Extensions[name] = opcode
	}
	conn.ExtLock.Unlock()
	if _, ok := opcodes["XC-MISC"]; ok {
		conn.SetIDRangeFunc(newXIDRanges(xproto.Setup(conn)).next)
	}
	return conn, nil
}

// CheckExtension fails unless the extension can be used on the connection: the
// X server has it, and NewConn made the connection
func CheckExtension(conn *xgb.Conn, name string) error {
	if !slices.Contains(extensionNames, name) {
		return fmt.Errorf("extension %s not set up by x11.NewConn", name)
	}
	conn.ExtLock.RLock()
	_, ok := conn.Extensions[name]
	conn.ExtLock.RUnlock()
	if !ok {
		return fmt.Errorf("no extension %s on the connection", name)
	}
	return nil
}

// setupExtensions asks the X server for the extensions through a connection
// of its own, closes it and waits for its reader to end, and only then writes
// the constructors of their events and errors, at the numbers the X server
// gave, as their Init would: no reader of an xgb connection runs meanwhile,
// since NewConn makes every other.
func setupExtensions() (map[string]byte, error) {
	probe, err := xgb.NewConn()
	if err != nil {
		return nil, err
	}
	cookies := make([]xproto.QueryExtensionCookie, len(extensionNames))
	for i, name := range extensionNames {
		cookies[i] = xproto.QueryExtension(probe, uint16(len(name)), name)
	}
	replies := make([]*xproto.QueryExtensionReply, len(extensionNames))
	for i, cookie := range cookies {
		if replies[i], err = cookie.Reply(); err != nil {
			break
		}
	}
	probe.Close()
	// The last act of the reader of xgb 1.3.1 is to close the channel of
	// events, or to pass a read error on it: WaitForEvent then returns neither
	// an event nor an error, and the reader reads the maps no more
	for {
		if ev, xerr := probe.WaitForEvent(); ev == nil && xerr == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("querying the extensions: %w", err)
	}

	opcodes := make(map[string]byte)
	for i, name := range extensionNames {
		reply := replies[i]
		if !reply.Present {
			continue
		}
		for n, f := range xgb.NewExtEventFuncs[name] {
			xgb.NewEventFuncs[int(reply.FirstEvent)+n] = f
		}
		for n, f := range xgb.NewExtErrorFuncs[name] {
			xgb.NewErrorFuncs[int(reply.FirstError)+n] = f
		}
		opcodes[name] = reply.MajorOpcode
	}
	if _, ok := opcodes["XC-MISC"]; !ok {
		log.Warn().Msg("XC-MISC unavailable, XIDs not reused: a connection makes no resource " +
			"once it has given every XID of its mask")
	}
	return opcodes, nil
}
