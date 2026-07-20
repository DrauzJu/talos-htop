package ui

import (
	"fmt"
	"time"
)

// humanBytes renders a byte count the way htop does: a compact value with a
// binary-unit suffix (K/M/G/T), keeping the field narrow.
func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	val := float64(b)
	units := []string{"K", "M", "G", "T", "P"}
	i := -1
	for val >= unit && i < len(units)-1 {
		val /= unit
		i++
	}
	if val >= 100 {
		return fmt.Sprintf("%.0f%s", val, units[i])
	}
	if val >= 10 {
		return fmt.Sprintf("%.1f%s", val, units[i])
	}
	return fmt.Sprintf("%.2f%s", val, units[i])
}

// formatCPUTime renders cumulative CPU seconds as htop's TIME+ column:
// MMM:SS.hh (minutes can grow unbounded).
func formatCPUTime(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	total := time.Duration(seconds * float64(time.Second))
	mins := int(total / time.Minute)
	secs := total % time.Minute
	hundredths := (secs % time.Second) / (10 * time.Millisecond)
	return fmt.Sprintf("%d:%02d.%02d", mins, int(secs/time.Second), int(hundredths))
}

// formatUptime renders a duration as htop's uptime string.
func formatUptime(d time.Duration) string {
	if d <= 0 {
		return "?"
	}
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	s := int((d % time.Minute) / time.Second)
	if days > 0 {
		return fmt.Sprintf("%dd, %02d:%02d:%02d", days, h, m, s)
	}
	return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
}

func formatPercent(v float64) string {
	return fmt.Sprintf("%.1f", v)
}
