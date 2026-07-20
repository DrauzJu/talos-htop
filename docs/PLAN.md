# talos-htop — Design & Roadmap

`talos-htop` is an [htop](https://github.com/htop-dev/htop)-style interactive
process viewer for [Talos Linux](https://github.com/siderolabs/talos) nodes.

Talos is an API-managed OS with **no shell and no SSH**, so the classic htop
(which reads `/proc` locally) cannot run on a node. Instead, everything htop
needs is already exposed over the authenticated Talos **gRPC machine API** — the
same API `talosctl dashboard` uses. `talos-htop` is a client that renders that
data as a familiar full-screen TUI on your workstation.

## Why this is feasible

The Talos machine API and its COSI resource API expose exactly the primitives
htop relies on:

| htop needs            | Talos source                                                        |
| --------------------- | ------------------------------------------------------------------- |
| Process list          | `MachineService.Processes` → `ProcessInfo{pid,ppid,state,threads,cpu_time,virtual_memory,resident_memory,command,executable,args}` |
| Total & per-core CPU  | COSI resource `perf.CPU` (`CPUStats.perf.talos.dev`) → per-core + total `CPUStat{user,nice,system,idle,iowait,irq,softIrq,steal,guest,guestNice}` |
| Memory / swap         | `MachineService.Memory` → `MemInfo{memtotal,memfree,memavailable,buffers,cached,swaptotal,swapfree,...}` |
| Node identity/version | `MachineService.Version`, response `Metadata.hostname`              |

CPU utilisation (total, per-core, and per-process) is not reported directly —
these are cumulative counters, exactly like `/proc/stat` and `/proc/<pid>/stat`.
We compute percentages from the **delta between two successive snapshots**, which
is precisely how htop itself works.

## Architecture

```
main.go                         flag parsing, client construction, program start
internal/model/                 pure domain types (Snapshot, Process, CPUUsage, MemUsage)
internal/source/                data sources behind one interface
  source.go                       Source interface + percentage/delta computation
  talos.go                        real Talos gRPC + COSI implementation
  mock.go                         synthetic data source (--demo) for offline dev/testing
internal/ui/                     Bubble Tea TUI
  model.go                        Elm-style model: state, Update, key bindings, refresh loop
  view.go                         full-screen layout (header meters + process table)
  meters.go                       CPU/mem/swap meter bars + colouring (lipgloss)
  process.go                      sorting + tree construction + flattening for display
  format.go                       human-friendly byte/time/percent formatting
```

The UI never blocks on the network: a `tea.Tick` re-arms every refresh interval,
and each fetch runs inside a `tea.Cmd` goroutine that returns a `snapshotMsg`
(with any error). A slow or failing node degrades gracefully into a header error
line instead of freezing the UI.

### Data source abstraction

```go
type Source interface {
    Snapshot(ctx context.Context) model.Snapshot // one poll: procs + cpu + mem
    Node() string
    Close() error
}
```

`talosSource` holds the previous CPU counters and per-process CPU times plus the
timestamp, so it can turn cumulative counters into live percentages. `mockSource`
generates believable moving data so the whole TUI can be exercised without a
cluster (`talos-htop --demo`).

## Iteration 1 (this repo) — core features

- [x] Connect to a Talos node using the standard talosconfig (`--talosconfig`,
      `--context`, `--nodes`, `--endpoints`), same resolution as `talosctl`.
- [x] **Process list** with the essential htop columns: PID, PPID, STATE,
      THREADS, CPU%, MEM%, RES/VIRT, TIME+, Command.
- [x] **Tree view** (`t` / `F5`) — processes nested under their PPID with
      connector glyphs; toggles against the flat sorted view.
- [x] **Total & per-core CPU utilisation** as coloured meter bars in the header.
- [x] **Per-process CPU%** (Irix-style: a busy core = 100%) via cpu_time deltas.
- [x] **Memory & swap** meters (used/buffers/cache breakdown) + per-process MEM%.
- [x] Sorting by CPU%, MEM%, PID, TIME, and invert; live incremental search (`/`).
- [x] Header with hostname, Talos version, task/thread counts, uptime, load.
- [x] `--demo` mode so the UI runs and is verifiable without a cluster.

## Later iterations (not in scope here)

- Multi-node view / node switcher.
- Setup screen (F2): configurable meters, columns, colour schemes.
- Kill/signal (F9) and renice — gated on Talos API support & privileges.
- Container/cgroup grouping (Talos `Stats` API), per-service rollups.
- Column scrolling, tagging, saved layouts, mouse support.
