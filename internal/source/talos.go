package source

import (
	"context"
	"fmt"
	"time"

	machineapi "github.com/siderolabs/talos/pkg/machinery/api/machine"
	"github.com/siderolabs/talos/pkg/machinery/client"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/drauzju/talos-htop/internal/model"
)

// Config controls how the Talos client is constructed. Empty fields fall back
// to the standard talosctl resolution (TALOSCONFIG env or ~/.talos/config).
type Config struct {
	ConfigPath  string   // explicit talosconfig file; "" => default rules
	ContextName string   // context within the config; "" => config's current
	Endpoints   []string // override endpoint (apid) addresses
	Node        string   // node to target; "" => talk to the endpoint directly
}

// talosSource is a Source backed by the real Talos gRPC machine API. It keeps
// the previous CPU counters and per-process CPU times so cumulative counters
// can be turned into live percentages.
type talosSource struct {
	client *client.Client
	node   string

	// delta state from the previous poll
	prevTime     time.Time
	prevCPUTotal *cpuCounters
	prevCPUCore  []cpuCounters
	prevProcCPU  map[int32]float64
}

// NewTalos builds a Source connected to a Talos node using the standard
// talosconfig resolution plus any overrides in cfg.
func NewTalos(ctx context.Context, cfg Config) (Source, error) {
	opts := []client.OptionFunc{}
	if cfg.ConfigPath != "" {
		opts = append(opts, client.WithConfigFromFile(cfg.ConfigPath))
	} else {
		opts = append(opts, client.WithDefaultConfig())
	}
	if cfg.ContextName != "" {
		opts = append(opts, client.WithContextName(cfg.ContextName))
	}
	if len(cfg.Endpoints) > 0 {
		opts = append(opts, client.WithEndpoints(cfg.Endpoints...))
	}

	c, err := client.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Talos: %w", err)
	}

	return &talosSource{
		client:      c,
		node:        cfg.Node,
		prevProcCPU: map[int32]float64{},
	}, nil
}

func (s *talosSource) Node() string { return s.node }
func (s *talosSource) Close() error { return s.client.Close() }

// nodeCtx targets the configured node, if any. With no node set the request
// goes to the endpoint machine directly.
func (s *talosSource) nodeCtx(ctx context.Context) context.Context {
	if s.node == "" {
		return ctx
	}
	return client.WithNode(ctx, s.node)
}

func (s *talosSource) Snapshot(ctx context.Context) model.Snapshot {
	now := time.Now()
	nctx := s.nodeCtx(ctx)
	snap := model.Snapshot{Node: s.node, Taken: now}

	// Version and hostname are cheap and give the header its identity. A
	// failure here is not fatal to the rest of the snapshot.
	if vresp, err := s.client.Version(nctx); err == nil && len(vresp.Messages) > 0 {
		v := vresp.Messages[0]
		if v.Metadata != nil && v.Metadata.Hostname != "" {
			snap.Hostname = v.Metadata.Hostname
		}
		if v.Version != nil {
			snap.Version = v.Version.Tag
		}
	}

	// Processes.
	presp, err := s.client.Processes(nctx)
	if err != nil {
		snap.Err = fmt.Errorf("processes: %w", err)
		return snap
	}
	if len(presp.Messages) == 0 {
		snap.Err = fmt.Errorf("processes: empty response")
		return snap
	}
	pmsg := presp.Messages[0]
	if snap.Hostname == "" && pmsg.Metadata != nil {
		snap.Hostname = pmsg.Metadata.Hostname
	}

	dt := now.Sub(s.prevTime).Seconds()
	haveDelta := !s.prevTime.IsZero() && dt > 0

	// Memory first, so per-process MEM% has a denominator.
	var memTotalBytes uint64
	if mresp, err := s.client.Memory(nctx); err == nil && len(mresp.Messages) > 0 {
		if mi := mresp.Messages[0].Meminfo; mi != nil {
			snap.Mem = memUsageFromMemInfo(mi)
			memTotalBytes = snap.Mem.Total
		}
	}

	curProcCPU := make(map[int32]float64, len(pmsg.Processes))
	snap.Proc = make([]model.Process, 0, len(pmsg.Processes))
	for _, pi := range pmsg.Processes {
		p := model.Process{
			PID:            pi.Pid,
			PPID:           pi.Ppid,
			State:          pi.State,
			Threads:        pi.Threads,
			CPUTime:        pi.CpuTime,
			VirtualMemory:  pi.VirtualMemory,
			ResidentMemory: pi.ResidentMemory,
			Command:        pi.Command,
			Executable:     pi.Executable,
			Args:           pi.Args,
		}
		curProcCPU[pi.Pid] = pi.CpuTime
		if haveDelta {
			if prev, ok := s.prevProcCPU[pi.Pid]; ok {
				// Irix-style: fraction of a single core, ×100.
				p.CPUPercent = clampPercentUncapped((pi.CpuTime - prev) / dt * 100)
			}
		}
		if memTotalBytes > 0 {
			p.MemPercent = float64(pi.ResidentMemory) / float64(memTotalBytes) * 100
		}
		snap.Proc = append(snap.Proc, p)
	}

	// CPU total + per-core from SystemStat (cumulative seconds).
	if sresp, err := s.client.MachineClient.SystemStat(nctx, &emptypb.Empty{}); err == nil && len(sresp.Messages) > 0 {
		st := sresp.Messages[0]
		if st.BootTime > 0 {
			snap.Uptime = now.Sub(time.Unix(int64(st.BootTime), 0))
		}
		curTotal := toCounters(st.CpuTotal)
		curCores := make([]cpuCounters, len(st.Cpu))
		for i, c := range st.Cpu {
			curCores[i] = toCounters(c)
		}
		if haveDelta && s.prevCPUTotal != nil {
			snap.CPU.Total = cpuLoad(*s.prevCPUTotal, curTotal)
			snap.CPU.PerCore = make([]model.CPULoad, len(curCores))
			for i := range curCores {
				if i < len(s.prevCPUCore) {
					snap.CPU.PerCore[i] = cpuLoad(s.prevCPUCore[i], curCores[i])
				}
			}
		} else {
			// First poll: no delta yet, but expose the core count so the
			// header can size itself immediately.
			snap.CPU.PerCore = make([]model.CPULoad, len(curCores))
		}
		s.prevCPUTotal = &curTotal
		s.prevCPUCore = curCores
	}

	// Load average (best effort; not all platforms report it).
	if lresp, err := s.client.MachineClient.LoadAvg(nctx, &emptypb.Empty{}); err == nil && len(lresp.Messages) > 0 {
		l := lresp.Messages[0]
		snap.LoadAvg = [3]float64{l.Load1, l.Load5, l.Load15}
	}

	s.prevProcCPU = curProcCPU
	s.prevTime = now
	return snap
}

// Sockets polls the node's network sockets via the machine API's Netstat RPC,
// requesting TCP/UDP (v4 and v6) with process resolution — the equivalent of
// `netstat -tulpn` (the view filters listening sockets client-side, so all
// records are fetched and can be shown too).
func (s *talosSource) Sockets(ctx context.Context) ([]model.Socket, error) {
	nctx := s.nodeCtx(ctx)
	resp, err := s.client.Netstat(nctx, &machineapi.NetstatRequest{
		Filter:  machineapi.NetstatRequest_ALL,
		Feature: &machineapi.NetstatRequest_Feature{Pid: true},
		L4Proto: &machineapi.NetstatRequest_L4Proto{
			Tcp: true, Tcp6: true, Udp: true, Udp6: true,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("netstat: %w", err)
	}
	if len(resp.Messages) == 0 {
		return nil, nil
	}

	recs := resp.Messages[0].Connectrecord
	out := make([]model.Socket, 0, len(recs))
	for _, r := range recs {
		sock := model.Socket{
			Protocol:   r.L4Proto,
			LocalIP:    r.Localip,
			LocalPort:  r.Localport,
			RemoteIP:   r.Remoteip,
			RemotePort: r.Remoteport,
			RxQueue:    r.Rxqueue,
			TxQueue:    r.Txqueue,
			Inode:      r.Inode,
		}
		// Datagram sockets carry no meaningful connection state; netstat leaves
		// the State column blank for them, and the listening heuristic relies on
		// an empty state, so only fill it for stream protocols.
		if !isDatagram(r.L4Proto) {
			sock.State = tcpStateName(r.State)
		}
		if r.Process != nil {
			sock.PID = int32(r.Process.Pid)
			sock.Process = r.Process.Name
		}
		out = append(out, sock)
	}
	return out, nil
}

// isDatagram reports whether an l4proto label is a connectionless (UDP/UDP-Lite)
// protocol, which netstat shows without a State.
func isDatagram(proto string) bool {
	switch proto {
	case "udp", "udp6", "udplite", "udplite6", "raw", "raw6":
		return true
	}
	return false
}

// tcpStateName maps the protobuf connection-state enum to the conventional
// netstat state name (with the underscores the kernel/ss/netstat use).
func tcpStateName(s machineapi.ConnectRecord_State) string {
	switch s {
	case machineapi.ConnectRecord_ESTABLISHED:
		return "ESTABLISHED"
	case machineapi.ConnectRecord_SYN_SENT:
		return "SYN_SENT"
	case machineapi.ConnectRecord_SYN_RECV:
		return "SYN_RECV"
	case machineapi.ConnectRecord_FIN_WAIT1:
		return "FIN_WAIT1"
	case machineapi.ConnectRecord_FIN_WAIT2:
		return "FIN_WAIT2"
	case machineapi.ConnectRecord_TIME_WAIT:
		return "TIME_WAIT"
	case machineapi.ConnectRecord_CLOSE:
		return "CLOSE"
	case machineapi.ConnectRecord_CLOSEWAIT:
		return "CLOSE_WAIT"
	case machineapi.ConnectRecord_LASTACK:
		return "LAST_ACK"
	case machineapi.ConnectRecord_LISTEN:
		return "LISTEN"
	case machineapi.ConnectRecord_CLOSING:
		return "CLOSING"
	default:
		return ""
	}
}

// memUsageFromMemInfo converts the kB-valued /proc/meminfo response into a
// byte-valued model.MemUsage, using htop's "used" definition.
func memUsageFromMemInfo(mi *machineapi.MemInfo) model.MemUsage {
	const kb = 1024
	total := mi.Memtotal * kb
	free := mi.Memfree * kb
	buffers := mi.Buffers * kb
	cached := mi.Cached * kb

	var used uint64
	if total > free+buffers+cached {
		used = total - free - buffers - cached
	}

	swapTotal := mi.Swaptotal * kb
	swapFree := mi.Swapfree * kb
	var swapUsed uint64
	if swapTotal > swapFree {
		swapUsed = swapTotal - swapFree
	}

	return model.MemUsage{
		Total:     total,
		Used:      used,
		Free:      free,
		Available: mi.Memavailable * kb,
		Buffers:   buffers,
		Cached:    cached,
		SwapTotal: swapTotal,
		SwapUsed:  swapUsed,
	}
}

func toCounters(c *machineapi.CPUStat) cpuCounters {
	if c == nil {
		return cpuCounters{}
	}
	return cpuCounters{
		User: c.User, Nice: c.Nice, System: c.System, Idle: c.Idle,
		Iowait: c.Iowait, Irq: c.Irq, SoftIrq: c.SoftIrq, Steal: c.Steal,
		Guest: c.Guest, GuestNice: c.GuestNice,
	}
}

// clampPercentUncapped floors at 0 but allows >100 (a multithreaded process can
// legitimately exceed one core, exactly as htop shows in Irix mode).
func clampPercentUncapped(v float64) float64 {
	if v < 0 {
		return 0
	}
	return v
}
