package ui

import (
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// Criteria of specs/031-animation-auto

// TestTCPLines checks K3: a line of /proc/net/tcp or tcp6 gives its local
// port and its state, of an IPv4 or an IPv6 address; the header and lines
// it cannot read give nothing; a table gives the first connection
// established on a port of the list, not one listening, closing or on
// another port, nor one whose remote port is on the list
func TestTCPLines(t *testing.T) {
	const (
		header = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode"
		// [::1]:5900 ← [::1]:47302, as on ws2 (research)
		v6 = "   3: 00000000000000000000000001000000:170C 00000000000000000000000001000000:B8C6 01 00000000:00000000 00:00000000 00000000  1000        0 412345 1 0000000000000000 20 4 30 10 -1"
		// 127.0.0.1:5900 listening
		listening = "   0: 0100007F:170C 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 41234 1 0000000000000000 100 0 0 10 0"
		// 127.0.0.1:5901, established
		v4 = "   1: 0100007F:170D 0100007F:9C40 01 00000000:00000000 02:000A7D2B 00000000  1000        0 41235 2 0000000000000000 20 4 29 10 -1"
		// 127.0.0.1:40000 → 127.0.0.1:5900: the viewer's end, its local port not the server's
		client = "   2: 0100007F:9C40 0100007F:170C 01 00000000:00000000 02:000A7D2B 00000000  1000        0 41236 2 0000000000000000 20 4 29 10 -1"
		// 127.0.0.1:5900 closing
		closing = "   4: 0100007F:170C 0100007F:9C41 06 00000000:00000000 03:00001770 00000000     0        0 0 3 0000000000000000"
	)
	for _, c := range []struct {
		line  string
		port  int
		state uint8
		ok    bool
	}{
		{v6, 5900, 0x01, true},
		{listening, 5900, 0x0A, true},
		{v4, 5901, 0x01, true},
		{client, 40000, 0x01, true},
		{closing, 5900, 0x06, true},
		{header, 0, 0, false},
		{"", 0, 0, false},
		{"   5: 0100007F 0100007F:9C41 01", 0, 0, false},      // no port
		{"   5: 0100007F:XYZ0 0100007F:9C41 01", 0, 0, false}, // not hex
		{"   5: 01007F:170C 0100007F:9C41 01", 0, 0, false},   // an address of neither length
		{"   5: 0100007F:170C 0100007F:9C41 1G", 0, 0, false}, // a state not hex
	} {
		port, state, ok := parseTCPLine(c.line)
		if port != c.port || state != c.state || ok != c.ok {
			t.Errorf("%q: port %d, state %#x, %v; want %d, %#x, %v", c.line, port, state, ok, c.port, c.state, c.ok)
		}
	}

	table := strings.Join([]string{header, listening, client, closing, v4, v6}, "\n") + "\n"
	for _, c := range []struct {
		ports []int
		port  int
		ok    bool
	}{
		{[]int{5900}, 5900, true}, // of v6, not listening, the client or closing
		{[]int{5901, 5900}, 5901, true},
		{[]int{5902}, 0, false},
		{nil, 0, false},
	} {
		if port, ok := establishedOn(strings.NewReader(table), c.ports); port != c.port || ok != c.ok {
			t.Errorf("ports %v: %d, %v; want %d, %v", c.ports, port, ok, c.port, c.ok)
		}
	}
	if _, ok := establishedOn(strings.NewReader(strings.Join([]string{header, listening, client, closing}, "\n")), []int{5900}); ok {
		t.Error("a listening socket, a viewer's end or a closing one taken for a connection on 5900")
	}
}

// TestViewerConnected checks K3 on the tables of the system: a socket of the
// test listening on a free port of the list, on IPv4 and on IPv6, is no
// viewer; a client connected to it is seen; both closed, it is not
func TestViewerConnected(t *testing.T) {
	if _, err := os.Stat(tcpTables[0]); err != nil {
		t.Skipf("no %s: %v", tcpTables[0], err)
	}
	for _, c := range []struct{ network, addr string }{{"tcp4", "127.0.0.1:0"}, {"tcp6", "[::1]:0"}} {
		ln, err := net.Listen(c.network, c.addr)
		if err != nil {
			if c.network == "tcp6" {
				t.Logf("no IPv6 loopback: %v", err)
				continue
			}
			t.Fatal(err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ports := []int{port}
		if p, ok := viewerConnected(ports); ok {
			ln.Close()
			t.Fatalf("%s: listening on %d, a viewer seen on %d", c.network, port, p)
		}

		accepted := make(chan net.Conn, 1)
		go func() {
			conn, err := ln.Accept()
			if err != nil {
				close(accepted)
				return
			}
			accepted <- conn
		}()
		client, err := net.Dial(c.network, ln.Addr().String())
		if err != nil {
			ln.Close()
			t.Fatal(err)
		}
		server, ok := <-accepted
		if !ok {
			client.Close()
			ln.Close()
			t.Fatalf("%s: no connection accepted", c.network)
		}
		if p, ok := viewerConnected(ports); !ok || p != port {
			t.Errorf("%s: a client connected on %d: %d, %v", c.network, port, p, ok)
		}

		client.Close()
		server.Close()
		ln.Close()
		// The states change as the segments of the closes cross the loopback
		deadline := time.Now().Add(time.Second)
		for {
			_, ok := viewerConnected(ports)
			if !ok {
				break
			}
			if time.Now().After(deadline) {
				t.Errorf("%s: closed, a viewer still seen on %d", c.network, port)
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
}

// BenchmarkViewerConnected measures the look for a viewer on the tables of
// the system, for a port no connection is on (research of
// specs/031-animation-auto, "The look for a viewer")
func BenchmarkViewerConnected(b *testing.B) {
	if _, err := os.Stat(tcpTables[0]); err != nil {
		b.Skipf("no %s: %v", tcpTables[0], err)
	}
	for range b.N {
		viewerConnected([]int{1})
	}
}
