// Package model holds the pure domain types shared between the data sources
// and the UI. It has no dependency on Talos or on the terminal, so it can be
// tested in isolation and reused by alternative front-ends.
package model

import "time"

// Process is a single process as presented to the UI. CPU and memory
// percentages are already computed by the Source (from counter deltas), so the
// UI only formats and orders them.
type Process struct {
	PID     int32
	PPID    int32
	State   string // R, S, D, Z, T, I, ...
	Threads int32

	// CPUPercent is Irix-style: a single fully-busy core reads ~100%, a
	// process saturating two cores reads ~200%.
	CPUPercent float64
	// CPUTime is cumulative user+system CPU time in seconds.
	CPUTime float64

	VirtualMemory  uint64 // bytes
	ResidentMemory uint64 // bytes
	MemPercent     float64

	Command    string
	Executable string
	Args       string
}

// Name returns the best available human label for the process, mirroring how
// htop shows the command line, falling back to the executable path.
func (p Process) Name() string {
	switch {
	case p.Args != "":
		return p.Args
	case p.Command != "":
		return p.Command
	case p.Executable != "":
		return p.Executable
	default:
		return "?"
	}
}

// Socket is one network socket as presented to the network view, mirroring a
// `netstat -tulpn` row: the layer-4 protocol, the local and remote endpoints,
// the connection state, and the owning process where the API could resolve it.
type Socket struct {
	Protocol   string // tcp, tcp6, udp, udp6
	LocalIP    string
	LocalPort  uint32
	RemoteIP   string
	RemotePort uint32
	State      string // LISTEN, ESTABLISHED, …; "" for datagram sockets
	RxQueue    uint64 // bytes in the receive queue (Recv-Q)
	TxQueue    uint64 // bytes in the send queue (Send-Q)
	Inode      uint64
	PID        int32  // owning process, 0 when the API could not resolve it
	Process    string // owning program name, "" when unresolved

	// Count is how many sockets this row stands for. A process that binds a
	// port with SO_REUSEPORT (cilium-envoy, nginx, …) opens one socket per
	// worker, and the node reports each separately — identical in every field
	// but the inode. The view collapses those into a single row and records
	// the number here; 0 or 1 means the row is one socket.
	Count int
}

// Listening reports whether the socket is a server socket — the ones
// `netstat -l` (and `-tulpn`) keep. TCP sockets are listening in the LISTEN
// state; datagram (UDP) sockets carry no state, so an unconnected one (no
// remote peer) is treated as listening.
func (s Socket) Listening() bool {
	if s.State == "LISTEN" {
		return true
	}
	if s.State == "" && s.RemotePort == 0 {
		return true
	}
	return false
}

// CPULoad is one CPU's utilisation broken down into htop's meter categories,
// each expressed as a percentage of that CPU (they sum to Busy). This lets the
// meter draw the same coloured segments htop does rather than a flat bar.
type CPULoad struct {
	User   float64 // normal user processes (green)
	Nice   float64 // low-priority / niced (blue)
	System float64 // kernel (red)
	IRQ    float64 // irq + softirq (magenta)
	Other  float64 // steal + guest (cyan)
}

// Busy is the total non-idle percentage for the CPU.
func (c CPULoad) Busy() float64 {
	return c.User + c.Nice + c.System + c.IRQ + c.Other
}

// CPUUsage is the aggregate and per-core CPU utilisation for the node.
type CPUUsage struct {
	Total   CPULoad
	PerCore []CPULoad
}

// MemUsage is the node's memory and swap situation, all byte counts.
type MemUsage struct {
	Total     uint64
	Used      uint64 // total - free - buffers - cached (application memory)
	Free      uint64
	Available uint64
	Buffers   uint64
	Cached    uint64

	SwapTotal uint64
	SwapUsed  uint64
}

// UsedPercent is the used fraction of total memory in [0,100].
func (m MemUsage) UsedPercent() float64 {
	if m.Total == 0 {
		return 0
	}
	return float64(m.Used) / float64(m.Total) * 100
}

// SwapPercent is the used fraction of swap in [0,100].
func (m MemUsage) SwapPercent() float64 {
	if m.SwapTotal == 0 {
		return 0
	}
	return float64(m.SwapUsed) / float64(m.SwapTotal) * 100
}

// Snapshot is one complete poll of a node: everything the UI renders for a
// single frame. Err is set when the poll failed; the UI keeps showing the last
// good data and surfaces the error in the header.
type Snapshot struct {
	Node     string
	Hostname string
	Version  string
	Uptime   time.Duration
	LoadAvg  [3]float64 // 1, 5, 15 minute; zero if unavailable

	CPU  CPUUsage
	Mem  MemUsage
	Proc []Process

	Taken time.Time
	Err   error
}

// Running counts processes in the running state.
func (s Snapshot) Running() int {
	n := 0
	for _, p := range s.Proc {
		if p.State == "R" || p.State == "running" {
			n++
		}
	}
	return n
}

// Threads sums the thread counts across all processes.
func (s Snapshot) Threads() int {
	n := 0
	for _, p := range s.Proc {
		n += int(p.Threads)
	}
	return n
}
