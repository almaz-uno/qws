package mru

import (
	"sync"

	"github.com/jezek/xgb/xproto"
)

// MRUList maintains a thread-safe list of windows in Most Recently Used order.
// Windows are ordered with the most recently active window at index 0.
type MRUList struct {
	mu    sync.RWMutex
	items []xproto.Window // [0] = most recently used
}

// NewMRUList creates a new empty MRU list.
func NewMRUList() *MRUList {
	return &MRUList{
		items: make([]xproto.Window, 0),
	}
}

// Touch moves the specified window to the front of the MRU list.
// This should be called whenever a window gains focus.
// If the window is not in the list, it will be added at the front.
func (m *MRUList) Touch(windowID xproto.Window) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find and remove if already exists
	for i, id := range m.items {
		if id == windowID {
			// Remove from current position
			m.items = append(m.items[:i], m.items[i+1:]...)
			break
		}
	}

	// Add to front
	m.items = append([]xproto.Window{windowID}, m.items...)
}

// Remove removes a window from the MRU list.
// This should be called when a window is closed.
func (m *MRUList) Remove(windowID xproto.Window) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i, id := range m.items {
		if id == windowID {
			m.items = append(m.items[:i], m.items[i+1:]...)
			return
		}
	}
}

// GetOrder returns a copy of the MRU list.
// The returned slice is ordered with the most recently used window first.
func (m *MRUList) GetOrder() []xproto.Window {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]xproto.Window, len(m.items))
	copy(result, m.items)
	return result
}

// Contains checks if a window is in the MRU list.
func (m *MRUList) Contains(windowID xproto.Window) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, id := range m.items {
		if id == windowID {
			return true
		}
	}
	return false
}

// Len returns the number of windows in the MRU list.
func (m *MRUList) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.items)
}

// Clear removes all windows from the MRU list.
func (m *MRUList) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = make([]xproto.Window, 0)
}

// GetCurrent returns the currently most recently used window (index 0).
// Returns 0 if the list is empty.
func (m *MRUList) GetCurrent() xproto.Window {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.items) > 0 {
		return m.items[0]
	}
	return 0
}
