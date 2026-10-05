package x11

import (
	"cmp"
	"errors"
	"slices"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xcmisc"
	"github.com/jezek/xgb/xproto"
)

// The XIDs of every connection of NewConn are reused through XC-MISC
// (specs/024-xid-reuse). xgb 1.3.1 gives the XIDs of a connection in turn
// within its resource-id-mask — 2 097 151 of them on the X servers of X.Org —
// and once it has given the last one asks the range function of the
// connection for more: without one, NewId fails from then on, and a
// connection that lives long enough can make no resource. NewConn sets an
// xidRanges of the connection's own as its range function when the X server
// has XC-MISC.

// errNoXIDs: every XID of the connection names a resource
var errNoXIDs = errors.New("XC-MISC: every XID of the connection in use")

// errXIDsAhead: the X server's range holds no XID but those xgb gave last
var errXIDsAhead = errors.New("XC-MISC: no free XID of the connection but those given last")

// xidsAhead bounds the XIDs xgb has given that the X server may not have seen
// named when it answers GetXIDRange. xgb asks for a range right after it has
// put the last XID of the one before in the buffer of NewId, which holds 5:
// those, and the XIDs taken by NewId whose request has not been sent — one
// or two at a time on a connection of qws, each named by the request that
// follows its NewId (specs/024-xid-reuse, research, "XIDs in flight"). Free
// to the X server, they may be in its range, and xgb would give them again
// while they name a resource.
const xidsAhead = 64

// xidSpan is a span of XIDs, from first to last
type xidSpan struct{ first, last uint32 }

func (s xidSpan) len() uint32 { return s.last - s.first + 1 }

// xidRanges is the range function of a connection: the ranges of XC-MISC,
// less the last xidsAhead XIDs xgb gave. xgb calls it from the one goroutine
// that gives the connection's XIDs, a call at a time.
type xidRanges struct {
	given []xidSpan // the last xidsAhead XIDs given, as spans, in the order given
}

// newXIDRanges is the range function of a connection of the setup, whose
// first range xgb gives on its own: base | inc to base | mask
func newXIDRanges(setup *xproto.SetupInfo) *xidRanges {
	inc := setup.ResourceIdMask & -setup.ResourceIdMask
	r := &xidRanges{}
	r.gave(xidSpan{setup.ResourceIdBase | inc, setup.ResourceIdBase | setup.ResourceIdMask})
	return r
}

// next is GetXIDRange of XC-MISC, a range of XIDs of the connection that name
// no resource, less the XIDs given last: its longest part without them, or
// errXIDsAhead when it has none. xgb takes count XIDs from start, at the step
// of the lowest bit of the mask — 1 on the X servers of X.Org, whose mask
// begins at bit 0 — and asks again past them.
func (r *xidRanges) next(conn *xgb.Conn) (start, count uint32, err error) {
	reply, err := xcmisc.GetXIDRange(conn).Reply()
	if err != nil {
		return 0, 0, err
	}
	if start, count, err = xidRange(reply); err != nil {
		return 0, 0, err
	}
	span, ok := r.pick(xidSpan{start, start + count - 1})
	if !ok {
		return 0, 0, errXIDsAhead
	}
	r.gave(span)
	return span.first, span.len(), nil
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

// pick is the longest part of the free span that holds none of the XIDs given
// last, the lowest of the longest; false when the span holds no other
func (r *xidRanges) pick(free xidSpan) (xidSpan, bool) {
	given := slices.SortedFunc(slices.Values(r.given), func(a, b xidSpan) int {
		return cmp.Compare(a.first, b.first)
	})
	var best xidSpan
	found := false
	consider := func(s xidSpan) {
		if !found || s.len() > best.len() {
			best, found = s, true
		}
	}
	from := free.first
	for _, g := range given {
		if g.last < from || g.first > free.last {
			continue
		}
		if g.first > from {
			consider(xidSpan{from, g.first - 1})
		}
		from = g.last + 1 // an XID is below 1 << 29
	}
	if from <= free.last {
		consider(xidSpan{from, free.last})
	}
	return best, found
}

// gave records the span xgb is to give, after those before, and keeps the last
// xidsAhead XIDs
func (r *xidRanges) gave(s xidSpan) {
	given := append(r.given, s)
	var n uint32
	for i := len(given) - 1; i >= 0; i-- {
		if l := given[i].len(); n+l >= xidsAhead {
			given[i].first = given[i].last - (xidsAhead - n) + 1
			r.given = slices.Clone(given[i:])
			return
		}
		n += given[i].len()
	}
	r.given = given
}
