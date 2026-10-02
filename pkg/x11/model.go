package x11

import (
	"fmt"
	"image"
	"strings"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// Model is a model of the client windows, kept up to date by PropertyNotify
// on an X connection of its own (specs/003-window-list): the list of an
// activation is read from memory, not window after window from the X server.
// It gives the list GetWindowListFiltered gives, from the same properties
// read the same way.
type Model struct {
	srv   server
	root  xproto.Window
	atoms modelAtoms
	conn  *xgb.Conn // nil for a model on a fake server

	mu      sync.Mutex
	order   []xproto.Window // _NET_CLIENT_LIST
	windows map[xproto.Window]*modelWindow

	// _NET_DESKTOP_NAMES and _NET_CURRENT_DESKTOP of the root
	names     []string
	namesOK   bool
	current   uint32
	currentOK bool
}

// modelWindow is what the list needs of a window, as last read
type modelWindow struct {
	nameOK  bool // the name could be read: the window exists
	name    string
	states  []xproto.Atom
	desktop uint32
	deskOK  bool
	icon    image.Image // of _NET_WM_ICON
	class   string      // of WM_CLASS, lower case
	classOK bool
	hinted  bool // WM_HINTS carries the urgency hint
}

// modelAtoms are the atoms the model reads, resolved once as the queries of
// windows.go resolve them, only if they exist
type modelAtoms struct {
	clientList, netWmName, utf8String, netWmState, skipTaskbar, hidden,
	demandsAttention, netWmDesktop, desktopNames, currentDesktop, netWmIcon xproto.Atom
}

// server is what the model asks of the X server: a property, and the
// changes of a window's properties. Tests give it a fake.
type server interface {
	property(w xproto.Window, atom, typ xproto.Atom, offset, length uint32) (*xproto.GetPropertyReply, error)
	watch(w xproto.Window) error
}

// xserver is the server of an xgb connection
type xserver struct{ conn *xgb.Conn }

func (x xserver) property(w xproto.Window, atom, typ xproto.Atom, offset, length uint32) (*xproto.GetPropertyReply, error) {
	return xproto.GetProperty(x.conn, false, w, atom, typ, offset, length).Reply()
}

func (x xserver) watch(w xproto.Window) error {
	return xproto.ChangeWindowAttributesChecked(x.conn, w, xproto.CwEventMask,
		[]uint32{xproto.EventMaskPropertyChange}).Check()
}

// NewModel connects to the X server and reads every client window. It fails
// without an X server; the caller then collects the list at each activation.
func NewModel() (*Model, error) {
	conn, err := NewConn()
	if err != nil {
		return nil, fmt.Errorf("model: %w", err)
	}
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	atom := func(name string) xproto.Atom {
		reply, err := xproto.InternAtom(conn, true, uint16(len(name)), name).Reply()
		if err != nil {
			return 0
		}
		return reply.Atom
	}
	atoms := modelAtoms{
		clientList:       atom("_NET_CLIENT_LIST"),
		netWmName:        atom("_NET_WM_NAME"),
		utf8String:       atom("UTF8_STRING"),
		netWmState:       atom("_NET_WM_STATE"),
		skipTaskbar:      atom("_NET_WM_STATE_SKIP_TASKBAR"),
		hidden:           atom("_NET_WM_STATE_HIDDEN"),
		demandsAttention: atom("_NET_WM_STATE_DEMANDS_ATTENTION"),
		netWmDesktop:     atom("_NET_WM_DESKTOP"),
		desktopNames:     atom("_NET_DESKTOP_NAMES"),
		currentDesktop:   atom("_NET_CURRENT_DESKTOP"),
		netWmIcon:        atom("_NET_WM_ICON"),
	}
	srv := xserver{conn}
	if err := srv.watch(root); err != nil {
		conn.Close()
		return nil, fmt.Errorf("model: %w", err)
	}
	m := newModel(srv, root, atoms)
	m.conn = conn
	go m.run()
	return m, nil
}

// newModel reads the root and every client window from the server
func newModel(srv server, root xproto.Window, atoms modelAtoms) *Model {
	m := &Model{srv: srv, root: root, atoms: atoms, windows: make(map[xproto.Window]*modelWindow)}
	m.readNames()
	m.readCurrent()
	m.reconcile()
	return m
}

// Close closes the model's connection
func (m *Model) Close() {
	if m.conn != nil {
		m.conn.Close()
	}
}

// run takes the events of the connection
func (m *Model) run() {
	for {
		ev, err := m.conn.WaitForEvent()
		if ev == nil && err == nil {
			return // connection closed
		}
		if ev != nil {
			m.mu.Lock()
			m.handle(ev)
			m.mu.Unlock()
		}
	}
}

// List is the list of windows GetWindowListFiltered gives for the options,
// as the X server has it now: one round trip lets the events sent before it
// in first
func (m *Model) List(opts WindowFilterOptions) ([]WindowInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.conn != nil {
		if _, err := xproto.GetInputFocus(m.conn).Reply(); err != nil {
			return nil, err
		}
		for {
			ev, _ := m.conn.PollForEvent()
			if ev == nil {
				break
			}
			m.handle(ev)
		}
	}
	return m.build(opts), nil
}

// handle reads again what a PropertyNotify says has changed
func (m *Model) handle(ev xgb.Event) {
	e, ok := ev.(xproto.PropertyNotifyEvent)
	if !ok {
		return
	}
	a := m.atoms
	if e.Window == m.root {
		switch e.Atom {
		case a.clientList:
			m.reconcile()
		case a.desktopNames:
			m.readNames()
		case a.currentDesktop:
			m.readCurrent()
		}
		return
	}
	w, ok := m.windows[e.Window]
	if !ok {
		return
	}
	switch e.Atom {
	case a.netWmName, xproto.AtomWmName:
		m.readName(e.Window, w)
	case a.netWmState:
		m.readStates(e.Window, w)
	case a.netWmDesktop:
		m.readDesktop(e.Window, w)
	case a.netWmIcon:
		m.readIcon(e.Window, w)
	case xproto.AtomWmClass:
		m.readClass(e.Window, w)
	case xproto.AtomWmHints:
		m.readHints(e.Window, w)
	}
}

// reconcile follows the windows of _NET_CLIENT_LIST, in its order: a new one
// is watched, then read in full
func (m *Model) reconcile() {
	order := []xproto.Window{}
	if prop, err := m.srv.property(m.root, m.atoms.clientList, xproto.AtomWindow, 0, ^uint32(0)); err == nil {
		for i := 0; i+4 <= int(prop.ValueLen)*4 && i+4 <= len(prop.Value); i += 4 {
			order = append(order, xproto.Window(xgb.Get32(prop.Value[i:])))
		}
	}
	listed := make(map[xproto.Window]bool, len(order))
	for _, id := range order {
		listed[id] = true
		if _, ok := m.windows[id]; !ok {
			w := &modelWindow{}
			m.srv.watch(id) // a window gone already reads as gone
			m.readName(id, w)
			m.readStates(id, w)
			m.readDesktop(id, w)
			m.readClass(id, w)
			m.readIcon(id, w)
			m.readHints(id, w)
			m.windows[id] = w
		}
	}
	for id := range m.windows {
		if !listed[id] {
			delete(m.windows, id)
		}
	}
	m.order = order
}

// readName reads the name as GetWindowName does
func (m *Model) readName(id xproto.Window, w *modelWindow) {
	if m.atoms.netWmName != 0 {
		prop, err := m.srv.property(id, m.atoms.netWmName, m.atoms.utf8String, 0, ^uint32(0))
		if err == nil && prop.ValueLen > 0 {
			w.nameOK, w.name = true, string(prop.Value)
			return
		}
	}
	prop, err := m.srv.property(id, xproto.AtomWmName, xproto.AtomString, 0, ^uint32(0))
	if err != nil {
		w.nameOK = false
		return
	}
	w.nameOK = true
	if prop.ValueLen == 0 {
		w.name = fmt.Sprintf("<unnamed 0x%x>", id)
	} else {
		w.name = string(prop.Value)
	}
}

// readStates reads _NET_WM_STATE as GetWindowState does
func (m *Model) readStates(id xproto.Window, w *modelWindow) {
	w.states = nil
	if m.atoms.netWmState == 0 {
		return
	}
	prop, err := m.srv.property(id, m.atoms.netWmState, xproto.AtomAtom, 0, ^uint32(0))
	if err != nil {
		return
	}
	for i := 0; i < int(prop.ValueLen) && 4*i+4 <= len(prop.Value); i++ {
		w.states = append(w.states, xproto.Atom(xgb.Get32(prop.Value[4*i:])))
	}
}

// readDesktop reads _NET_WM_DESKTOP as GetWindowDesktop does
func (m *Model) readDesktop(id xproto.Window, w *modelWindow) {
	w.deskOK = false
	if m.atoms.netWmDesktop == 0 {
		return
	}
	prop, err := m.srv.property(id, m.atoms.netWmDesktop, xproto.AtomCardinal, 0, 1)
	if err != nil || prop.ValueLen == 0 || len(prop.Value) < 4 {
		return
	}
	w.desktop, w.deskOK = xgb.Get32(prop.Value), true
}

// readClass reads the class of WM_CLASS as GetWindowClass does
func (m *Model) readClass(id xproto.Window, w *modelWindow) {
	w.classOK = false
	prop, err := m.srv.property(id, xproto.AtomWmClass, xproto.AtomString, 0, ^uint32(0))
	if err != nil || prop.ValueLen == 0 {
		return
	}
	parts := strings.Split(string(prop.Value), "\x00")
	switch {
	case len(parts) >= 2:
		w.class, w.classOK = strings.ToLower(parts[1]), true
	case len(parts) >= 1:
		w.class, w.classOK = strings.ToLower(parts[0]), true
	}
}

// readIcon reads the icon of _NET_WM_ICON by its headers
func (m *Model) readIcon(id xproto.Window, w *modelWindow) {
	w.icon = nil
	if m.atoms.netWmIcon == 0 {
		return
	}
	read := func(offset, length uint32) ([]uint32, uint32, error) {
		prop, err := m.srv.property(id, m.atoms.netWmIcon, xproto.AtomCardinal, offset, length)
		if err != nil {
			return nil, 0, err
		}
		values := make([]uint32, 0, prop.ValueLen)
		for i := 0; i < int(prop.ValueLen) && 4*i+4 <= len(prop.Value); i++ {
			values = append(values, xgb.Get32(prop.Value[4*i:]))
		}
		return values, prop.BytesAfter / 4, nil
	}
	if icon, err := readIconByHeaders(read); err == nil && icon != nil {
		w.icon = icon
	}
}

// readHints reads the urgency hint of WM_HINTS as GetWindowUrgent does
func (m *Model) readHints(id xproto.Window, w *modelWindow) {
	w.hinted = false
	prop, err := m.srv.property(id, xproto.AtomWmHints, xproto.AtomWmHints, 0, 9)
	if err == nil && prop.ValueLen > 0 && len(prop.Value) >= 4 {
		const urgencyHint = 1 << 8
		w.hinted = xgb.Get32(prop.Value)&urgencyHint != 0
	}
}

// readNames reads _NET_DESKTOP_NAMES as GetDesktopNames does
func (m *Model) readNames() {
	m.names, m.namesOK = nil, false
	if m.atoms.desktopNames == 0 {
		return
	}
	typ := m.atoms.utf8String
	if typ == 0 {
		typ = xproto.AtomString
	}
	prop, err := m.srv.property(m.root, m.atoms.desktopNames, typ, 0, ^uint32(0))
	if err != nil || prop.ValueLen == 0 {
		return
	}
	for _, name := range strings.Split(string(prop.Value), "\x00") {
		if name != "" {
			m.names = append(m.names, name)
		}
	}
	m.namesOK = true
}

// readCurrent reads _NET_CURRENT_DESKTOP as GetCurrentDesktop does
func (m *Model) readCurrent() {
	m.currentOK = false
	if m.atoms.currentDesktop == 0 {
		return
	}
	prop, err := m.srv.property(m.root, m.atoms.currentDesktop, xproto.AtomCardinal, 0, 1)
	if err != nil || prop.ValueLen == 0 || len(prop.Value) < 4 {
		return
	}
	m.current, m.currentOK = xgb.Get32(prop.Value), true
}

// has reports whether the window's state holds the atom
func (w *modelWindow) has(atom xproto.Atom) bool {
	for _, s := range w.states {
		if s == atom {
			return true
		}
	}
	return false
}

// workspace is the name of a desktop as GetWindowWorkspaceName and the
// filter of GetWindowListFiltered make it
func (m *Model) workspace(desktop uint32) string {
	if !m.namesOK {
		return fmt.Sprintf("%d", desktop+1)
	}
	if int(desktop) < len(m.names) {
		return m.names[desktop]
	}
	return fmt.Sprintf("%d", desktop+1)
}

// build makes the list as GetWindowListFiltered does, from the model
func (m *Model) build(opts WindowFilterOptions) []WindowInfo {
	result := make([]WindowInfo, 0, len(m.order))
	for _, id := range m.order {
		w := m.windows[id]
		if w == nil || !w.nameOK {
			continue
		}
		if !opts.IgnoreSkipTaskbar && m.atoms.skipTaskbar != 0 && w.has(m.atoms.skipTaskbar) {
			continue
		}
		workspace := ""
		if w.deskOK {
			workspace = m.workspace(w.desktop)
		}
		icon := w.icon
		if icon == nil && w.classOK {
			icon = iconByClass(w.class)
		}
		urgent := w.hinted || m.atoms.netWmState != 0 && m.atoms.demandsAttention != 0 && w.has(m.atoms.demandsAttention)
		result = append(result, WindowInfo{ID: id, Name: w.name, Icon: icon, Workspace: workspace, Urgent: urgent})
	}

	if opts.Workspace != "all" {
		var current string
		if m.currentOK {
			current = m.workspace(m.current)
		}
		result = FilterWindowsByWorkspace(result, current, opts.Workspace)
	}
	if opts.SortMinimizedLast {
		var normal, minimized []WindowInfo
		for _, win := range result {
			if w := m.windows[win.ID]; w != nil && m.atoms.hidden != 0 && w.has(m.atoms.hidden) {
				minimized = append(minimized, win)
			} else {
				normal = append(normal, win)
			}
		}
		result = append(normal, minimized...)
	}
	return result
}
