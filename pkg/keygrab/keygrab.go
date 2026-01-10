package keygrab

import (
	"fmt"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// Key modifiers
const (
	ModShift    = xproto.ModMaskShift
	ModCapsLock = xproto.ModMaskLock
	ModControl  = xproto.ModMaskControl
	ModAlt      = xproto.ModMask1 // Mod1
	ModNumLock  = xproto.ModMask2 // Mod2
	Mod3        = xproto.ModMask3 // Usually unused
	ModSuper    = xproto.ModMask4 // Mod4 - Win/Super key
	Mod5        = xproto.ModMask5 // Usually ISO_Level3_Shift
)

// KeyGrabber captures key combinations
type KeyGrabber struct {
	conn *xgb.Conn
	root xproto.Window
}

// NewKeyGrabber creates a new key grabber
func NewKeyGrabber(conn *xgb.Conn, root xproto.Window) *KeyGrabber {
	return &KeyGrabber{
		conn: conn,
		root: root,
	}
}

// GrabAltTab captures Alt+Tab and Alt+Shift+Tab
func (kg *KeyGrabber) GrabAltTab() error {
	// Get keycode for Tab
	setup := xproto.Setup(kg.conn)
	tabKeycode := keysymToKeycode(kg.conn, setup, 0xFF09) // XK_Tab = 0xFF09

	if tabKeycode == 0 {
		return fmt.Errorf("failed to find keycode for Tab")
	}

	// Modifiers to ignore (NumLock, CapsLock)
	ignoreMods := []uint16{
		0,                                // no additional modifiers
		uint16(ModNumLock),               // NumLock
		uint16(ModCapsLock),              // CapsLock
		uint16(ModNumLock | ModCapsLock), // NumLock + CapsLock
	}

	// Capture Alt+Tab
	for _, mod := range ignoreMods {
		err := xproto.GrabKeyChecked(kg.conn,
			true,                 // owner_events
			kg.root,              // grab_window
			uint16(ModAlt)|mod,   // modifiers (Alt + ignore)
			tabKeycode,           // key
			xproto.GrabModeAsync, // pointer_mode
			xproto.GrabModeAsync, // keyboard_mode
		).Check()
		if err != nil {
			kg.UngrabAll()
			return fmt.Errorf("failed to capture Alt+Tab: %w", err)
		}
	}

	// Capture Alt+Shift+Tab
	for _, mod := range ignoreMods {
		err := xproto.GrabKeyChecked(kg.conn,
			true,                        // owner_events
			kg.root,                     // grab_window
			uint16(ModAlt|ModShift)|mod, // modifiers
			tabKeycode,                  // key
			xproto.GrabModeAsync,        // pointer_mode
			xproto.GrabModeAsync,        // keyboard_mode
		).Check()
		if err != nil {
			kg.UngrabAll()
			return fmt.Errorf("failed to capture Alt+Shift+Tab: %w", err)
		}
	}

	return nil
}

// UngrabAll releases all captured keys
func (kg *KeyGrabber) UngrabAll() {
	xproto.UngrabKey(kg.conn, xproto.GrabAny, kg.root, xproto.ModMaskAny)
}

// keysymToKeycode converts keysym to keycode
func keysymToKeycode(conn *xgb.Conn, setup *xproto.SetupInfo, keysym uint32) xproto.Keycode {
	mapping, err := xproto.GetKeyboardMapping(conn,
		setup.MinKeycode,
		byte(setup.MaxKeycode-setup.MinKeycode+1)).Reply()
	if err != nil {
		return 0
	}

	for keycode := setup.MinKeycode; keycode <= setup.MaxKeycode; keycode++ {
		for i := byte(0); i < mapping.KeysymsPerKeycode; i++ {
			idx := int(keycode-setup.MinKeycode)*int(mapping.KeysymsPerKeycode) + int(i)
			if idx < len(mapping.Keysyms) && uint32(mapping.Keysyms[idx]) == keysym {
				return keycode
			}
		}
	}
	return 0
}
