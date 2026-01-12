package keygrab

import (
	_ "embed"
	"fmt"
	"strings"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"gopkg.in/yaml.v3"
)

//go:embed keysyms.yaml
var keysymsYAML []byte

// KeysymDB holds all keysym definitions
type KeysymDB struct {
	Navigation map[string]uint32 `yaml:"Navigation"`
	Editing    map[string]uint32 `yaml:"Editing"`
	Special    map[string]uint32 `yaml:"Special"`
	Function   map[string]uint32 `yaml:"Function"`
	Letters    map[string]uint32 `yaml:"Letters"`
	Numbers    map[string]uint32 `yaml:"Numbers"`
}

var (
	keysymDB   *KeysymDB
	keysymOnce sync.Once
)

// loadKeysyms loads keysym definitions from embedded YAML
func loadKeysyms() *KeysymDB {
	keysymOnce.Do(func() {
		keysymDB = &KeysymDB{}
		if err := yaml.Unmarshal(keysymsYAML, keysymDB); err != nil {
			// Fallback to empty maps if unmarshal fails
			keysymDB = &KeysymDB{
				Navigation: make(map[string]uint32),
				Editing:    make(map[string]uint32),
				Special:    make(map[string]uint32),
				Function:   make(map[string]uint32),
				Letters:    make(map[string]uint32),
				Numbers:    make(map[string]uint32),
			}
		}

		// Normalize all keys to lowercase for case-insensitive lookup
		keysymDB.Navigation = normalizeCaseMap(keysymDB.Navigation)
		keysymDB.Editing = normalizeCaseMap(keysymDB.Editing)
		keysymDB.Special = normalizeCaseMap(keysymDB.Special)
		keysymDB.Function = normalizeCaseMap(keysymDB.Function)
		keysymDB.Letters = normalizeCaseMap(keysymDB.Letters)
		keysymDB.Numbers = normalizeCaseMap(keysymDB.Numbers)
	})
	return keysymDB
}

// normalizeCaseMap converts all map keys to lowercase
func normalizeCaseMap(m map[string]uint32) map[string]uint32 {
	result := make(map[string]uint32, len(m))
	for k, v := range m {
		result[strings.ToLower(k)] = v
	}
	return result
}

// GetKeysymDB returns the global keysym database
func GetKeysymDB() *KeysymDB {
	return loadKeysyms()
}

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

// X11 keysyms for common keys
const (
	XK_Tab     = 0xFF09
	XK_grave   = 0x0060 // backtick/grave accent
	XK_space   = 0x0020
	XK_Escape  = 0xFF1B
	XK_Return  = 0xFF0D
	XK_Left    = 0xFF51
	XK_Up      = 0xFF52
	XK_Right   = 0xFF53
	XK_Down    = 0xFF54
	XK_a       = 0x0061
	XK_z       = 0x007A
	// Function keys
	XK_F1  = 0xFFBE
	XK_F2  = 0xFFBF
	XK_F3  = 0xFFC0
	XK_F4  = 0xFFC1
	XK_F5  = 0xFFC2
	XK_F6  = 0xFFC3
	XK_F7  = 0xFFC4
	XK_F8  = 0xFFC5
	XK_F9  = 0xFFC6
	XK_F10 = 0xFFC7
	XK_F11 = 0xFFC8
	XK_F12 = 0xFFC9
)

// KeyGrabber captures key combinations
type KeyGrabber struct {
	conn  *xgb.Conn
	root  xproto.Window
	setup *xproto.SetupInfo
}

// NewKeyGrabber creates a new key grabber
func NewKeyGrabber(conn *xgb.Conn, root xproto.Window) *KeyGrabber {
	return &KeyGrabber{
		conn:  conn,
		root:  root,
		setup: xproto.Setup(conn),
	}
}

// ParseModifier converts a modifier name to X11 modifier mask
// Supported values: Alt, Super, Ctrl, Control, Shift, Mod1, Mod2, Mod3, Mod4, Mod5
func ParseModifier(name string) (uint16, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "alt", "mod1":
		return uint16(ModAlt), nil
	case "super", "mod4", "win":
		return uint16(ModSuper), nil
	case "ctrl", "control":
		return uint16(ModControl), nil
	case "shift":
		return uint16(ModShift), nil
	case "mod2":
		return uint16(ModNumLock), nil
	case "mod3":
		return uint16(Mod3), nil
	case "mod5":
		return uint16(Mod5), nil
	default:
		return 0, fmt.Errorf("unknown modifier: %s", name)
	}
}

// ParseKey converts a key name to X11 keysym
// Supports X11 keysym names as shown by xev command
// Case-insensitive for convenience
func ParseKey(name string) (uint32, error) {
	keyName := strings.ToLower(strings.TrimSpace(name))

	db := loadKeysyms()

	// Search in all categories
	categories := []map[string]uint32{
		db.Navigation,
		db.Editing,
		db.Special,
		db.Function,
		db.Letters,
		db.Numbers,
	}

	for _, category := range categories {
		if keysym, ok := category[keyName]; ok {
			return keysym, nil
		}
	}

	return 0, fmt.Errorf("unknown key: %s (use --keysym-list to see all supported keys)", name)
}

// KeycodeFromKeysym converts keysym to keycode for this connection
func (kg *KeyGrabber) KeycodeFromKeysym(keysym uint32) (xproto.Keycode, error) {
	keycode := keysymToKeycode(kg.conn, kg.setup, keysym)
	if keycode == 0 {
		return 0, fmt.Errorf("failed to find keycode for keysym 0x%X", keysym)
	}
	return keycode, nil
}

// GrabKeys captures configured key combinations
// modifier: primary modifier (Alt, Super, Ctrl)
// key: main key (Tab, grave, space, etc.)
// backward: modifier for reverse direction (Shift)
// workspaceModifier: modifier for workspace filtering (Ctrl)
func (kg *KeyGrabber) GrabKeys(modifier, key, backward, workspaceModifier string) error {
	// Parse modifier
	modMask, err := ParseModifier(modifier)
	if err != nil {
		return fmt.Errorf("invalid modifier: %w", err)
	}

	// Parse backward modifier
	backwardMask, err := ParseModifier(backward)
	if err != nil {
		return fmt.Errorf("invalid backward modifier: %w", err)
	}

	// Parse workspace modifier
	workspaceMask, err := ParseModifier(workspaceModifier)
	if err != nil {
		return fmt.Errorf("invalid workspace modifier: %w", err)
	}

	// Parse key
	keysym, err := ParseKey(key)
	if err != nil {
		return fmt.Errorf("invalid key: %w", err)
	}

	// Get keycode for the key
	keycode, err := kg.KeycodeFromKeysym(keysym)
	if err != nil {
		return err
	}

	// Modifiers to ignore (NumLock, CapsLock)
	ignoreMods := []uint16{
		0,                                // no additional modifiers
		uint16(ModNumLock),               // NumLock
		uint16(ModCapsLock),              // CapsLock
		uint16(ModNumLock | ModCapsLock), // NumLock + CapsLock
	}

	// Capture: modifier+key
	for _, mod := range ignoreMods {
		err := xproto.GrabKeyChecked(kg.conn,
			true,                 // owner_events
			kg.root,              // grab_window
			modMask|mod,          // modifiers
			keycode,              // key
			xproto.GrabModeAsync, // pointer_mode
			xproto.GrabModeAsync, // keyboard_mode
		).Check()
		if err != nil {
			kg.UngrabAll()
			return fmt.Errorf("failed to grab %s+%s: %w", modifier, key, err)
		}
	}

	// Capture: modifier+backward+key (e.g., Alt+Shift+Tab)
	for _, mod := range ignoreMods {
		err := xproto.GrabKeyChecked(kg.conn,
			true,                       // owner_events
			kg.root,                    // grab_window
			modMask|backwardMask|mod,   // modifiers
			keycode,                    // key
			xproto.GrabModeAsync,       // pointer_mode
			xproto.GrabModeAsync,       // keyboard_mode
		).Check()
		if err != nil {
			kg.UngrabAll()
			return fmt.Errorf("failed to grab %s+%s+%s: %w", modifier, backward, key, err)
		}
	}

	// Capture: modifier+workspace+key (e.g., Alt+Ctrl+Tab)
	for _, mod := range ignoreMods {
		err := xproto.GrabKeyChecked(kg.conn,
			true,                       // owner_events
			kg.root,                    // grab_window
			modMask|workspaceMask|mod,  // modifiers
			keycode,                    // key
			xproto.GrabModeAsync,       // pointer_mode
			xproto.GrabModeAsync,       // keyboard_mode
		).Check()
		if err != nil {
			kg.UngrabAll()
			return fmt.Errorf("failed to grab %s+%s+%s: %w", modifier, workspaceModifier, key, err)
		}
	}

	// Capture: modifier+workspace+backward+key (e.g., Alt+Ctrl+Shift+Tab)
	for _, mod := range ignoreMods {
		err := xproto.GrabKeyChecked(kg.conn,
			true,                                 // owner_events
			kg.root,                              // grab_window
			modMask|workspaceMask|backwardMask|mod, // modifiers
			keycode,                              // key
			xproto.GrabModeAsync,                 // pointer_mode
			xproto.GrabModeAsync,                 // keyboard_mode
		).Check()
		if err != nil {
			kg.UngrabAll()
			return fmt.Errorf("failed to grab %s+%s+%s+%s: %w", modifier, workspaceModifier, backward, key, err)
		}
	}

	return nil
}

// GrabAltTab captures Alt+Tab and Alt+Shift+Tab (legacy method for backward compatibility)
func (kg *KeyGrabber) GrabAltTab() error {
	return kg.GrabKeys("Alt", "Tab", "Shift", "Ctrl")
}

// UngrabAll releases all captured keys
func (kg *KeyGrabber) UngrabAll() {
	xproto.UngrabKey(kg.conn, xproto.GrabAny, kg.root, xproto.ModMaskAny)
}

// GetModifierMask returns the X11 modifier mask for the given modifier name
func GetModifierMask(name string) (uint16, error) {
	return ParseModifier(name)
}

// GetKeysym returns the X11 keysym for the given key name
func GetKeysym(name string) (uint32, error) {
	return ParseKey(name)
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
