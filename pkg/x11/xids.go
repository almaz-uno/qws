package x11

import (
	"errors"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xcmisc"
)

// The XIDs of every connection of NewConn are reused through XC-MISC
// (specs/024-xid-reuse). xgb 1.3.1 gives the XIDs of a connection in turn
// within its resource-id-mask — 2 097 151 of them on the X servers of X.Org —
// and once it has given the last one asks the range function of the
// connection for more: without one, NewId fails from then on, and a
// connection that lives long enough can make no resource. NewConn sets
// nextXIDs as the range function of every connection when the X server has
// XC-MISC.

// errNoXIDs: every XID of the connection names a resource
var errNoXIDs = errors.New("XC-MISC: every XID of the connection in use")

// nextXIDs is the range function of a connection: GetXIDRange of XC-MISC,
// a range of XIDs of the connection that name no resource. xgb takes count
// XIDs from start, at the step of the lowest bit of the mask — 1 on the X
// servers of X.Org, whose mask begins at bit 0 — and asks again past them.
func nextXIDs(conn *xgb.Conn) (start, count uint32, err error) {
	reply, err := xcmisc.GetXIDRange(conn).Reply()
	if err != nil {
		return 0, 0, err
	}
	return xidRange(reply)
}

// xidRange is the range of a reply of GetXIDRange. The X server of X.Org
// answers start 0 and count 1 when no XID of the client is free
// (dix/resource.c, GetXIDRange), which xgb would take for a range of one XID,
// the base itself, in use.
func xidRange(reply *xcmisc.GetXIDRangeReply) (start, count uint32, err error) {
	if reply.StartId == 0 && reply.Count == 1 {
		return 0, 0, errNoXIDs
	}
	return reply.StartId, reply.Count, nil
}
