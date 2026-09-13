package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drauzju/talos-htop/internal/model"
	"github.com/drauzju/talos-htop/internal/source"
)

func sampleSockets() []model.Socket {
	return []model.Socket{
		{Protocol: "tcp", LocalIP: "0.0.0.0", LocalPort: 6443, State: "LISTEN", PID: 200, Process: "kube-apiserver"},
		{Protocol: "tcp", LocalIP: "127.0.0.1", LocalPort: 2379, State: "LISTEN", PID: 210, Process: "etcd"},
		{Protocol: "udp", LocalIP: "0.0.0.0", LocalPort: 53, PID: 250, Process: "coredns"},
		{Protocol: "tcp", LocalIP: "10.0.0.5", LocalPort: 52344, RemoteIP: "10.0.0.5", RemotePort: 6443, State: "ESTABLISHED", PID: 130, Process: "kubelet"},
		{Protocol: "tcp", LocalIP: "10.0.0.5", LocalPort: 51876, RemoteIP: "10.0.0.5", RemotePort: 10250, State: "TIME_WAIT"},
	}
}

func TestListeningHeuristic(t *testing.T) {
	cases := []struct {
		s    model.Socket
		want bool
	}{
		{model.Socket{Protocol: "tcp", State: "LISTEN"}, true},
		{model.Socket{Protocol: "tcp", State: "ESTABLISHED", RemotePort: 6443}, false},
		{model.Socket{Protocol: "udp", State: "", RemotePort: 0}, true},      // unconnected datagram
		{model.Socket{Protocol: "udp", State: "", RemotePort: 40000}, false}, // connected datagram
		{model.Socket{Protocol: "tcp", State: "TIME_WAIT", RemotePort: 1}, false},
	}
	for i, c := range cases {
		if got := c.s.Listening(); got != c.want {
			t.Errorf("case %d: Listening()=%v want %v (%+v)", i, got, c.want, c.s)
		}
	}
}

func TestBuildSocketRowsListeningOnly(t *testing.T) {
	rows := buildSocketRows(sampleSockets(), "", true)
	// Listening: the two LISTEN tcp sockets + the unconnected udp socket.
	if len(rows) != 3 {
		t.Fatalf("listening-only should keep 3 sockets, got %d: %+v", len(rows), rows)
	}
	// Ordered by local port ascending: 53, 2379, 6443.
	wantPorts := []uint32{53, 2379, 6443}
	for i, w := range wantPorts {
		if rows[i].LocalPort != w {
			t.Errorf("row %d: port %d want %d", i, rows[i].LocalPort, w)
		}
	}
	// The ESTABLISHED and TIME_WAIT sockets must be excluded.
	for _, r := range rows {
		if r.State == "ESTABLISHED" || r.State == "TIME_WAIT" {
			t.Errorf("listening-only leaked a non-listening socket: %+v", r)
		}
	}
}

func TestBuildSocketRowsAllAndFilter(t *testing.T) {
	if got := len(buildSocketRows(sampleSockets(), "", false)); got != 5 {
		t.Fatalf("all-sockets should keep 5, got %d", got)
	}
	// Query filters across fields: "etcd" by program, "6443" by port.
	if rows := buildSocketRows(sampleSockets(), "etcd", false); len(rows) != 1 || rows[0].Process != "etcd" {
		t.Fatalf("filter etcd: %+v", rows)
	}
	// Port 6443 appears as a listening local port and as a remote port on the
	// established kubelet connection.
	rows := buildSocketRows(sampleSockets(), "6443", false)
	if len(rows) != 2 {
		t.Fatalf("filter 6443 should match 2 sockets, got %d: %+v", len(rows), rows)
	}
}

func TestBuildSocketRowsCollapsesReusePort(t *testing.T) {
	// Eight SO_REUSEPORT listeners on the same endpoint, differing only in
	// inode — exactly what cilium-envoy reports — plus one unrelated socket.
	socks := []model.Socket{
		{Protocol: "tcp", LocalIP: "127.0.0.1", LocalPort: 2379, State: "LISTEN", PID: 210, Process: "etcd"},
	}
	for i := 0; i < 8; i++ {
		socks = append(socks, model.Socket{
			Protocol: "tcp", LocalIP: "0.0.0.0", LocalPort: 9964, State: "LISTEN",
			PID: 3606, Process: "cilium-envoy", Inode: uint64(18590 + i),
			RxQueue: uint64(i), // the row must keep the largest queue of the set
		})
	}

	rows := buildSocketRows(socks, "", true)
	if len(rows) != 2 {
		t.Fatalf("reuseport listeners should collapse to 2 rows, got %d: %+v", len(rows), rows)
	}
	etcd, envoy := rows[0], rows[1]
	if etcd.LocalPort != 2379 || etcd.Count != 1 {
		t.Errorf("single socket should stay a 1-count row: %+v", etcd)
	}
	if envoy.LocalPort != 9964 || envoy.Count != 8 {
		t.Errorf("collapsed row: port=%d count=%d want 9964/8", envoy.LocalPort, envoy.Count)
	}
	if envoy.RxQueue != 7 {
		t.Errorf("collapsed row Recv-Q = %d, want the max of the set (7)", envoy.RxQueue)
	}
	if got := programLabel(envoy); got != "cilium-envoy ×8" {
		t.Errorf("programLabel = %q, want %q", got, "cilium-envoy ×8")
	}
	if got := programLabel(etcd); got != "etcd" {
		t.Errorf("programLabel = %q, want %q (no count for a single socket)", got, "etcd")
	}
}

func TestBuildSocketRowsKeepsDistinctConnections(t *testing.T) {
	// Connections that share a local endpoint but differ in the remote port are
	// distinct rows — collapsing must not swallow them.
	socks := []model.Socket{
		{Protocol: "tcp", LocalIP: "10.0.0.5", LocalPort: 6443, RemoteIP: "10.0.0.5", RemotePort: 41812, State: "ESTABLISHED", PID: 200, Process: "kube-apiserver"},
		{Protocol: "tcp", LocalIP: "10.0.0.5", LocalPort: 6443, RemoteIP: "10.0.0.5", RemotePort: 41816, State: "ESTABLISHED", PID: 200, Process: "kube-apiserver"},
	}
	if rows := buildSocketRows(socks, "", false); len(rows) != 2 {
		t.Fatalf("distinct connections must not collapse, got %d rows: %+v", len(rows), rows)
	}
}

func TestFormatAddr(t *testing.T) {
	cases := []struct {
		ip   string
		port uint32
		want string
	}{
		{"0.0.0.0", 6443, "0.0.0.0:6443"},
		{"0.0.0.0", 0, "0.0.0.0:*"},
		{"", 0, "*:*"},
		{"::", 50000, "[::]:50000"},
		{"fe80::1", 22, "[fe80::1]:22"},
	}
	for _, c := range cases {
		if got := formatAddr(c.ip, c.port); got != c.want {
			t.Errorf("formatAddr(%q,%d)=%q want %q", c.ip, c.port, got, c.want)
		}
	}
}

// driveNet builds a model, feeds it a snapshot and a socket poll, then switches
// to the network view (plus any extra keys) and returns the rendered frame.
func driveNet(t *testing.T, keys ...string) (Model, string) {
	t.Helper()
	src := source.NewMock(4)
	var m tea.Model = New(src, time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m, _ = m.Update(snapshotMsg{snap: src.Snapshot(context.Background())})
	socks, _ := src.Sockets(context.Background())
	m, _ = m.Update(socketsMsg{socks: socks})
	m, _ = m.Update(keyMsg("s")) // switch to the network view
	for _, k := range keys {
		m, _ = m.Update(keyMsg(k))
	}
	return m.(Model), stripANSI(m.View())
}

func TestNetViewRendersSockets(t *testing.T) {
	m, out := driveNet(t)
	if m.view != viewNet {
		t.Fatalf("expected network view active")
	}
	for _, want := range []string{
		"Proto", "Local Address", "Foreign Address", "State", "Program",
		"netstat",        // header box indicates the mode
		"kube-apiserver", // a listening program from the mock
		"0.0.0.0:6443",   // its local address
		"LISTEN",
		"Show:listening", // footer scope indicator
		"[::]:50000",     // an IPv6 listener (apid)
	} {
		if !strings.Contains(out, want) {
			t.Errorf("network view missing %q\n---\n%s", want, out)
		}
	}
	// Listening-only by default: an ESTABLISHED-only ephemeral port is hidden.
	if strings.Contains(out, ":52344") {
		t.Errorf("listening-only view should hide established-only sockets")
	}
}

func TestNetViewShowAllToggle(t *testing.T) {
	// Press l to switch to all sockets.
	m, out := driveNet(t, "l")
	if m.netListeningOnly {
		t.Fatalf("l should turn off listening-only")
	}
	if !strings.Contains(out, "ESTABLISHED") {
		t.Errorf("all-sockets view should include established connections\n%s", out)
	}
	if !strings.Contains(out, "Show:all") {
		t.Errorf("footer should show scope 'all'")
	}
}

func TestNetViewToggleBackToProc(t *testing.T) {
	// s in, s out.
	m, out := driveNet(t, "s")
	if m.view != viewProc {
		t.Fatalf("second toggle should return to the process view")
	}
	if !strings.Contains(out, "CPU%") || !strings.Contains(out, "kube-apiserver") {
		t.Errorf("process view should be back\n%s", out)
	}
}
