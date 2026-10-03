package x11

import (
	"errors"
	"testing"

	"github.com/jezek/xgb/xcmisc"
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
