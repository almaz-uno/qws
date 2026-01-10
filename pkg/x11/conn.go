package x11

import (
	"fmt"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// Connection represents a connection to an X11 server
type Connection struct {
	Conn   *xgb.Conn
	Root   xproto.Window
	Screen *xproto.ScreenInfo
}

// Connect establishes a connection to an X server
func Connect() (*Connection, error) {
	// Connect to X server (uses DISPLAY environment variable)
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to X server: %w", err)
	}

	// Get setup information
	setup := xproto.Setup(conn)
	if setup == nil || len(setup.Roots) == 0 {
		conn.Close()
		return nil, fmt.Errorf("failed to get root window information")
	}

	// Take the first screen
	screen := setup.Roots[0]
	root := screen.Root

	return &Connection{
		Conn:   conn,
		Root:   root,
		Screen: &screen,
	}, nil
}

// Close closes the connection to the X server
func (c *Connection) Close() {
	if c.Conn != nil {
		c.Conn.Close()
	}
}

// InternAtom gets an atom by name
func (c *Connection) InternAtom(name string, onlyIfExists bool) (xproto.Atom, error) {
	reply, err := xproto.InternAtom(c.Conn, onlyIfExists, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, fmt.Errorf("failed to get atom %s: %w", name, err)
	}
	return reply.Atom, nil
}
