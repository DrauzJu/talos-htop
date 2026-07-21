package ui

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drauzju/talos-htop/internal/source"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansi.ReplaceAllString(s, "") }

// drive feeds the model a window size and two snapshots (so CPU deltas exist in
// the real source; the mock produces movement on each poll) and returns the
// rendered, ANSI-stripped frame.
func drive(t *testing.T, keys ...string) (Model, string) {
	t.Helper()
	src := source.NewMock(4)
	var m tea.Model = New(src, time.Second)

	m, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	// Two polls to exercise delta paths.
	m, _ = m.Update(snapshotMsg{snap: src.Snapshot(context.Background())})
	m, _ = m.Update(snapshotMsg{snap: src.Snapshot(context.Background())})

	for _, k := range keys {
		m, _ = m.Update(keyMsg(k))
	}
	return m.(Model), stripANSI(m.View())
}

// keyMsg fabricates a tea.KeyMsg for a single-rune key or a named special key.
func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

func TestRenderContainsHeaderAndProcesses(t *testing.T) {
	_, out := drive(t)
	for _, want := range []string{
		"talos-htop",
		"talos-demo-cp-1", // hostname
		"PID", "CPU%", "MEM%", "RES", "TIME+", "Command",
		"Mem", "Swp",
		"kube-apiserver", // a process from the mock tree
		"Tasks", "Uptime",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered frame missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderTreeToggle(t *testing.T) {
	_, flat := drive(t)
	if strings.Contains(flat, "└─") || strings.Contains(flat, "├─") {
		t.Fatalf("flat view should not contain tree glyphs")
	}
	_, tree := drive(t, "t")
	if !strings.Contains(tree, "─") {
		t.Fatalf("tree view should contain connector glyphs:\n%s", tree)
	}
}

func TestRenderDefaultSortIsCPUDesc(t *testing.T) {
	m, out := drive(t)
	if m.sortKey != sortCPU || !m.desc {
		t.Fatalf("default sort should be CPU desc, got %v desc=%v", m.sortKey, m.desc)
	}
	// The highest-CPU process (kube-apiserver, base 0.60) should appear before
	// a low-CPU one (trustd, base 0.01) in the flat sorted output.
	if strings.Index(out, "kube-apiserver") > strings.Index(out, "trustd") {
		t.Errorf("expected kube-apiserver above trustd under CPU-desc sort")
	}
}

func TestSearchFiltersRows(t *testing.T) {
	// Open search, type "etcd".
	m, out := drive(t, "/", "e", "t", "c", "d")
	if !m.searching {
		t.Fatalf("expected search mode active")
	}
	if !strings.Contains(out, "etcd") {
		t.Fatalf("expected etcd in filtered output")
	}
	if strings.Contains(out, "kube-apiserver") {
		t.Fatalf("apiserver should be filtered out by query 'etcd'")
	}
}

func TestTreeModeDisablesSortKeys(t *testing.T) {
	// Enable tree, capture order, then hammer sort keys and invert.
	before, _ := drive(t, "t")
	pidsBefore := rowPIDs(before)

	after, out := drive(t, "t", "m", "p", "T", "c", "i")
	if !after.tree {
		t.Fatalf("tree should still be on")
	}
	if after.sortKey != sortCPU || !after.desc {
		t.Fatalf("sort key/direction must be untouched in tree mode, got %v desc=%v", after.sortKey, after.desc)
	}
	pidsAfter := rowPIDs(after)
	if len(pidsBefore) != len(pidsAfter) {
		t.Fatalf("row count changed")
	}
	for i := range pidsBefore {
		if pidsBefore[i] != pidsAfter[i] {
			t.Fatalf("tree order changed after sort keys: %v vs %v", pidsAfter, pidsBefore)
		}
	}
	if !strings.Contains(out, "Sort:disabled") {
		t.Fatalf("footer should show sorting disabled in tree view:\n%s", out)
	}
}

func rowPIDs(m Model) []int32 {
	out := make([]int32, len(m.rows))
	for i, r := range m.rows {
		out[i] = r.proc.PID
	}
	return out
}

func TestHelpPageShowsColorLegend(t *testing.T) {
	m, out := drive(t, "?")
	if !m.showHelp {
		t.Fatalf("expected help page to be open")
	}
	for _, want := range []string{
		"Help", "CPU meter", "Memory meter", "Keys",
		"user", "nice", "kernel", "irq", "idle", // CPU legend categories
		"buffers", "cache", // memory legend
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help page missing %q\n---\n%s", want, out)
		}
	}
	// The help page replaces the process list.
	if strings.Contains(out, "kube-apiserver") {
		t.Errorf("help page should not show the process list")
	}

	// Any key dismisses the help page.
	m2, out2 := drive(t, "?", "x")
	if m2.showHelp {
		t.Fatalf("expected help page to close after a keypress")
	}
	if !strings.Contains(out2, "kube-apiserver") {
		t.Errorf("expected process list back after closing help")
	}
}

func TestFooterHasHelpHint(t *testing.T) {
	_, out := drive(t)
	if !strings.Contains(out, "Help") {
		t.Errorf("footer should advertise the Help key")
	}
}

func TestQuitKey(t *testing.T) {
	src := source.NewMock(2)
	var m tea.Model = New(src, time.Second)
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	_, cmd := m.Update(keyMsg("q"))
	if cmd == nil {
		t.Fatalf("q should return a command (Quit)")
	}
	if msg := cmd(); msg == nil {
		t.Fatalf("expected quit message")
	}
}
