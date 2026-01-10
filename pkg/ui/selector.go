package ui

import (
	"fmt"

	"github.com/almaz-uno/qws/pkg/x11"
)

// Selector provides a text interface for window selection
type Selector struct {
	windows       []x11.WindowInfo
	selectedIndex int
}

// NewSelector creates a new window selector
func NewSelector(windows []x11.WindowInfo) *Selector {
	return &Selector{
		windows:       windows,
		selectedIndex: 0,
	}
}

// UpdateWindows updates the window list, preserving the index
func (s *Selector) UpdateWindows(windows []x11.WindowInfo) {
	s.windows = windows
	// Reset index if it's out of bounds of the new list
	if s.selectedIndex >= len(windows) {
		s.selectedIndex = 0
	}
}

// Show displays the window list and automatically selects the next one
// In Phase 1 we use simple logic: cycle through windows
func (s *Selector) Show() (*x11.WindowInfo, error) {
	if len(s.windows) == 0 {
		return nil, fmt.Errorf("no available windows")
	}

	// Display window list to console (first 10 for readability)
	fmt.Println("\n┌─────────────────────────────────────────────────────┐")
	fmt.Println("│           Available windows:                        │")
	fmt.Println("└─────────────────────────────────────────────────────┘")

	for i := 0; i < len(s.windows); i++ {
		win := s.windows[i]
		if i == s.selectedIndex {
			fmt.Printf("  → [%d] %s (0x%x)\n", i+1, win.Name, win.ID)
		} else {
			fmt.Printf("    [%d] %s (0x%x)\n", i+1, win.Name, win.ID)
		}
	}

	// In Phase 1: automatically select the next window (cyclically)
	// This behavior is like alttab without holding Alt
	selected := &s.windows[s.selectedIndex]

	// For the next Alt+Tab press move forward
	s.selectedIndex = (s.selectedIndex + 1) % len(s.windows)

	return selected, nil
}
