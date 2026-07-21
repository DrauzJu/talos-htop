// Package source provides the data behind the UI. A Source is polled once per
// refresh and returns a fully-computed model.Snapshot. Two implementations
// exist: talosSource (real gRPC machine API) and mockSource (synthetic data for
// offline development, `--demo`).
package source

import (
	"context"

	"github.com/drauzju/talos-htop/internal/model"
)

// Source is one node's worth of pollable metrics.
type Source interface {
	// Snapshot performs a single poll and returns computed metrics. Transient
	// errors are reported via Snapshot.Err rather than returned, so the UI can
	// keep the previous frame on screen.
	Snapshot(ctx context.Context) model.Snapshot
	// Node returns the human label for the node this source targets.
	Node() string
	// Close releases any underlying connection.
	Close() error
}

// cpuLoad returns the per-category utilisation (0..100 each) implied by the
// delta between two cumulative CPUStat samples. This is the interval-independent
// method htop and talosctl dashboard use: each category is its share of the
// total CPU-time delta.
func cpuLoad(prev, cur cpuCounters) model.CPULoad {
	dTotal := cur.total() - prev.total()
	if dTotal <= 0 {
		return model.CPULoad{}
	}
	pct := func(now, before float64) float64 {
		return clampPercent((now - before) / dTotal * 100)
	}
	return model.CPULoad{
		User:   pct(cur.User, prev.User),
		Nice:   pct(cur.Nice, prev.Nice),
		System: pct(cur.System, prev.System),
		IRQ:    pct(cur.Irq+cur.SoftIrq, prev.Irq+prev.SoftIrq),
		Other:  pct(cur.Steal+cur.Guest+cur.GuestNice, prev.Steal+prev.Guest+prev.GuestNice),
	}
}

// cpuCounters mirrors the cumulative per-CPU seconds reported by the machine
// API, decoupled from the generated protobuf type so mockSource can produce it
// too.
type cpuCounters struct {
	User, Nice, System, Idle, Iowait, Irq, SoftIrq, Steal, Guest, GuestNice float64
}

func (c cpuCounters) total() float64 {
	return c.User + c.Nice + c.System + c.Idle + c.Iowait +
		c.Irq + c.SoftIrq + c.Steal + c.Guest + c.GuestNice
}

func clampPercent(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}
