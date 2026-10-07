package ui

import (
	"bufio"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
)

// A VNC viewer connected to the display (specs/031-animation-auto, D4): a
// TCP connection established on a local port the VNC server listens on —
// in /proc/net/tcp or /proc/net/tcp6, a line of that local port in state 01.
// No request to the X server.

// tcpTables are the tables of the TCP sockets of the network namespace
var tcpTables = []string{"/proc/net/tcp", "/proc/net/tcp6"}

// tcpEstablished is the state ESTABLISHED in the tables
const tcpEstablished = 0x01

// viewerConnected reports whether a TCP connection is established on a local
// port of ports, and the first such port found; a table it cannot read is
// skipped
func viewerConnected(ports []int) (int, bool) {
	for _, name := range tcpTables {
		f, err := os.Open(name)
		if err != nil {
			continue
		}
		port, ok := establishedOn(f, ports)
		f.Close()
		if ok {
			return port, true
		}
	}
	return 0, false
}

// establishedOn reads a table of /proc/net/tcp or tcp6 and reports the local
// port of the first connection established on a port of ports
func establishedOn(r io.Reader, ports []int) (int, bool) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		port, state, ok := parseTCPLine(sc.Text())
		if ok && state == tcpEstablished && slices.Contains(ports, port) {
			return port, true
		}
	}
	return 0, false
}

// parseTCPLine reads a line of /proc/net/tcp or tcp6 — "sl local_address
// rem_address st ...", the addresses in hex, the address and the port joined
// by a colon, IPv4 in 8 digits, IPv6 in 32 — and returns its local port and
// its state; false for the header or a line it cannot read
func parseTCPLine(line string) (port int, state uint8, ok bool) {
	f := strings.Fields(line)
	if len(f) < 4 || !strings.HasSuffix(f[0], ":") {
		return 0, 0, false
	}
	addr, hexPort, found := strings.Cut(f[1], ":")
	if !found || len(addr) != 8 && len(addr) != 32 {
		return 0, 0, false
	}
	p, err := strconv.ParseUint(hexPort, 16, 16)
	if err != nil {
		return 0, 0, false
	}
	st, err := strconv.ParseUint(f[3], 16, 8)
	if err != nil {
		return 0, 0, false
	}
	return int(p), uint8(st), true
}
