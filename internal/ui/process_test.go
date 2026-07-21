package ui

import (
	"strings"
	"testing"

	"github.com/drauzju/talos-htop/internal/model"
)

func sample() []model.Process {
	return []model.Process{
		{PID: 1, PPID: 0, CPUPercent: 1, ResidentMemory: 100, CPUTime: 5, Args: "init"},
		{PID: 100, PPID: 1, CPUPercent: 50, ResidentMemory: 500, CPUTime: 50, Args: "kubelet"},
		{PID: 200, PPID: 100, CPUPercent: 90, ResidentMemory: 900, CPUTime: 90, Args: "apiserver"},
		{PID: 300, PPID: 100, CPUPercent: 10, ResidentMemory: 300, CPUTime: 10, Args: "etcd"},
	}
}

func TestSortByCPUDescending(t *testing.T) {
	rows := buildRows(sample(), sortCPU, true, false)
	want := []int32{200, 100, 300, 1}
	for i, w := range want {
		if rows[i].proc.PID != w {
			t.Fatalf("row %d: got PID %d, want %d", i, rows[i].proc.PID, w)
		}
	}
}

func TestSortByPIDAscending(t *testing.T) {
	rows := buildRows(sample(), sortPID, false, false)
	want := []int32{1, 100, 200, 300}
	for i, w := range want {
		if rows[i].proc.PID != w {
			t.Fatalf("row %d: got PID %d, want %d", i, rows[i].proc.PID, w)
		}
	}
}

func TestSortByMemDescending(t *testing.T) {
	rows := buildRows(sample(), sortMem, true, false)
	if rows[0].proc.PID != 200 || rows[len(rows)-1].proc.PID != 1 {
		t.Fatalf("mem sort wrong: first=%d last=%d", rows[0].proc.PID, rows[len(rows)-1].proc.PID)
	}
}

func TestTreeStructure(t *testing.T) {
	rows := buildRows(sample(), sortCPU, true, true)
	// Expect root init first at depth 0, kubelet under it, apiserver+etcd under kubelet.
	byPID := map[int32]row{}
	for _, r := range rows {
		byPID[r.proc.PID] = r
	}
	if byPID[1].depth != 0 {
		t.Fatalf("init should be depth 0, got %d", byPID[1].depth)
	}
	if byPID[100].depth != 1 {
		t.Fatalf("kubelet should be depth 1, got %d", byPID[100].depth)
	}
	if byPID[200].depth != 2 || byPID[300].depth != 2 {
		t.Fatalf("children should be depth 2: apiserver=%d etcd=%d", byPID[200].depth, byPID[300].depth)
	}
	// Siblings are ordered by PID (sorting is disabled in tree view): apiserver
	// (PID 200) precedes etcd (PID 300).
	var iAPI, iETCD int
	for i, r := range rows {
		switch r.proc.PID {
		case 200:
			iAPI = i
		case 300:
			iETCD = i
		}
	}
	if iAPI > iETCD {
		t.Fatalf("apiserver (PID 200) should precede etcd (PID 300) in PID order")
	}
	// Tree rows carry connector glyphs.
	if !strings.Contains(byPID[200].prefix, "─") {
		t.Fatalf("expected tree connector in prefix, got %q", byPID[200].prefix)
	}
}

// TestTreeIgnoresSort verifies that the active sort key/direction has no effect
// in tree view: siblings stay in PID order regardless.
func TestTreeIgnoresSort(t *testing.T) {
	pidsOf := func(rows []row) []int32 {
		out := make([]int32, len(rows))
		for i, r := range rows {
			out[i] = r.proc.PID
		}
		return out
	}

	// Every sort key/direction must produce the identical tree layout.
	base := pidsOf(buildRows(sample(), sortPID, false, true))
	for _, key := range []sortKey{sortCPU, sortMem, sortPID, sortTime, sortName} {
		for _, desc := range []bool{true, false} {
			got := pidsOf(buildRows(sample(), key, desc, true))
			if len(got) != len(base) {
				t.Fatalf("row count changed for key=%v desc=%v", key, desc)
			}
			for i := range base {
				if got[i] != base[i] {
					t.Fatalf("tree order changed with sort key=%v desc=%v: %v vs %v", key, desc, got, base)
				}
			}
		}
	}

	// Concretely: sorting by CPU ascending must NOT reorder kubelet's children
	// (apiserver CPU 90 would come last under a real CPU-asc sort, but tree
	// keeps PID order so it stays before etcd).
	rows := buildRows(sample(), sortCPU, false, true)
	var iAPI, iETCD int
	for i, r := range rows {
		switch r.proc.PID {
		case 200:
			iAPI = i
		case 300:
			iETCD = i
		}
	}
	if iAPI > iETCD {
		t.Fatalf("tree must ignore CPU-asc sort; apiserver (PID 200) should stay before etcd (PID 300)")
	}
}

func TestTreeNoOrphansDropped(t *testing.T) {
	// A process whose parent is absent must still appear (promoted to root).
	procs := []model.Process{
		{PID: 500, PPID: 999, Args: "orphan"},
		{PID: 501, PPID: 500, Args: "child"},
	}
	rows := buildRows(procs, sortPID, false, true)
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
}

func TestFilter(t *testing.T) {
	got := filterProcs(sample(), "kube")
	if len(got) != 1 || got[0].PID != 100 {
		t.Fatalf("filter kube: %+v", got)
	}
	if len(filterProcs(sample(), "")) != 4 {
		t.Fatalf("empty filter should keep all")
	}
	// filter by pid substring
	if len(filterProcs(sample(), "200")) != 1 {
		t.Fatalf("pid filter failed")
	}
}
