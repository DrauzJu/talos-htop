package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/drauzju/talos-htop/internal/model"
)

// Meter/segment colours, roughly matching htop's palette.
var (
	colGreen   = lipgloss.Color("2")   // used / normal
	colBlue    = lipgloss.Color("4")   // buffers / low
	colYellow  = lipgloss.Color("3")   // cache / medium load
	colRed     = lipgloss.Color("1")   // high load
	colGray    = lipgloss.Color("240") // empty / idle
	colBracket = lipgloss.Color("246")
	colLabel   = lipgloss.Color("6")
)

// segment is a run of bar characters drawn in one colour.
type segment struct {
	frac  float64 // fraction of the bar this segment occupies (0..1)
	color lipgloss.Color
}

// bar renders `label[####----  text]` with coloured segments summing to <=1 of
// the inner width. text is right-aligned inside the bar (the numeric readout).
func bar(label string, width int, text string, segs []segment) string {
	inner := width - len(label) - 2 // minus brackets
	if inner < 1 {
		inner = 1
	}

	// Decide the character at each cell: a filled glyph coloured by its
	// segment, or a trailing dot for the idle remainder.
	cells := make([]lipgloss.Color, inner)
	filled := make([]bool, inner)
	pos := 0
	for _, s := range segs {
		n := int(s.frac*float64(inner) + 0.5)
		for j := 0; j < n && pos < inner; j++ {
			cells[pos] = s.color
			filled[pos] = true
			pos++
		}
	}

	// Overlay the right-aligned readout text on top of the bar cells.
	textStart := inner - len(text)
	var b strings.Builder
	for i := 0; i < inner; i++ {
		var ch string
		inText := i >= textStart && textStart >= 0
		if inText {
			ch = string(text[i-textStart])
		} else if filled[i] {
			ch = "|"
		} else {
			ch = " "
		}
		color := colGray
		if filled[i] {
			color = cells[i]
		}
		b.WriteString(lipgloss.NewStyle().Foreground(color).Render(ch))
	}

	br := lipgloss.NewStyle().Foreground(colBracket)
	lbl := lipgloss.NewStyle().Foreground(colLabel).Bold(true).Render(label)
	return lbl + br.Render("[") + b.String() + br.Render("]")
}

// loadColor maps a 0..100 utilisation to green/yellow/red like htop.
func loadColor(pct float64) lipgloss.Color {
	switch {
	case pct >= 80:
		return colRed
	case pct >= 50:
		return colYellow
	default:
		return colGreen
	}
}

// cpuMeter renders a single CPU (core or total) as an intensity-coloured bar.
func cpuMeter(label string, pct float64, width int) string {
	text := fmt.Sprintf("%5.1f%%", pct)
	segs := []segment{{frac: pct / 100, color: loadColor(pct)}}
	return bar(label, width, text, segs)
}

// memMeter renders the used/buffers/cache breakdown against total memory.
func memMeter(m model.MemUsage, width int) string {
	text := fmt.Sprintf("%s/%s", humanBytes(m.Used), humanBytes(m.Total))
	var segs []segment
	if m.Total > 0 {
		t := float64(m.Total)
		segs = []segment{
			{frac: float64(m.Used) / t, color: colGreen},
			{frac: float64(m.Buffers) / t, color: colBlue},
			{frac: float64(m.Cached) / t, color: colYellow},
		}
	}
	return bar("Mem", width, text, segs)
}

// swapMeter renders swap usage, or an idle bar with a hint when swap is absent
// (the common case on Talos).
func swapMeter(m model.MemUsage, width int) string {
	if m.SwapTotal == 0 {
		return bar("Swp", width, "none", nil)
	}
	text := fmt.Sprintf("%s/%s", humanBytes(m.SwapUsed), humanBytes(m.SwapTotal))
	segs := []segment{{frac: float64(m.SwapUsed) / float64(m.SwapTotal), color: colRed}}
	return bar("Swp", width, text, segs)
}
