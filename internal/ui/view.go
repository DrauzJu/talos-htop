package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/drauzju/talos-htop/internal/model"
)

var (
	styleHeaderRow = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(colCyan).Bold(true)
	styleSelected  = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(colGreen).Bold(true)
	styleDim       = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	styleFooter    = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(colCyan)
	styleFnKey     = lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(colCyan).Bold(true)
	styleErr       = lipgloss.NewStyle().Foreground(colRed).Bold(true)
	styleCmd       = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	styleBorder    = lipgloss.NewStyle().Foreground(colBracket)
	styleTitle     = lipgloss.NewStyle().Foreground(colCyan).Bold(true)
	styleSep       = lipgloss.NewStyle().Foreground(colGray)
)

// twoColumn reports whether the meter header uses the side-by-side layout.
func (m Model) twoColumn() bool { return m.width >= 70 }

// headerContentRows is the number of content lines inside the meter box.
func (m Model) headerContentRows() int {
	cores := len(m.snap.CPU.PerCore)
	if cores == 0 {
		cores = 1
	}
	const rightLines = 5 // Mem, Swp, Tasks, Load, Uptime
	if m.twoColumn() {
		if cores > rightLines {
			return cores
		}
		return rightLines
	}
	return cores + rightLines
}

// headerHeight is the total rows the meter box occupies (content + 2 borders).
func (m Model) headerHeight() int { return m.headerContentRows() + 2 }

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
	b.WriteString(m.renderHeaderBox())
	b.WriteByte('\n')
	b.WriteString(m.renderColumns())
	b.WriteByte('\n')
	b.WriteString(m.renderRows())
	b.WriteString(m.renderFooter())
	return b.String()
}

// ---- meter header box -------------------------------------------------------

func (m Model) renderHeaderBox() string {
	contentW := m.width - 4 // "│ " + content + " │"
	if contentW < 10 {
		contentW = 10
	}

	// Build the two meter columns.
	var left, right []string
	cores := m.snap.CPU.PerCore

	if m.twoColumn() {
		leftW := contentW/2 - 1
		rightW := contentW - leftW - 2
		for i, c := range cores {
			left = append(left, cpuMeter(fmt.Sprintf("%2d", i), c, leftW))
		}
		if len(cores) == 0 {
			left = append(left, cpuMeter("Cpu", m.snap.CPU.Total, leftW))
		}
		right = m.textMeters(rightW)
		return m.box(m.joinColumns(left, right, leftW, rightW), contentW)
	}

	// Narrow: everything stacked in one column.
	for i, c := range cores {
		left = append(left, cpuMeter(fmt.Sprintf("%2d", i), c, contentW))
	}
	if len(cores) == 0 {
		left = append(left, cpuMeter("Cpu", m.snap.CPU.Total, contentW))
	}
	left = append(left, m.textMeters(contentW)...)
	return m.box(left, contentW)
}

// textMeters builds the right-hand column: Mem, Swp, and the text stats.
func (m Model) textMeters(w int) []string {
	tasks := fmt.Sprintf("%s Tasks, %s thr; %s running",
		styleTitle.Render(itoaInt(len(m.snap.Proc))),
		itoaInt(m.snap.Threads()),
		lipgloss.NewStyle().Foreground(colGreen).Render(itoaInt(m.snap.Running())))
	load := lipgloss.NewStyle().Foreground(colLabel).Bold(true).Render("Load ") +
		loadString(m.snap.LoadAvg)
	up := lipgloss.NewStyle().Foreground(colLabel).Bold(true).Render("Uptime ") +
		formatUptime(m.snap.Uptime)
	return []string{
		memMeter(m.snap.Mem, w),
		swapMeter(m.snap.Mem, w),
		padRight(tasks, w),
		padRight(load, w),
		padRight(up, w),
	}
}

// joinColumns places left and right meter lists side by side, padding each to
// its column width and the shorter list with blanks.
func (m Model) joinColumns(left, right []string, leftW, rightW int) []string {
	n := len(left)
	if len(right) > n {
		n = len(right)
	}
	lines := make([]string, n)
	for i := 0; i < n; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		lines[i] = padRight(l, leftW) + "  " + padRight(r, rightW)
	}
	return lines
}

// box wraps content lines in a rounded border with a title embedded in the top
// edge, btop/htop style.
func (m Model) box(lines []string, contentW int) string {
	node := m.snap.Hostname
	if node == "" {
		node = m.snap.Node
	}
	if node == "" {
		node = "(endpoint)"
	}
	title := "talos-htop · " + node
	if m.snap.Version != "" {
		title += " · " + m.snap.Version
	}
	if m.lastErr != nil {
		title += " · " + styleErr.Render("poll error")
	}

	var b strings.Builder
	// Top border: ╭─ title ───...───╮
	prefix := "╭─ "
	styledTitle := styleTitle.Render(title)
	used := lipgloss.Width(prefix) + lipgloss.Width(styledTitle) + 1 // +1 space
	dashes := m.width - used - 1                                     // -1 for ╮
	if dashes < 0 {
		dashes = 0
	}
	b.WriteString(styleBorder.Render(prefix))
	b.WriteString(styledTitle)
	b.WriteString(styleBorder.Render(" " + strings.Repeat("─", dashes) + "╮"))
	b.WriteByte('\n')

	for _, ln := range lines {
		b.WriteString(styleBorder.Render("│ "))
		b.WriteString(padRight(ln, contentW))
		b.WriteString(styleBorder.Render(" │"))
		b.WriteByte('\n')
	}

	b.WriteString(styleBorder.Render("╰" + strings.Repeat("─", m.width-2) + "╯"))
	return b.String()
}

// ---- process table ----------------------------------------------------------

type col struct {
	title string
	w     int
	right bool
}

var procCols = []col{
	{"PID", 7, true},
	{"S", 1, false},
	{"CPU%", 5, true},
	{"MEM%", 5, true},
	{"VIRT", 8, true},
	{"RES", 8, true},
	{"THR", 4, true},
	{"TIME+", 9, true},
}

const colSep = " │ "

// fixedWidth is the total width of the fixed columns plus their separators
// (including the separator before the Command column).
func fixedWidth() int {
	w := 0
	for _, c := range procCols {
		w += c.w
	}
	w += len(procCols) * len(colSep) // a separator after each fixed column
	return w
}

func (m Model) commandWidth() int {
	w := m.width - fixedWidth()
	if w < 6 {
		w = 6
	}
	return w
}

func fmtCell(c col, v string) string {
	if c.right {
		return fmt.Sprintf("%*s", c.w, truncate(v, c.w))
	}
	return fmt.Sprintf("%-*s", c.w, truncate(v, c.w))
}

func (m Model) renderColumns() string {
	sep := styleHeaderRow.Render(colSep)
	var parts []string
	for _, c := range procCols {
		title := c.title
		if c.title == m.sortColTitle() {
			title = m.sortArrow() + strings.TrimSpace(c.title)
		}
		parts = append(parts, styleHeaderRow.Render(fmtCell(c, title)))
	}
	cmdTitle := "Command"
	if m.sortKey == sortName {
		cmdTitle = m.sortArrow() + "Command"
	}
	line := strings.Join(parts, sep) + sep +
		styleHeaderRow.Render(padRight(truncate(cmdTitle, m.commandWidth()), m.commandWidth()))
	return line
}

func (m Model) sortColTitle() string {
	switch m.sortKey {
	case sortCPU:
		return "CPU%"
	case sortMem:
		return "MEM%"
	case sortPID:
		return "PID"
	case sortTime:
		return "TIME+"
	default:
		return ""
	}
}

func (m Model) sortArrow() string {
	if m.desc {
		return "▾"
	}
	return "▴"
}

func (m Model) renderRows() string {
	h := m.listHeight()
	var b strings.Builder
	if len(m.rows) == 0 {
		b.WriteString(styleDim.Render("  (no processes match)"))
		for i := 0; i < h; i++ {
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
	for ; drawn < h; drawn++ {
		b.WriteByte('\n')
	}
	return b.String()
}

func (m Model) renderRow(r row, selected bool) string {
	p := r.proc
	cmd := r.prefix + p.Name()
	cmdW := m.commandWidth()

	vals := []string{
		itoa(p.PID),
		stateShort(p.State),
		formatPercent(p.CPUPercent),
		formatPercent(p.MemPercent),
		humanBytes(p.VirtualMemory),
		humanBytes(p.ResidentMemory),
		fmt.Sprintf("%d", p.Threads),
		formatCPUTime(p.CPUTime),
	}

	if selected {
		// A uniform highlight bar, like htop's selected row.
		var plain strings.Builder
		for i, c := range procCols {
			plain.WriteString(fmtCell(c, vals[i]))
			plain.WriteString(colSep)
		}
		plain.WriteString(truncate(cmd, cmdW))
		return styleSelected.Render(padRight(plain.String(), m.width))
	}

	sep := styleSep.Render(colSep)
	var parts []string
	for i, c := range procCols {
		parts = append(parts, colorCell(c, vals[i], p))
	}
	line := strings.Join(parts, sep) + sep + renderCommand(r, cmdW)
	return line
}

// colorCell renders one fixed column with the appropriate colour.
func colorCell(c col, v string, p model.Process) string {
	cell := fmtCell(c, v)
	switch c.title {
	case "PID":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Render(cell)
	case "S":
		return stateStyle(p.State).Render(cell)
	case "CPU%":
		return lipgloss.NewStyle().Foreground(loadColor(p.CPUPercent)).Render(cell)
	case "MEM%":
		return lipgloss.NewStyle().Foreground(loadColor(p.MemPercent)).Render(cell)
	default:
		return styleDim.Render(cell)
	}
}

// renderCommand colours the tree prefix dimly and the command brighter.
func renderCommand(r row, w int) string {
	full := truncate(r.prefix+r.proc.Name(), w)
	if r.prefix != "" && strings.HasPrefix(full, r.prefix) {
		rest := full[len(r.prefix):]
		return styleSep.Render(r.prefix) + styleCmd.Render(rest)
	}
	return styleCmd.Render(full)
}

// ---- footer -----------------------------------------------------------------

func (m Model) renderFooter() string {
	if m.searching {
		return styleFooter.Render(padRight(truncate(m.search.View(), m.width), m.width))
	}

	type fk struct{ key, label string }
	var keys []fk
	if m.showHelp {
		keys = []fk{
			{"↑↓/jk", "Move"}, {"PgUp/Dn", "Page"}, {"g/G", "Top/Bot"},
			{"t", "Tree"}, {"p", "CPU"}, {"m", "MEM"}, {"n", "PID"},
			{"T", "TIME"}, {"c", "CMD"}, {"i", "Invert"}, {"/", "Search"}, {"q", "Quit"},
		}
	} else {
		tree := "off"
		if m.tree {
			tree = "on"
		}
		keys = []fk{
			{"F5", "Tree:" + tree}, {"F6", "Sort:" + m.sortKey.String() + m.sortArrow()},
			{"/", "Search"}, {"?", "Help"}, {"q", "Quit"},
		}
		if m.query != "" {
			keys = append([]fk{{"filter", "\"" + m.query + "\""}}, keys...)
		}
	}

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(styleFnKey.Render(" " + k.key + " "))
		b.WriteString(styleFooter.Render(k.label + " "))
	}
	return styleFooter.Render(padRight(truncate(b.String(), m.width), m.width))
}

// ---- small helpers ----------------------------------------------------------

func stateShort(s string) string {
	if s == "" {
		return "?"
	}
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
		return lipgloss.NewStyle().Foreground(colRed).Bold(true)
	default:
		return styleDim
	}
}

func loadString(l [3]float64) string {
	if l == [3]float64{} {
		return styleDim.Render("n/a")
	}
	c := func(v float64) string {
		return lipgloss.NewStyle().Foreground(loadColor(v * 25)).Render(fmt.Sprintf("%.2f", v))
	}
	return c(l[0]) + " " + c(l[1]) + " " + c(l[2])
}

func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
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

func itoaInt(v int) string { return fmt.Sprintf("%d", v) }
