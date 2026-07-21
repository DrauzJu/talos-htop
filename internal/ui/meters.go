package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/drauzju/talos-htop/internal/model"
)

// Palette, roughly matching htop's default colours.
var (
	colGreen   = lipgloss.Color("2")   // user / used memory
	colBlue    = lipgloss.Color("4")   // nice / buffers
	colRed     = lipgloss.Color("1")   // kernel / high load
	colYellow  = lipgloss.Color("3")   // cache
	colMagenta = lipgloss.Color("5")   // irq / softirq
	colCyan    = lipgloss.Color("6")   // steal / guest / labels
	colGray    = lipgloss.Color("240") // empty / idle
	colBracket = lipgloss.Color("245")
	colLabel   = lipgloss.Color("6")
)

// segment is a run of bar characters drawn in one colour.
type segment struct {
	frac  float64 // fraction of the inner bar this segment occupies (0..1)
	color lipgloss.Color
}

// bar renders `label[|||###   text]` where coloured segments fill from the left
// and the numeric readout is drawn right-aligned on top of the bar, exactly
// like an htop meter.
func bar(label string, width int, text string, segs []segment) string {
	inner := width - lipgloss.Width(label) - 2 // minus the two brackets
	if inner < 1 {
		inner = 1
	}

	cellColor := make([]lipgloss.Color, inner)
	filled := make([]bool, inner)
	pos := 0
	for _, s := range segs {
		n := int(s.frac*float64(inner) + 0.5)
		for j := 0; j < n && pos < inner; j++ {
			cellColor[pos] = s.color
			filled[pos] = true
			pos++
		}
	}

	// Overlay the right-aligned readout text.
	textStart := inner - lipgloss.Width(text)
	var b strings.Builder
	for i := 0; i < inner; i++ {
		var ch string
		if textStart >= 0 && i >= textStart {
			ch = string(text[i-textStart])
		} else if filled[i] {
			ch = "│"
		} else {
			ch = " "
		}
		color := colGray
		if filled[i] {
			color = cellColor[i]
		}
		b.WriteString(lipgloss.NewStyle().Foreground(color).Render(ch))
	}

	br := lipgloss.NewStyle().Foreground(colBracket)
	lbl := lipgloss.NewStyle().Foreground(colLabel).Bold(true).Render(label)
	return lbl + br.Render("[") + b.String() + br.Render("]")
}

// cpuSegments turns a CPULoad into htop-ordered coloured segments (fractions of
// the whole bar).
func cpuSegments(l model.CPULoad) []segment {
	return []segment{
		{frac: l.Nice / 100, color: colBlue},
		{frac: l.User / 100, color: colGreen},
		{frac: l.System / 100, color: colRed},
		{frac: l.IRQ / 100, color: colMagenta},
		{frac: l.Other / 100, color: colCyan},
	}
}

// cpuMeter renders a single CPU (core or aggregate) with segmented colouring.
func cpuMeter(label string, l model.CPULoad, width int) string {
	busy := l.Busy()
	text := fmt.Sprintf("%4.1f%%", busy)
	return bar(label, width, text, cpuSegments(l))
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

// swapMeter renders swap usage, or an idle bar labelled "none" when swap is
// absent (the common case on Talos).
func swapMeter(m model.MemUsage, width int) string {
	if m.SwapTotal == 0 {
		return bar("Swp", width, "none", nil)
	}
	text := fmt.Sprintf("%s/%s", humanBytes(m.SwapUsed), humanBytes(m.SwapTotal))
	segs := []segment{{frac: float64(m.SwapUsed) / float64(m.SwapTotal), color: colRed}}
	return bar("Swp", width, text, segs)
}

// loadColor maps a 0..100 utilisation to green/yellow/red, used for the CPU%
// and MEM% cells in the process table.
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
