package x11

import (
	"fmt"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// The switchers of the qws instances of a display (specs/011-snapshot-pause):
// each lists its overlay window in the property _QWS_SWITCHERS of the root
// window, and the others follow the map state of the windows listed. The X
// server destroys the windows of a client that dies, so a switcher killed
// while shown is not followed as shown for good.

// switchersName is the property; a variable, so that a test can list its
// windows in one of its own and leave the display's alone
var switchersName = "_QWS_SWITCHERS"

// switchersAtom is the atom of _QWS_SWITCHERS
func switchersAtom(conn *xgb.Conn) (xproto.Atom, error) {
	reply, err := xproto.InternAtom(conn, false, uint16(len(switchersName)), switchersName).Reply()
	if err != nil {
		return 0, fmt.Errorf("%s: %w", switchersName, err)
	}
	return reply.Atom, nil
}

// readSwitchers is the list of _QWS_SWITCHERS; empty without the property
func readSwitchers(conn *xgb.Conn, root xproto.Window, atom xproto.Atom) ([]xproto.Window, error) {
	reply, err := xproto.GetProperty(conn, false, root, atom, xproto.AtomWindow, 0, 1<<16).Reply()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", switchersName, err)
	}
	if reply.Format != 32 {
		return nil, nil
	}
	ids := make([]xproto.Window, 0, reply.ValueLen)
	for i := 0; i+4 <= len(reply.Value); i += 4 {
		ids = append(ids, xproto.Window(xgb.Get32(reply.Value[i:])))
	}
	return ids, nil
}

// ListSwitcher lists the overlay window win in _QWS_SWITCHERS and drops from
// it the windows that no longer exist. It grabs the server meanwhile, so that
// instances listing at once lose none.
func ListSwitcher(conn *xgb.Conn, root, win xproto.Window) error {
	atom, err := switchersAtom(conn)
	if err != nil {
		return err
	}
	xproto.GrabServer(conn)
	defer xproto.UngrabServer(conn)

	ids, err := readSwitchers(conn, root, atom)
	if err != nil {
		return err
	}
	var keep []xproto.Window
	for _, id := range ids {
		if id == win {
			continue
		}
		if _, err := xproto.GetWindowAttributes(conn, id).Reply(); err == nil {
			keep = append(keep, id)
		}
	}
	keep = append(keep, win)
	data := make([]byte, 4*len(keep))
	for i, id := range keep {
		xgb.Put32(data[4*i:], uint32(id))
	}
	return xproto.ChangePropertyChecked(conn, xproto.PropModeReplace, root, atom,
		xproto.AtomWindow, 32, uint32(len(keep)), data).Check()
}

// Switchers follows the switchers listed in _QWS_SWITCHERS through a
// connection whose events its owner reads and passes to Handle. The owner
// selects the property changes of the root window; Switchers selects the
// structure events of the windows it follows.
type Switchers struct {
	conn   *xgb.Conn
	root   xproto.Window
	atom   xproto.Atom
	mapped map[xproto.Window]bool // the windows followed: whether mapped
}

// NewSwitchers starts following the switchers listed now
func NewSwitchers(conn *xgb.Conn, root xproto.Window) (*Switchers, error) {
	atom, err := switchersAtom(conn)
	if err != nil {
		return nil, err
	}
	s := &Switchers{conn: conn, root: root, atom: atom, mapped: make(map[xproto.Window]bool)}
	s.read()
	return s, nil
}

// read follows the windows of the list not followed yet; a window that no
// longer exists is not followed
func (s *Switchers) read() {
	ids, err := readSwitchers(s.conn, s.root, s.atom)
	if err != nil {
		return
	}
	for _, id := range ids {
		if _, ok := s.mapped[id]; ok {
			continue
		}
		// Selected first, then asked: a map in between comes as an event
		if err := xproto.ChangeWindowAttributesChecked(s.conn, id, xproto.CwEventMask,
			[]uint32{xproto.EventMaskStructureNotify}).Check(); err != nil {
			continue
		}
		attrs, err := xproto.GetWindowAttributes(s.conn, id).Reply()
		if err != nil {
			continue
		}
		s.mapped[id] = attrs.MapState != xproto.MapStateUnmapped
	}
}

// Handle takes an event of the connection and reports whether it was about
// the switchers
func (s *Switchers) Handle(ev xgb.Event) bool {
	if s == nil {
		return false
	}
	switch e := ev.(type) {
	case xproto.PropertyNotifyEvent:
		if e.Window == s.root && e.Atom == s.atom {
			s.read()
			return true
		}
	case xproto.MapNotifyEvent:
		if _, ok := s.mapped[e.Window]; ok {
			s.mapped[e.Window] = true
			return true
		}
	case xproto.UnmapNotifyEvent:
		if _, ok := s.mapped[e.Window]; ok {
			s.mapped[e.Window] = false
			return true
		}
	case xproto.DestroyNotifyEvent:
		if _, ok := s.mapped[e.Window]; ok {
			delete(s.mapped, e.Window)
			return true
		}
	}
	return false
}

// Shown reports whether a switcher is shown
func (s *Switchers) Shown() bool {
	if s == nil {
		return false
	}
	for _, mapped := range s.mapped {
		if mapped {
			return true
		}
	}
	return false
}
