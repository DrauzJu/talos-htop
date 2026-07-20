package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/drauzju/talos-htop/internal/model"
)

var (
	styleHeaderRow = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6")).Bold(true)
	styleSelected  = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("2"))
	styleDim       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleKey       = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("6"))
	styleErr       = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleCmd       = lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
)

// meterCols is the number of CPU-meter columns for the current width.
func (m Model) meterCols() int {
	if m.width >= 90 {
		return 2
	}
	return 1
}

// headerHeight is the number of rows the header block occupies. Kept in sync
// with renderHeader so scrolling math matches what is drawn.
func (m Model) headerHeight() int {
	cols := m.meterCols()
	cores := len(m.snap.CPU.PerCore)
	if cores == 0 {
		cores = 1
	}
	cpuRows := (cores + cols - 1) / cols
	memRows := 2
	if m.width >= 90 {
		memRows = 1 // Mem and Swp side by side
	}
	// title + cpu grid + mem/swap + summary + blank separator
	return 1 + cpuRows + memRows + 1 + 1
}

// listHeight is the number of process rows that fit on screen.
func (m Model) listHeight() int {
	h := m.height - m.headerHeight() - 2 // column header + footer
	if h < 1 {
		return 1
	}
	return h
}

func (m Model) View() string {
	if !m.loaded {
		return "Connecting to Talos node…"
	}
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteByte('\n')
	b.WriteString(m.renderColumns())
	b.WriteByte('\n')
	b.WriteString(m.renderRows())
	b.WriteString(m.renderFooter())
	return b.String()
}

func (m Model) renderHeader() string {
	var lines []string

	// Title line: identity + poll status.
	node := m.snap.Hostname
	if node == "" {
		node = m.snap.Node
	}
	if node == "" {
		node = "(endpoint)"
	}
	title := fmt.Sprintf("talos-htop  %s", lipgloss.NewStyle().Bold(true).Render(node))
	if m.snap.Version != "" {
		title += styleDim.Render("  " + m.snap.Version)
	}
	if m.lastErr != nil {
		title += "  " + styleErr.Render("[poll error: "+truncate(m.lastErr.Error(), 40)+"]")
	}
	lines = append(lines, title)

	// CPU meters in a grid.
	cols := m.meterCols()
	cellW := m.width/cols - 1
	if cellW < 20 {
		cellW = 20
	}
	cores := m.snap.CPU.PerCore
	var cpuLines []string
	for i := 0; i < len(cores); i += cols {
		var cells []string
		for c := 0; c < cols && i+c < len(cores); c++ {
			idx := i + c
			label := fmt.Sprintf("%2d", idx)
			cells = append(cells, cpuMeter(label, cores[idx], cellW))
		}
		cpuLines = append(cpuLines, strings.Join(cells, " "))
	}
	if len(cores) == 0 {
		cpuLines = append(cpuLines, cpuMeter("Cpu", m.snap.CPU.Total, cellW))
	}
	lines = append(lines, cpuLines...)

	// Memory + swap.
	if m.width >= 90 {
		mem := memMeter(m.snap.Mem, cellW)
		swp := swapMeter(m.snap.Mem, cellW)
		lines = append(lines, mem+" "+swp)
	} else {
		lines = append(lines, memMeter(m.snap.Mem, m.width-1))
		lines = append(lines, swapMeter(m.snap.Mem, m.width-1))
	}

	// Summary: tasks, load, uptime, CPU total.
	summary := fmt.Sprintf(
		"Tasks: %s, %s thr; %s running   Load: %s   Uptime: %s   CPU: %s",
		lipgloss.NewStyle().Bold(true).Render(itoaInt(len(m.snap.Proc))),
		itoaInt(m.snap.Threads()),
		lipgloss.NewStyle().Foreground(colGreen).Render(itoaInt(m.snap.Running())),
		loadString(m.snap.LoadAvg),
		formatUptime(m.snap.Uptime),
		fmt.Sprintf("%.1f%%", m.snap.CPU.Total),
	)
	lines = append(lines, styleDim.Render(truncate(summary, m.width)))

	lines = append(lines, "") // blank separator
	return strings.Join(lines, "\n")
}

// column widths (excluding the flexible Command column)
const (
	wPID  = 7
	wST   = 3
	wCPU  = 6
	wMEM  = 6
	wVIRT = 8
	wRES  = 8
	wTHR  = 5
	wTIME = 10
)

func (m Model) renderColumns() string {
	head := fmt.Sprintf("%*s %-*s %*s %*s %*s %*s %*s %*s %s",
		wPID, "PID", wST, "S", wCPU, "CPU%", wMEM, "MEM%",
		wVIRT, "VIRT", wRES, "RES", wTHR, "THR", wTIME, "TIME+", sortMarker(m)+"Command")
	head = padRight(head, m.width)
	return styleHeaderRow.Render(head)
}

func sortMarker(m Model) string {
	arrow := "▼"
	if !m.desc {
		arrow = "▲"
	}
	return "[" + m.sortKey.String() + arrow + "] "
}

func (m Model) renderRows() string {
	h := m.listHeight()
	var b strings.Builder
	if len(m.rows) == 0 {
		b.WriteString(styleDim.Render("  (no processes match)"))
		b.WriteByte('\n')
		for i := 1; i < h; i++ {
			b.WriteByte('\n')
		}
		return b.String()
	}

	end := m.offset + h
	if end > len(m.rows) {
		end = len(m.rows)
	}
	drawn := 0
	for i := m.offset; i < end; i++ {
		b.WriteString(m.renderRow(m.rows[i], i == m.cursor))
		b.WriteByte('\n')
		drawn++
	}
	for ; drawn < h; drawn++ { // pad to a stable height
		b.WriteByte('\n')
	}
	return b.String()
}

func (m Model) renderRow(r row, selected bool) string {
	p := r.proc
	cmd := r.prefix + p.Name()

	fixed := fmt.Sprintf("%*d %-*s %*s %*s %*s %*s %*d %*s ",
		wPID, p.PID,
		wST, stateShort(p.State),
		wCPU, formatPercent(p.CPUPercent),
		wMEM, formatPercent(p.MemPercent),
		wVIRT, humanBytes(p.VirtualMemory),
		wRES, humanBytes(p.ResidentMemory),
		wTHR, p.Threads,
		wTIME, formatCPUTime(p.CPUTime),
	)

	cmdWidth := m.width - lipgloss.Width(fixed)
	if cmdWidth < 1 {
		cmdWidth = 1
	}
	cmd = truncate(cmd, cmdWidth)

	if selected {
		line := padRight(fixed+cmd, m.width)
		return styleSelected.Render(line)
	}

	// Colourise a few fields when not selected.
	colored := colorField(fixed, p)
	return colored + styleCmd.Render(cmd)
}

// colorField re-colours the CPU%/MEM%/state portions of the pre-formatted fixed
// columns. It rebuilds the string to keep alignment intact.
func colorField(fixed string, p model.Process) string {
	// Recompose with colour rather than trying to patch the flat string.
	pid := lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Render(fmt.Sprintf("%*d", wPID, p.PID))
	st := stateStyle(p.State).Render(fmt.Sprintf(" %-*s", wST, stateShort(p.State)))
	cpu := lipgloss.NewStyle().Foreground(loadColor(p.CPUPercent)).Render(fmt.Sprintf(" %*s", wCPU, formatPercent(p.CPUPercent)))
	mem := lipgloss.NewStyle().Foreground(loadColor(p.MemPercent)).Render(fmt.Sprintf(" %*s", wMEM, formatPercent(p.MemPercent)))
	rest := fmt.Sprintf(" %*s %*s %*d %*s ",
		wVIRT, humanBytes(p.VirtualMemory),
		wRES, humanBytes(p.ResidentMemory),
		wTHR, p.Threads,
		wTIME, formatCPUTime(p.CPUTime),
	)
	return pid + st + cpu + mem + styleDim.Render(rest)
}

func (m Model) renderFooter() string {
	if m.searching {
		return m.search.View()
	}
	var hint string
	if m.showHelp {
		hint = "↑↓/jk move  PgUp/PgDn page  g/G top/bottom  " +
			"t/F5 tree  p CPU  m MEM  n PID  T TIME  c CMD  i invert  / search  ? help  q quit"
	} else {
		treeState := "off"
		if m.tree {
			treeState = "on"
		}
		hint = fmt.Sprintf("Sort %s %s | Tree %s | %d procs | ? help  q quit",
			m.sortKey.String(), arrowFor(m.desc), treeState, len(m.rows))
		if m.query != "" {
			hint = "filter=\"" + m.query + "\" | " + hint
		}
	}
	return styleKey.Render(padRight(truncate(hint, m.width), m.width))
}

// --- small helpers ---

func stateShort(s string) string {
	if s == "" {
		return "?"
	}
	// Talos may report either single-letter codes or words like "running".
	switch s {
	case "running":
		return "R"
	case "sleeping":
		return "S"
	case "zombie":
		return "Z"
	}
	return string(s[0])
}

func stateStyle(s string) lipgloss.Style {
	switch stateShort(s) {
	case "R":
		return lipgloss.NewStyle().Foreground(colGreen).Bold(true)
	case "D", "Z":
		return lipgloss.NewStyle().Foreground(colRed)
	default:
		return styleDim
	}
}

func loadString(l [3]float64) string {
	if l == [3]float64{} {
		return "n/a"
	}
	return fmt.Sprintf("%.2f %.2f %.2f", l[0], l[1], l[2])
}

func arrowFor(desc bool) string {
	if desc {
		return "▼"
	}
	return "▲"
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	// Trim rune-safely to width, leaving room for an ellipsis.
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes)) > w {
		runes = runes[:len(runes)-1]
	}
	if len(runes) > 0 {
		runes[len(runes)-1] = '…'
	}
	return string(runes)
}

func padRight(s string, w int) string {
	diff := w - lipgloss.Width(s)
	if diff <= 0 {
		return s
	}
	return s + strings.Repeat(" ", diff)
}

func itoaInt(v int) string {
	return fmt.Sprintf("%d", v)
}
