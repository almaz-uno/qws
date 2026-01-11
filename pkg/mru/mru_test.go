package mru

import (
	"sync"
	"testing"

	"github.com/jezek/xgb/xproto"
)

func TestNewMRUList(t *testing.T) {
	mru := NewMRUList()
	if mru == nil {
		t.Fatal("NewMRUList returned nil")
	}
	if mru.Len() != 0 {
		t.Errorf("New MRU list should be empty, got length %d", mru.Len())
	}
}

func TestTouch_NewWindow(t *testing.T) {
	mru := NewMRUList()
	windowID := xproto.Window(100)

	mru.Touch(windowID)

	if mru.Len() != 1 {
		t.Errorf("Expected length 1, got %d", mru.Len())
	}

	order := mru.GetOrder()
	if len(order) != 1 || order[0] != windowID {
		t.Errorf("Expected [%d], got %v", windowID, order)
	}
}

func TestTouch_MultipleWindows(t *testing.T) {
	mru := NewMRUList()
	w1 := xproto.Window(100)
	w2 := xproto.Window(200)
	w3 := xproto.Window(300)

	mru.Touch(w1)
	mru.Touch(w2)
	mru.Touch(w3)

	order := mru.GetOrder()
	expected := []xproto.Window{w3, w2, w1}

	if len(order) != len(expected) {
		t.Fatalf("Expected length %d, got %d", len(expected), len(order))
	}

	for i, w := range expected {
		if order[i] != w {
			t.Errorf("At index %d: expected %d, got %d", i, w, order[i])
		}
	}
}

func TestTouch_MovesToFront(t *testing.T) {
	mru := NewMRUList()
	w1 := xproto.Window(100)
	w2 := xproto.Window(200)
	w3 := xproto.Window(300)

	// Add windows in order
	mru.Touch(w1)
	mru.Touch(w2)
	mru.Touch(w3)

	// Touch w1 again - it should move to front
	mru.Touch(w1)

	order := mru.GetOrder()
	expected := []xproto.Window{w1, w3, w2}

	if len(order) != len(expected) {
		t.Fatalf("Expected length %d, got %d", len(expected), len(order))
	}

	for i, w := range expected {
		if order[i] != w {
			t.Errorf("At index %d: expected %d, got %d", i, w, order[i])
		}
	}

	// Verify length hasn't changed
	if mru.Len() != 3 {
		t.Errorf("Expected length 3 after moving to front, got %d", mru.Len())
	}
}

func TestRemove_ExistingWindow(t *testing.T) {
	mru := NewMRUList()
	w1 := xproto.Window(100)
	w2 := xproto.Window(200)
	w3 := xproto.Window(300)

	mru.Touch(w1)
	mru.Touch(w2)
	mru.Touch(w3)

	// Remove middle window
	mru.Remove(w2)

	if mru.Len() != 2 {
		t.Errorf("Expected length 2 after removal, got %d", mru.Len())
	}

	order := mru.GetOrder()
	expected := []xproto.Window{w3, w1}

	for i, w := range expected {
		if order[i] != w {
			t.Errorf("At index %d: expected %d, got %d", i, w, order[i])
		}
	}

	if mru.Contains(w2) {
		t.Error("Removed window should not be in the list")
	}
}

func TestRemove_NonExistingWindow(t *testing.T) {
	mru := NewMRUList()
	w1 := xproto.Window(100)
	w2 := xproto.Window(200)

	mru.Touch(w1)

	// Remove window that was never added
	mru.Remove(w2)

	if mru.Len() != 1 {
		t.Errorf("Expected length 1, got %d", mru.Len())
	}

	if !mru.Contains(w1) {
		t.Error("Original window should still be in the list")
	}
}

func TestContains(t *testing.T) {
	mru := NewMRUList()
	w1 := xproto.Window(100)
	w2 := xproto.Window(200)

	mru.Touch(w1)

	if !mru.Contains(w1) {
		t.Error("Contains should return true for added window")
	}

	if mru.Contains(w2) {
		t.Error("Contains should return false for non-added window")
	}
}

func TestGetOrder_ReturnsACopy(t *testing.T) {
	mru := NewMRUList()
	w1 := xproto.Window(100)
	w2 := xproto.Window(200)

	mru.Touch(w1)
	mru.Touch(w2)

	order1 := mru.GetOrder()
	order2 := mru.GetOrder()

	// Verify they're equal
	if len(order1) != len(order2) {
		t.Fatal("GetOrder returned different lengths")
	}

	for i := range order1 {
		if order1[i] != order2[i] {
			t.Errorf("Different values at index %d", i)
		}
	}

	// Modify the returned slice - should not affect the MRU list
	order1[0] = xproto.Window(999)

	order3 := mru.GetOrder()
	if order3[0] == xproto.Window(999) {
		t.Error("GetOrder should return a copy, not the internal slice")
	}
}

func TestClear(t *testing.T) {
	mru := NewMRUList()
	w1 := xproto.Window(100)
	w2 := xproto.Window(200)
	w3 := xproto.Window(300)

	mru.Touch(w1)
	mru.Touch(w2)
	mru.Touch(w3)

	mru.Clear()

	if mru.Len() != 0 {
		t.Errorf("Expected length 0 after Clear, got %d", mru.Len())
	}

	if mru.Contains(w1) || mru.Contains(w2) || mru.Contains(w3) {
		t.Error("No windows should be present after Clear")
	}
}

func TestLen(t *testing.T) {
	mru := NewMRUList()

	if mru.Len() != 0 {
		t.Errorf("Expected initial length 0, got %d", mru.Len())
	}

	for i := 1; i <= 5; i++ {
		mru.Touch(xproto.Window(i * 100))
		if mru.Len() != i {
			t.Errorf("After adding %d windows, expected length %d, got %d", i, i, mru.Len())
		}
	}

	mru.Remove(xproto.Window(300))
	if mru.Len() != 4 {
		t.Errorf("After removal, expected length 4, got %d", mru.Len())
	}
}

func TestConcurrentAccess(t *testing.T) {
	mru := NewMRUList()
	const numGoroutines = 10
	const numOperations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Multiple goroutines performing concurrent operations
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			baseWindow := xproto.Window(id * 1000)

			for j := 0; j < numOperations; j++ {
				windowID := baseWindow + xproto.Window(j%10)

				// Mix of operations
				switch j % 5 {
				case 0:
					mru.Touch(windowID)
				case 1:
					mru.Contains(windowID)
				case 2:
					mru.GetOrder()
				case 3:
					mru.Remove(windowID)
				case 4:
					mru.Len()
				}
			}
		}(i)
	}

	wg.Wait()

	// Just verify the list is still operational
	mru.Touch(xproto.Window(99999))
	if !mru.Contains(xproto.Window(99999)) {
		t.Error("MRU list should be operational after concurrent access")
	}
}

func TestTouch_SameWindowMultipleTimes(t *testing.T) {
	mru := NewMRUList()
	windowID := xproto.Window(100)

	// Touch the same window multiple times
	mru.Touch(windowID)
	mru.Touch(windowID)
	mru.Touch(windowID)

	if mru.Len() != 1 {
		t.Errorf("Expected length 1 after touching same window multiple times, got %d", mru.Len())
	}

	order := mru.GetOrder()
	if len(order) != 1 || order[0] != windowID {
		t.Errorf("Expected single window [%d], got %v", windowID, order)
	}
}

func TestRemove_FromEmptyList(t *testing.T) {
	mru := NewMRUList()
	windowID := xproto.Window(100)

	// Remove from empty list should not panic
	mru.Remove(windowID)

	if mru.Len() != 0 {
		t.Errorf("Expected length 0, got %d", mru.Len())
	}
}

func TestComplexScenario(t *testing.T) {
	mru := NewMRUList()

	// Simulate a realistic window switching scenario
	browser := xproto.Window(101)
	editor := xproto.Window(102)
	terminal := xproto.Window(103)
	fileManager := xproto.Window(104)

	// User opens windows in sequence
	mru.Touch(browser)
	mru.Touch(editor)
	mru.Touch(terminal)

	// User switches back to editor
	mru.Touch(editor)

	// Expected order: editor, terminal, browser
	order := mru.GetOrder()
	expected := []xproto.Window{editor, terminal, browser}
	if !slicesEqual(order, expected) {
		t.Errorf("After switching, expected %v, got %v", expected, order)
	}

	// User opens file manager
	mru.Touch(fileManager)

	// User closes terminal
	mru.Remove(terminal)

	// Expected order: fileManager, editor, browser
	order = mru.GetOrder()
	expected = []xproto.Window{fileManager, editor, browser}
	if !slicesEqual(order, expected) {
		t.Errorf("After closing terminal, expected %v, got %v", expected, order)
	}

	if mru.Len() != 3 {
		t.Errorf("Expected 3 windows, got %d", mru.Len())
	}
}

// Helper function to compare window slices
func slicesEqual(a, b []xproto.Window) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
