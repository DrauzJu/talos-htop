package ui

import (
	"sort"
	"strings"

	"github.com/drauzju/talos-htop/internal/model"
)

// sortKey identifies the column processes are ordered by.
type sortKey int

const (
	sortCPU sortKey = iota
	sortMem
	sortPID
	sortTime
	sortName
)

func (k sortKey) String() string {
	switch k {
	case sortCPU:
		return "CPU%"
	case sortMem:
		return "MEM%"
	case sortPID:
		return "PID"
	case sortTime:
		return "TIME+"
	case sortName:
		return "Command"
	default:
		return "?"
	}
}

// row is one rendered line in the process list: the process plus, in tree mode,
// its depth and the connector prefix drawn to its left.
type row struct {
	proc   model.Process
	depth  int
	prefix string // tree connector glyphs; empty in flat mode
}

// less compares two processes by the active sort key. `out` is computed as the
// descending ordering (a before b when a's key is "greater"); desc==false flips
// it to ascending. Ties fall back to ascending PID, direction-independent, so
// rows never jitter between refreshes.
func less(a, b model.Process, key sortKey, desc bool) bool {
	var out bool
	switch key {
	case sortMem:
		if a.ResidentMemory == b.ResidentMemory {
			return a.PID < b.PID
		}
		out = a.ResidentMemory > b.ResidentMemory
	case sortPID:
		if a.PID == b.PID {
			return false
		}
		out = a.PID > b.PID
	case sortTime:
		if a.CPUTime == b.CPUTime {
			return a.PID < b.PID
		}
		out = a.CPUTime > b.CPUTime
	case sortName:
		an, bn := strings.ToLower(a.Name()), strings.ToLower(b.Name())
		if an == bn {
			return a.PID < b.PID
		}
		out = an > bn
	default: // sortCPU
		if a.CPUPercent == b.CPUPercent {
			return a.PID < b.PID
		}
		out = a.CPUPercent > b.CPUPercent
	}
	if desc {
		return out
	}
	return !out
}

// filterProcs keeps processes whose command/args/pid contains the query
// (case-insensitive). An empty query keeps everything.
func filterProcs(procs []model.Process, query string) []model.Process {
	if query == "" {
		return procs
	}
	q := strings.ToLower(query)
	out := procs[:0:0]
	for _, p := range procs {
		if strings.Contains(strings.ToLower(p.Name()), q) ||
			strings.Contains(strings.ToLower(p.Command), q) ||
			strings.Contains(itoa(p.PID), q) {
			out = append(out, p)
		}
	}
	return out
}

// buildRows turns a process slice into display rows honouring sort and tree
// settings. In flat mode it is a simple sorted list. In tree mode the column
// sort is intentionally disabled: the hierarchy is the ordering, so processes
// are nested under their PPID and each sibling group is shown in stable PID
// order regardless of the selected sort key.
func buildRows(procs []model.Process, key sortKey, desc, tree bool) []row {
	if !tree {
		sorted := append([]model.Process(nil), procs...)
		sort.SliceStable(sorted, func(i, j int) bool {
			return less(sorted[i], sorted[j], key, desc)
		})
		rows := make([]row, len(sorted))
		for i, p := range sorted {
			rows[i] = row{proc: p}
		}
		return rows
	}
	return buildTree(procs)
}

func buildTree(procs []model.Process) []row {
	children := map[int32][]model.Process{}
	present := map[int32]bool{}
	for _, p := range procs {
		present[p.PID] = true
	}
	for _, p := range procs {
		// A process whose parent is not in the set (or is itself) is treated as
		// a root, so nothing is dropped.
		parent := p.PPID
		if parent == p.PID || !present[parent] {
			parent = -1
		}
		children[parent] = append(children[parent], p)
	}
	// Sorting is disabled in tree view; order siblings by PID for a stable,
	// predictable layout that doesn't jitter as metrics change.
	for k := range children {
		siblings := children[k]
		sort.SliceStable(siblings, func(i, j int) bool {
			return siblings[i].PID < siblings[j].PID
		})
	}

	var rows []row
	var walk func(pid int32, depth int, ancestorsLast []bool)
	walk = func(parent int32, depth int, ancestorsLast []bool) {
		kids := children[parent]
		for i, p := range kids {
			last := i == len(kids)-1
			rows = append(rows, row{
				proc:   p,
				depth:  depth,
				prefix: treePrefix(ancestorsLast, last),
			})
			walk(p.PID, depth+1, append(append([]bool(nil), ancestorsLast...), last))
		}
	}
	walk(-1, 0, nil)
	return rows
}

// treePrefix draws the connector glyphs for a tree row given, for each
// ancestor level, whether that ancestor was the last of its siblings.
func treePrefix(ancestorsLast []bool, last bool) string {
	if len(ancestorsLast) == 0 && !last {
		// root-level, not implicitly last: keep it flush-left like htop.
	}
	var b strings.Builder
	for _, al := range ancestorsLast {
		if al {
			b.WriteString("  ")
		} else {
			b.WriteString("│ ")
		}
	}
	if last {
		b.WriteString("└─ ")
	} else {
		b.WriteString("├─ ")
	}
	return b.String()
}

func itoa(v int32) string {
	// small, allocation-light int32 -> string
	if v == 0 {
		return "0"
	}
	neg := v < 0
	var buf [12]byte
	i := len(buf)
	n := v
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
