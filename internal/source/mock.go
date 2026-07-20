package source

import (
	"context"
	"math"
	"math/rand"
	"time"

	"github.com/drauzju/talos-htop/internal/model"
)

// mockSource generates believable, moving data so the whole TUI can be run and
// verified without a real Talos cluster (`talos-htop --demo`). It models a
// small process tree, per-core CPU noise, and slowly drifting memory.
type mockSource struct {
	rng      *rand.Rand
	cores    int
	memTotal uint64
	boot     time.Time
	procs    []*mockProc
	tick     int
}

type mockProc struct {
	pid, ppid int32
	state     string
	threads   int32
	cmd       string
	base      float64 // baseline CPU load factor
	cpuTime   float64 // cumulative seconds, grows over time
	rss       uint64
}

// NewMock returns a synthetic Source with the given number of cores.
func NewMock(cores int) Source {
	if cores <= 0 {
		cores = 4
	}
	r := rand.New(rand.NewSource(42))
	m := &mockSource{
		rng:      r,
		cores:    cores,
		memTotal: 8 * 1024 * 1024 * 1024, // 8 GiB
		boot:     time.Now().Add(-73 * time.Hour),
	}
	m.seedProcs()
	return m
}

func (m *mockSource) seedProcs() {
	// A realistic-ish Talos process tree rooted at machined (pid 1).
	specs := []struct {
		pid, ppid int32
		cmd       string
		base      float64
		rssMB     uint64
		threads   int32
	}{
		{1, 0, "/sbin/init", 0.02, 24, 8},
		{100, 1, "/system/bin/machined", 0.08, 96, 14},
		{110, 100, "/system/bin/apid", 0.05, 48, 10},
		{120, 100, "/system/bin/trustd", 0.01, 20, 6},
		{130, 100, "/system/bin/kubelet --config=/etc/kubernetes/kubelet.yaml", 0.35, 320, 24},
		{140, 130, "containerd --config /etc/containerd/config.toml", 0.18, 210, 20},
		{200, 140, "kube-apiserver --advertise-address=10.0.0.5", 0.60, 780, 28},
		{210, 140, "etcd --data-dir=/var/lib/etcd", 0.45, 540, 18},
		{220, 140, "kube-controller-manager", 0.22, 260, 16},
		{230, 140, "kube-scheduler", 0.12, 180, 12},
		{240, 140, "kube-proxy --config=/var/lib/kube-proxy/config.yaml", 0.08, 90, 8},
		{250, 140, "coredns -conf /etc/coredns/Corefile", 0.06, 70, 8},
		{260, 140, "flanneld --ip-masq --kube-subnet-mgr", 0.05, 64, 7},
		{300, 140, "pause", 0.00, 4, 1},
		{310, 140, "metrics-server --secure-port=4443", 0.09, 120, 9},
		{320, 100, "/system/bin/dashboard", 0.03, 40, 5},
		{330, 130, "runc init", 0.01, 12, 2},
	}
	for _, s := range specs {
		state := "S"
		if s.base > 0.3 {
			state = "R"
		}
		m.procs = append(m.procs, &mockProc{
			pid: s.pid, ppid: s.ppid, state: state, threads: s.threads,
			cmd: s.cmd, base: s.base, rss: s.rssMB * 1024 * 1024,
		})
	}
}

func (m *mockSource) Node() string { return "demo (mock data)" }
func (m *mockSource) Close() error { return nil }

func (m *mockSource) Snapshot(ctx context.Context) model.Snapshot {
	m.tick++
	now := time.Now()
	// Interval used to advance cumulative CPU time; matches a ~2s refresh.
	const dt = 2.0

	snap := model.Snapshot{
		Node:     "demo",
		Hostname: "talos-demo-cp-1",
		Version:  "v1.13.6 (demo)",
		Uptime:   now.Sub(m.boot),
		Taken:    now,
	}

	// Per-core CPU: baseline + a wandering sine so meters visibly move.
	snap.CPU.PerCore = make([]float64, m.cores)
	var totalBusy float64
	for i := 0; i < m.cores; i++ {
		phase := float64(m.tick)/6 + float64(i)
		busy := 18 + 22*math.Sin(phase) + 10*m.rng.Float64()
		busy = clampPercent(busy)
		snap.CPU.PerCore[i] = busy
		totalBusy += busy
	}
	snap.CPU.Total = clampPercent(totalBusy / float64(m.cores))
	snap.LoadAvg = [3]float64{
		snap.CPU.Total / 100 * float64(m.cores),
		snap.CPU.Total / 100 * float64(m.cores) * 0.8,
		snap.CPU.Total / 100 * float64(m.cores) * 0.6,
	}

	// Memory: slowly drifting used fraction.
	usedFrac := 0.42 + 0.06*math.Sin(float64(m.tick)/9)
	used := uint64(float64(m.memTotal) * usedFrac)
	buffers := m.memTotal / 40
	cached := m.memTotal / 6
	snap.Mem = model.MemUsage{
		Total:     m.memTotal,
		Used:      used,
		Buffers:   buffers,
		Cached:    cached,
		Free:      m.memTotal - used - buffers - cached,
		Available: m.memTotal - used,
		SwapTotal: 0, // Talos typically runs without swap
	}

	snap.Proc = make([]model.Process, 0, len(m.procs))
	for _, p := range m.procs {
		// Advance cumulative CPU time by a noisy per-tick amount.
		load := p.base * (0.6 + 0.8*m.rng.Float64())
		p.cpuTime += load * dt
		// Wander RSS a little.
		p.rss = uint64(float64(p.rss) * (0.995 + 0.01*m.rng.Float64()))

		cpuPct := load * 100 // Irix-style: base 1.0 => ~100% of one core
		snap.Proc = append(snap.Proc, model.Process{
			PID:            p.pid,
			PPID:           p.ppid,
			State:          p.state,
			Threads:        p.threads,
			CPUPercent:     cpuPct,
			CPUTime:        p.cpuTime,
			ResidentMemory: p.rss,
			VirtualMemory:  p.rss * 3,
			MemPercent:     float64(p.rss) / float64(m.memTotal) * 100,
			Command:        firstWord(p.cmd),
			Args:           p.cmd,
			Executable:     firstWord(p.cmd),
		})
	}
	return snap
}

func firstWord(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			return s[:i]
		}
	}
	return s
}
