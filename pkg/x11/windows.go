package x11

import (
	"bufio"
	"fmt"
	"image"
	_ "image/png" // Register PNG decoder
	"os"
	"path/filepath"
	"strings"

	"github.com/jezek/xgb/xproto"
)

// WindowInfo contains information about a window
type WindowInfo struct {
	ID        xproto.Window
	Name      string
	Icon      image.Image // Window icon from _NET_WM_ICON
	Preview   image.Image // Window thumbnail (to be implemented)
	Workspace string      // Workspace name
}

// GetClientList retrieves the list of client windows via EWMH _NET_CLIENT_LIST
func (c *Connection) GetClientList() ([]xproto.Window, error) {
	// Get _NET_CLIENT_LIST atom
	atom, err := c.InternAtom("_NET_CLIENT_LIST", true)
	if err != nil {
		return nil, err
	}

	// Read property
	prop, err := xproto.GetProperty(c.Conn, false, c.Root,
		atom,              // property
		xproto.AtomWindow, // type
		0,                 // offset
		^uint32(0),        // length (maximum)
	).Reply()
	if err != nil {
		return nil, fmt.Errorf("failed to read _NET_CLIENT_LIST: %w", err)
	}

	if prop.ValueLen == 0 {
		return []xproto.Window{}, nil
	}

	// Convert bytes to []Window
	windows := make([]xproto.Window, prop.ValueLen)
	for i := uint32(0); i < prop.ValueLen; i++ {
		windows[i] = xproto.Window(
			uint32(prop.Value[i*4]) |
				uint32(prop.Value[i*4+1])<<8 |
				uint32(prop.Value[i*4+2])<<16 |
				uint32(prop.Value[i*4+3])<<24,
		)
	}
	return windows, nil
}

// GetWindowName retrieves window name via _NET_WM_NAME (UTF-8) or WM_NAME (fallback)
func (c *Connection) GetWindowName(window xproto.Window) (string, error) {
	// Try _NET_WM_NAME (UTF-8) first
	netWmName, err := c.InternAtom("_NET_WM_NAME", true)
	if err == nil {
		utf8String, _ := c.InternAtom("UTF8_STRING", true)
		prop, err := xproto.GetProperty(c.Conn, false, window,
			netWmName,
			utf8String,
			0,
			^uint32(0),
		).Reply()
		if err == nil && prop.ValueLen > 0 {
			return string(prop.Value), nil
		}
	}

	// Fallback: WM_NAME
	prop, err := xproto.GetProperty(c.Conn, false, window,
		xproto.AtomWmName,
		xproto.AtomString,
		0,
		^uint32(0),
	).Reply()
	if err != nil {
		return "", fmt.Errorf("failed to get window name: %w", err)
	}

	if prop.ValueLen == 0 {
		return fmt.Sprintf("<unnamed 0x%x>", window), nil
	}

	return string(prop.Value), nil
}

// GetWindowIcon retrieves window icon from _NET_WM_ICON property.
// Returns nil if the icon is not available.
// If multiple icons are available, returns the largest one or closest to 48x48.
func (c *Connection) GetWindowIcon(window xproto.Window) (image.Image, error) {
	// Get _NET_WM_ICON atom
	netWmIcon, err := c.InternAtom("_NET_WM_ICON", true)
	if err != nil {
		return nil, fmt.Errorf("failed to get _NET_WM_ICON atom: %w", err)
	}

	// Read property
	prop, err := xproto.GetProperty(c.Conn, false, window,
		netWmIcon,
		xproto.AtomCardinal,
		0,
		^uint32(0), // read all
	).Reply()
	if err != nil || prop.ValueLen == 0 {
		return nil, nil // no icon available
	}

	// Parse icon data
	// Format: width, height, ARGB pixels...
	data := make([]uint32, prop.ValueLen)
	for i := uint32(0); i < prop.ValueLen; i++ {
		data[i] = uint32(prop.Value[i*4]) |
			uint32(prop.Value[i*4+1])<<8 |
			uint32(prop.Value[i*4+2])<<16 |
			uint32(prop.Value[i*4+3])<<24
	}

	// Find the best icon (closest to 48x48 or largest available)
	bestIcon := findBestIcon(data)
	if bestIcon == nil {
		return nil, nil
	}

	return bestIcon, nil
}

// findBestIcon finds the best icon from _NET_WM_ICON data
// Prefers icons close to 48x48, or the largest available
func findBestIcon(data []uint32) image.Image {
	if len(data) < 2 {
		return nil
	}

	var bestImg image.Image
	var bestScore int
	targetSize := 48
	pos := 0

	// Parse all icons in the data
	for pos < len(data)-1 {
		width := int(data[pos])
		height := int(data[pos+1])

		if width <= 0 || height <= 0 || width > 512 || height > 512 {
			break // Invalid dimensions
		}

		pixelsNeeded := width * height
		if pos+2+pixelsNeeded > len(data) {
			break // Not enough data
		}

		// Calculate score (prefer icons close to target size)
		size := width
		if height > width {
			size = height
		}
		score := 0
		if size >= targetSize {
			// Prefer larger icons, but not too large
			score = 1000 - (size - targetSize)
		} else {
			// Smaller icons get lower score
			score = size * 10
		}

		// Create image if this is the best so far
		if bestImg == nil || score > bestScore {
			img := image.NewRGBA(image.Rect(0, 0, width, height))

			// Copy pixel data (ARGB format with premultiplied alpha)
			for y := 0; y < height; y++ {
				for x := 0; x < width; x++ {
					pixel := data[pos+2+y*width+x]
					a := uint8((pixel >> 24) & 0xFF)
					r := uint8((pixel >> 16) & 0xFF)
					g := uint8((pixel >> 8) & 0xFF)
					b := uint8(pixel & 0xFF)

					// Store RGBA directly
					offset := img.PixOffset(x, y)
					img.Pix[offset+0] = r
					img.Pix[offset+1] = g
					img.Pix[offset+2] = b
					img.Pix[offset+3] = a
				}
			}

			bestImg = img
			bestScore = score
		}

		// Move to next icon
		pos += 2 + pixelsNeeded
	}

	return bestImg
}

// GetWindowList retrieves list of windows with names
func (c *Connection) GetWindowList() ([]WindowInfo, error) {
	windows, err := c.GetClientList()
	if err != nil {
		return nil, err
	}

	result := make([]WindowInfo, 0, len(windows))
	for _, win := range windows {
		name, err := c.GetWindowName(win)
		if err != nil {
			// Skip windows for which we couldn't get the name
			continue
		}

		// Try to get window icon (ignore errors)
		icon, _ := c.GetWindowIcon(win)

		// If no icon from _NET_WM_ICON, try to find from WM_CLASS
		if icon == nil {
			if wmClass, err := c.GetWindowClass(win); err == nil {
				icon = findIconByClass(wmClass)
			}
		}

		// Get workspace name
		workspace := c.GetWindowWorkspaceName(win)

		result = append(result, WindowInfo{
			ID:        win,
			Name:      name,
			Icon:      icon,
			Workspace: workspace,
		})
	}

	return result, nil
}

// SortWindowsByMRU sorts a list of WindowInfo by MRU order.
// Windows not in the MRU list are placed at the end.
func SortWindowsByMRU(windows []WindowInfo, mruOrder []xproto.Window) []WindowInfo {
	// Create a map for quick lookup of MRU position
	mruPos := make(map[xproto.Window]int)
	for i, win := range mruOrder {
		mruPos[win] = i
	}

	// Sort windows by MRU position
	sorted := make([]WindowInfo, len(windows))
	copy(sorted, windows)

	// Custom sort: MRU windows first, then others
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			posI, inMRUI := mruPos[sorted[i].ID]
			posJ, inMRUJ := mruPos[sorted[j].ID]

			// Both in MRU: compare positions
			if inMRUI && inMRUJ {
				if posI > posJ {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			} else if !inMRUI && inMRUJ {
				// Only J in MRU: move J before I
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
			// If only I in MRU or neither in MRU: keep order
		}
	}

	return sorted
}

// ActivateWindow activates the specified window via _NET_ACTIVE_WINDOW
func (c *Connection) ActivateWindow(window xproto.Window) error {
	netActiveWindow, err := c.InternAtom("_NET_ACTIVE_WINDOW", false)
	if err != nil {
		return err
	}

	// Create ClientMessage event
	event := xproto.ClientMessageEvent{
		Format: 32,
		Window: window,
		Type:   netActiveWindow,
		Data: xproto.ClientMessageDataUnionData32New([]uint32{
			2, // source indication: 2 = pager
			0, // timestamp
			0, // requestor's currently active window
			0, 0,
		}),
	}

	// Send event to root window
	mask := xproto.EventMaskSubstructureRedirect | xproto.EventMaskSubstructureNotify
	return xproto.SendEventChecked(c.Conn, false, c.Root, uint32(mask), string(event.Bytes())).Check()
}

// GetWindowClass retrieves WM_CLASS property (instance, class)
func (c *Connection) GetWindowClass(window xproto.Window) (string, error) {
	prop, err := xproto.GetProperty(c.Conn, false, window,
		xproto.AtomWmClass,
		xproto.AtomString,
		0,
		^uint32(0),
	).Reply()
	if err != nil || prop.ValueLen == 0 {
		return "", fmt.Errorf("no WM_CLASS property")
	}

	// WM_CLASS format: "instance\0class\0"
	// We're interested in the class (second part)
	parts := strings.Split(string(prop.Value), "\x00")
	if len(parts) >= 2 {
		return strings.ToLower(parts[1]), nil
	}
	if len(parts) >= 1 {
		return strings.ToLower(parts[0]), nil
	}
	return "", fmt.Errorf("invalid WM_CLASS format")
}

// GetWindowDesktop retrieves the desktop/workspace ID for a window via _NET_WM_DESKTOP
func (c *Connection) GetWindowDesktop(window xproto.Window) (uint32, error) {
	netWmDesktop, err := c.InternAtom("_NET_WM_DESKTOP", true)
	if err != nil {
		return 0, fmt.Errorf("failed to get _NET_WM_DESKTOP atom: %w", err)
	}

	prop, err := xproto.GetProperty(c.Conn, false, window,
		netWmDesktop,
		xproto.AtomCardinal,
		0,
		1,
	).Reply()
	if err != nil || prop.ValueLen == 0 {
		return 0, fmt.Errorf("no _NET_WM_DESKTOP property")
	}

	desktop := uint32(prop.Value[0]) |
		uint32(prop.Value[1])<<8 |
		uint32(prop.Value[2])<<16 |
		uint32(prop.Value[3])<<24

	return desktop, nil
}

// GetDesktopNames retrieves the list of desktop/workspace names via _NET_DESKTOP_NAMES
func (c *Connection) GetDesktopNames() ([]string, error) {
	netDesktopNames, err := c.InternAtom("_NET_DESKTOP_NAMES", true)
	if err != nil {
		return nil, fmt.Errorf("failed to get _NET_DESKTOP_NAMES atom: %w", err)
	}

	utf8String, err := c.InternAtom("UTF8_STRING", true)
	if err != nil {
		utf8String = xproto.AtomString
	}

	prop, err := xproto.GetProperty(c.Conn, false, c.Root,
		netDesktopNames,
		utf8String,
		0,
		^uint32(0),
	).Reply()
	if err != nil || prop.ValueLen == 0 {
		return nil, fmt.Errorf("no _NET_DESKTOP_NAMES property")
	}

	// Names are null-terminated strings
	names := strings.Split(string(prop.Value), "\x00")
	// Remove empty strings
	result := make([]string, 0, len(names))
	for _, name := range names {
		if name != "" {
			result = append(result, name)
		}
	}

	return result, nil
}

// GetWindowWorkspaceName retrieves the workspace name for a window
func (c *Connection) GetWindowWorkspaceName(window xproto.Window) string {
	desktop, err := c.GetWindowDesktop(window)
	if err != nil {
		return ""
	}

	names, err := c.GetDesktopNames()
	if err != nil {
		// Fallback to desktop number if names are not available
		return fmt.Sprintf("%d", desktop+1)
	}

	if int(desktop) < len(names) {
		return names[desktop]
	}

	// Fallback to desktop number
	return fmt.Sprintf("%d", desktop+1)
}

// findIconByClass tries to find an icon file for the given WM_CLASS
// Uses the same logic as i3qws: searches desktop files and icon directories
func findIconByClass(wmClass string) image.Image {
	if wmClass == "" {
		return nil
	}

	// Get icon name from desktop files
	iconName := findIconNameFromDesktop(wmClass)
	if iconName == "" {
		// Fallback to window class as icon name
		iconName = strings.ToLower(wmClass)
	}

	// Search for icon file in standard directories
	return findIconFile(iconName)
}

// findIconNameFromDesktop searches desktop files for icon name
func findIconNameFromDesktop(windowClass string) string {
	dirs := []string{
		"/usr/share/applications",
		"/usr/local/share/applications",
	}

	// Add user local directory
	if home := os.Getenv("HOME"); home != "" {
		dirs = append(dirs, filepath.Join(home, ".local/share/applications"))
	}

	// Add XDG_DATA_DIRS
	if xdgDataDirs := os.Getenv("XDG_DATA_DIRS"); xdgDataDirs != "" {
		for _, dir := range strings.Split(xdgDataDirs, ":") {
			if dir != "" {
				dirs = append(dirs, filepath.Join(dir, "applications"))
			}
		}
	}

	windowClassLower := strings.ToLower(windowClass)

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".desktop") {
				continue
			}

			desktopFile := filepath.Join(dir, entry.Name())
			if iconName := parseDesktopFileForIcon(desktopFile, windowClass, windowClassLower); iconName != "" {
				return iconName
			}
		}
	}

	return ""
}

// parseDesktopFileForIcon parses a desktop file and returns icon name if matches
func parseDesktopFileForIcon(path, windowClass, windowClassLower string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	var icon, startupWMClass, name string
	inDesktopEntry := false
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check for [Desktop Entry] section
		if strings.HasPrefix(line, "[") {
			inDesktopEntry = line == "[Desktop Entry]"
			continue
		}

		if !inDesktopEntry {
			continue
		}

		// Parse key=value pairs
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "Icon":
			icon = value
		case "StartupWMClass":
			startupWMClass = value
		case "Name":
			name = value
		}

		// Early exit if we have all we need
		if icon != "" && startupWMClass != "" && name != "" {
			break
		}
	}

	if icon == "" {
		return ""
	}

	// Match by StartupWMClass first
	if startupWMClass != "" && strings.EqualFold(startupWMClass, windowClass) {
		return icon
	}

	// Match by Name
	if name != "" && strings.EqualFold(name, windowClass) {
		return icon
	}

	// Match by desktop filename (without .desktop)
	baseName := strings.TrimSuffix(filepath.Base(path), ".desktop")
	if strings.EqualFold(baseName, windowClass) || strings.Contains(strings.ToLower(baseName), windowClassLower) {
		return icon
	}

	return ""
}

// findIconFile searches for icon file in standard icon directories
func findIconFile(iconName string) image.Image {
	// Icon directories in order of preference
	iconDirs := []string{
		"/usr/share/icons/hicolor/128x128/apps",
		"/usr/share/icons/hicolor/64x64/apps",
		"/usr/share/icons/hicolor/48x48/apps",
		"/usr/share/pixmaps",
	}

	// Add user local icons
	if home := os.Getenv("HOME"); home != "" {
		iconDirs = append([]string{
			filepath.Join(home, ".local/share/icons/hicolor/128x128/apps"),
			filepath.Join(home, ".local/share/icons/hicolor/64x64/apps"),
			filepath.Join(home, ".local/share/icons/hicolor/48x48/apps"),
		}, iconDirs...)
	}

	// Try PNG first, then SVG
	extensions := []string{".png", ".svg", ".xpm"}

	for _, dir := range iconDirs {
		for _, ext := range extensions {
			iconPath := filepath.Join(dir, iconName+ext)
			if img, err := loadImageFile(iconPath); err == nil {
				return img
			}
		}
	}

	return nil
}

// loadImageFile loads an image from a file path
func loadImageFile(path string) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	img, _, err := image.Decode(file)
	return img, err
}
