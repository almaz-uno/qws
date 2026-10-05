package x11

import (
	"errors"
	"testing"

	"github.com/jezek/xgb/xcmisc"
	"github.com/jezek/xgb/xproto"
)

// TestXIDRange checks criterion K3 of specs/024-xid-reuse: the answer of the X
// server of X.Org when no XID of the client is free, start 0 and count 1, is
// an error, not the XID 0 | base; any other range is xgb's as it is.
func TestXIDRange(t *testing.T) {
	if _, _, err := xidRange(&xcmisc.GetXIDRangeReply{StartId: 0, Count: 1}); !errors.Is(err, errNoXIDs) {
		t.Errorf("start 0, count 1: %v, want %v", err, errNoXIDs)
	}
	// A connection of E1 that holds the XIDs base + 1 to base + 3
	start, count, err := xidRange(&xcmisc.GetXIDRangeReply{StartId: 0x9c00004, Count: 2097148})
	if start != 0x9c00004 || count != 2097148 || err != nil {
		t.Errorf("start 0x9c00004, count 2097148: start 0x%x, count %d, %v", start, count, err)
	}
}

// TestXIDRangesAhead checks criterion K7 of specs/024-xid-reuse: the range
// given to xgb is the longest part of the X server's that holds none of the
// last 64 XIDs xgb gave, across the ranges given, small ones too; none when
// the X server's holds no other. The connection is one of E1, base 0x9c00000
// and mask 0x1fffff, whose first range xgb ends at 0x9dfffff.
func TestXIDRangesAhead(t *testing.T) {
	r := newXIDRanges(&xproto.SetupInfo{ResourceIdBase: 0x9c00000, ResourceIdMask: 0x1fffff})
	for i, step := range []struct {
		free, want xidSpan
		ok         bool
	}{
		// The whole space: the last 64 of the first range left out
		{xidSpan{0x9c00000, 0x9dfffff}, xidSpan{0x9c00000, 0x9dfffbf}, true},
		// Apart from the 64 given last, 0x9dfff80–0x9dfffbf: as it is
		{xidSpan{0x9c00010, 0x9c0001f}, xidSpan{0x9c00010, 0x9c0001f}, true},
		// Across them, the 48 of 0x9dfff90–0x9dfffbf left of the range before:
		// 144 below, 64 above
		{xidSpan{0x9dfff00, 0x9dfffff}, xidSpan{0x9dfff00, 0x9dfff8f}, true},
		// Within the 64 given last, 0x9dfff50–0x9dfff8f: none
		{xidSpan{0x9dfff60, 0x9dfff6f}, xidSpan{}, false},
		// A range of 10: the last 64 are 54 of the one before and these 10
		{xidSpan{0x9c00100, 0x9c00109}, xidSpan{0x9c00100, 0x9c00109}, true},
		// 0x9dfff50–0x9dfff59 were given before the last 64
		{xidSpan{0x9dfff50, 0x9dfff5f}, xidSpan{0x9dfff50, 0x9dfff59}, true},
		// The range of 10 is among the last 64 still: 16 below it, 6 above
		{xidSpan{0x9c000f0, 0x9c0010f}, xidSpan{0x9c000f0, 0x9c000ff}, true},
	} {
		got, ok := r.pick(step.free)
		if got != step.want || ok != step.ok {
			t.Fatalf("step %d, free 0x%x–0x%x: 0x%x–0x%x %v, want 0x%x–0x%x %v",
				i, step.free.first, step.free.last, got.first, got.last, ok, step.want.first, step.want.last, step.ok)
		}
		if ok {
			r.gave(got)
		}
		var n uint32
		for _, s := range r.given {
			n += s.len()
		}
		if n != xidsAhead {
			t.Fatalf("step %d: %d XIDs recorded as given last, want %d", i, n, xidsAhead)
		}
	}
}
