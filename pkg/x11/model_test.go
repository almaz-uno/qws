package x11

import (
	"encoding/binary"
	"errors"
	"reflect"
	"testing"

	"github.com/jezek/xgb/xproto"
)

// fakeServer holds properties in memory: per window, per atom, the bytes and
// their format, as the X server keeps them
type fakeServer struct {
	props map[xproto.Window]map[xproto.Atom]fakeProp
}

type fakeProp struct {
	format byte // 8 or 32
	data   []byte
}

func (f *fakeServer) property(w xproto.Window, atom, typ xproto.Atom, offset, length uint32) (*xproto.GetPropertyReply, error) {
	props, ok := f.props[w]
	if !ok {
		return nil, errors.New("BadWindow")
	}
	p, ok := props[atom]
	if !ok {
		return &xproto.GetPropertyReply{}, nil
	}
	start := min(len(p.data), int(4*offset))
	end := len(p.data)
	if length != ^uint32(0) {
		end = min(len(p.data), start+int(4*length))
	}
	value := p.data[start:end]
	return &xproto.GetPropertyReply{
		Format: p.format, Value: value, ValueLen: uint32(len(value)) / uint32(p.format/8),
		BytesAfter: uint32(len(p.data) - end),
	}, nil
}

func (f *fakeServer) watch(w xproto.Window) error { return nil }

func (f *fakeServer) set(w xproto.Window, atom xproto.Atom, p fakeProp) {
	if f.props[w] == nil {
		f.props[w] = make(map[xproto.Atom]fakeProp)
	}
	f.props[w][atom] = p
}

func text(s string) fakeProp { return fakeProp{8, []byte(s)} }

func longs(values ...uint32) fakeProp {
	data := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(data[4*i:], v)
	}
	return fakeProp{32, data}
}

// Atoms of the fake server, above the predefined ones
var fakeAtoms = modelAtoms{
	clientList: 100, netWmName: 101, utf8String: 102, netWmState: 103, skipTaskbar: 104,
	hidden: 105, demandsAttention: 106, netWmDesktop: 107, desktopNames: 108,
	currentDesktop: 109, netWmIcon: 110,
}

func names(list []WindowInfo) []string {
	var out []string
	for _, w := range list {
		out = append(out, w.Name+"@"+w.Workspace)
	}
	return out
}

// TestModelFollows checks criterion K3 of specs/003-window-list: a change of
// each property, a new window and a window gone reach the next list
func TestModelFollows(t *testing.T) {
	const root, one, two, three xproto.Window = 1, 10, 20, 30
	a := fakeAtoms
	f := &fakeServer{props: make(map[xproto.Window]map[xproto.Atom]fakeProp)}
	f.set(root, a.clientList, longs(uint32(one), uint32(two)))
	f.set(root, a.desktopNames, text("web\x00code\x00"))
	f.set(root, a.currentDesktop, longs(0))
	f.set(one, a.netWmName, text("one"))
	f.set(one, a.netWmDesktop, longs(0))
	f.set(one, a.netWmIcon, longs(iconProperty([2]int{16, 16}, [2]int{48, 48})...))
	f.set(two, xproto.AtomWmName, text("two"))
	f.set(two, a.netWmDesktop, longs(1))

	m := newModel(f, root, a)
	all := WindowFilterOptions{Workspace: "all"}
	notify := func(w xproto.Window, atom xproto.Atom) {
		m.handle(xproto.PropertyNotifyEvent{Window: w, Atom: atom})
	}
	check := func(what string, opts WindowFilterOptions, want ...string) {
		t.Helper()
		if got := names(m.build(opts)); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", what, got, want)
		}
	}

	check("at the start", all, "one@web", "two@code")
	if icon := m.build(all)[0].Icon; icon == nil || icon.Bounds().Dx() != 48 {
		t.Errorf("icon of one: %v, want 48×48", bounds(icon))
	}

	f.set(one, a.netWmName, text("uno"))
	notify(one, a.netWmName)
	check("renamed", all, "uno@web", "two@code")

	f.set(two, a.netWmState, longs(uint32(a.skipTaskbar)))
	notify(two, a.netWmState)
	check("skip taskbar", all, "uno@web")
	check("skip taskbar ignored", WindowFilterOptions{Workspace: "all", IgnoreSkipTaskbar: true}, "uno@web", "two@code")

	f.set(three, a.netWmName, text("three"))
	f.set(three, a.netWmDesktop, longs(1))
	f.set(root, a.clientList, longs(uint32(three), uint32(one)))
	notify(root, a.clientList)
	check("a window new, one gone", all, "three@code", "uno@web")

	check("current workspace", WindowFilterOptions{Workspace: "current"}, "uno@web")
	f.set(root, a.currentDesktop, longs(1))
	notify(root, a.currentDesktop)
	check("workspace switched", WindowFilterOptions{Workspace: "current"}, "three@code")

	f.set(root, a.desktopNames, text("mail\x00chat\x00"))
	notify(root, a.desktopNames)
	check("workspaces renamed", all, "three@chat", "uno@mail")

	f.set(three, xproto.AtomWmHints, longs(1<<8, 0, 0, 0, 0, 0, 0, 0, 0))
	notify(three, xproto.AtomWmHints)
	if !m.build(all)[0].Urgent {
		t.Error("urgency hint not followed")
	}

	f.set(one, a.netWmIcon, longs(iconProperty([2]int{32, 32})...))
	notify(one, a.netWmIcon)
	if icon := m.build(all)[1].Icon; icon == nil || icon.Bounds().Dx() != 32 {
		t.Errorf("icon of one after a change: %v, want 32×32", bounds(icon))
	}

	f.set(three, a.netWmState, longs(uint32(a.hidden)))
	notify(three, a.netWmState)
	check("minimized last", WindowFilterOptions{Workspace: "all", SortMinimizedLast: true}, "uno@mail", "three@chat")
}

// TestModelMatchesQueries checks K1 on the windows of the display there is:
// for every filter, the model gives the list the queries of today give.
// Windows that change between two queries — a title being written — are
// left out of the comparison.
func TestModelMatchesQueries(t *testing.T) {
	c, err := Connect()
	if err != nil {
		t.Skipf("no X display: %v", err)
	}
	defer c.Close()
	m, err := NewModel()
	if err != nil {
		t.Skipf("no model: %v", err)
	}
	defer m.Close()

	for _, ws := range []string{"all", "current", "all-except-current"} {
		for _, skip := range []bool{false, true} {
			for _, minimized := range []bool{false, true} {
				opts := WindowFilterOptions{Workspace: ws, IgnoreSkipTaskbar: skip, SortMinimizedLast: minimized}
				before, err := c.GetWindowListFiltered(opts)
				if err != nil {
					t.Fatal(err)
				}
				got, err := m.List(opts)
				if err != nil {
					t.Fatal(err)
				}
				after, _ := c.GetWindowListFiltered(opts)
				if !reflect.DeepEqual(ids(before), ids(after)) {
					continue // the windows changed meanwhile
				}
				if !reflect.DeepEqual(ids(got), ids(after)) {
					t.Errorf("%+v: windows %v, want %v", opts, ids(got), ids(after))
					continue
				}
				for i := range got {
					w, b := after[i], before[i]
					if w.Name != b.Name {
						continue // a title being written
					}
					if got[i].Name != w.Name || got[i].Workspace != w.Workspace || got[i].Urgent != w.Urgent {
						t.Errorf("%+v: window 0x%x: %q@%q urgent %v, want %q@%q urgent %v", opts, w.ID,
							got[i].Name, got[i].Workspace, got[i].Urgent, w.Name, w.Workspace, w.Urgent)
					}
					if !sameIcon(got[i].Icon, w.Icon) {
						t.Errorf("%+v: window 0x%x: icon %v, want %v", opts, w.ID, bounds(got[i].Icon), bounds(w.Icon))
					}
				}
			}
		}
	}
}

func ids(list []WindowInfo) []xproto.Window {
	var out []xproto.Window
	for _, w := range list {
		out = append(out, w.ID)
	}
	return out
}
