// Command talos-htop is an htop-style interactive process viewer for Talos
// Linux nodes. Talos has no shell, so instead of reading /proc locally it polls
// the Talos gRPC machine API (the same API `talosctl dashboard` uses) and
// renders the result as a familiar full-screen TUI.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/drauzju/talos-htop/internal/source"
	"github.com/drauzju/talos-htop/internal/ui"
)

func main() {
	var (
		talosconfig = flag.String("talosconfig", "", "path to talosconfig (default: $TALOSCONFIG or ~/.talos/config)")
		contextName = flag.String("context", "", "talosconfig context to use (default: config's current context)")
		endpoints   = flag.String("endpoints", "", "comma-separated endpoint (apid) addresses, overriding the config")
		nodesFlag   = flag.String("nodes", "", "node to target (IP or name); default talks to the endpoint directly")
		refresh     = flag.Duration("refresh", 2*time.Second, "refresh interval")
		demo        = flag.Bool("demo", false, "run against synthetic data, no cluster required")
	)
	flag.Usage = usage
	flag.Parse()

	var (
		src source.Source
		err error
	)
	if *demo {
		src = source.NewMock(runtime.NumCPU())
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		src, err = source.NewTalos(ctx, source.Config{
			ConfigPath:  *talosconfig,
			ContextName: *contextName,
			Endpoints:   splitCSV(*endpoints),
			Node:        firstNode(*nodesFlag),
		})
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "talos-htop: %v\n", err)
			fmt.Fprintln(os.Stderr, "\nTip: pass --demo to explore the UI without a cluster.")
			os.Exit(1)
		}
	}
	defer src.Close()

	p := tea.NewProgram(
		ui.New(src, *refresh),
		tea.WithAltScreen(),
	)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "talos-htop: %v\n", err)
		os.Exit(1)
	}
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// firstNode targets a single node. talos-htop shows one node per screen, so if
// several are given we use the first (multi-node is a later iteration).
func firstNode(s string) string {
	n := splitCSV(s)
	if len(n) == 0 {
		return ""
	}
	return n[0]
}

func usage() {
	fmt.Fprint(os.Stderr, `talos-htop — htop-style process viewer for Talos Linux

Usage:
  talos-htop [flags]

Flags:
`)
	flag.PrintDefaults()
	fmt.Fprint(os.Stderr, `
Keys:
  ↑/↓ or j/k   move selection      t / F5   toggle tree view
  PgUp/PgDn    page                p        sort by CPU%
  g / G        top / bottom        m        sort by MEM%
  / or F3      search              n        sort by PID
  i            invert sort order   T        sort by TIME+
  s / F2       network (netstat)   c        sort by command
  l / F4       listening ↔ all     ? / F1   toggle help
  q / F10      quit

Examples:
  talos-htop --nodes 10.0.0.5
  talos-htop --talosconfig ./talosconfig --context prod --nodes worker-1
  talos-htop --demo
`)
}
