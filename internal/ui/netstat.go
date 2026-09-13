package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/drauzju/talos-htop/internal/model"
)

// netCol describes a column in the network table. A column is either fixed
// width (w > 0) or flexible (w == 0), in which case flex is its share of the
// leftover width. This mirrors netstat -tulpn's columns.
type netCol struct {
	title string
	w     int
	flex  int
	right bool
}

var netCols = []netCol{
	{"Proto", 5, 0, false},
	{"Recv-Q", 6, 0, true},
	{"Send-Q", 6, 0, true},
	{"Local Address", 0, 4, false},
	{"Foreign Address", 0, 4, false},
	{"State", 11, 0, false},
	{"PID", 7, 0, true},
	{"Program", 0, 3, false},
}

// netColWidths resolves the concrete width of each column for the current
// terminal width: fixed columns keep their width and the flexible ones share
// what remains, weighted by their flex value.
func (m Model) netColWidths() []int {
	widths := make([]int, len(netCols))
	fixed, flexTotal, lastFlex := 0, 0, -1
	for i, c := range netCols {
		if c.w > 0 {
			widths[i] = c.w
			fixed += c.w
		} else {
			flexTotal += c.flex
			lastFlex = i
		}
	}

	seps := (len(netCols) - 1) * len(colSep)
	remaining := m.width - fixed - seps
	if remaining < len(netCols) {
		remaining = len(netCols)
	}

	if flexTotal > 0 {
		acc := 0
		for i, c := range netCols {
			if c.w > 0 {
				continue
			}
			var w int
			if i == lastFlex {
				w = remaining - acc // last flex column absorbs the rounding remainder
			} else {
				w = remaining * c.flex / flexTotal
				acc += w
			}
			if w < 6 {
				w = 6
			}
			widths[i] = w
		}
	}
	return widths
}

// socketKey identifies a row: everything the table shows. Two sockets with the
// same key are indistinguishable on screen, so they collapse into one row.
type socketKey struct {
	protocol   string
	localIP    string
	localPort  uint32
	remoteIP   string
	remotePort uint32
	state      string
	pid        int32
	process    string
}

func keyOf(s model.Socket) socketKey {
	return socketKey{
		protocol:   s.Protocol,
		localIP:    s.LocalIP,
		localPort:  s.LocalPort,
		remoteIP:   s.RemoteIP,
		remotePort: s.RemotePort,
		state:      s.State,
		pid:        s.PID,
		process:    s.Process,
	}
}

// buildSocketRows filters, collapses and orders sockets for display.
// Listening-only mode keeps just the server sockets (netstat -l); the query
// filters on any visible field. Sockets that differ only in their inode — the
// per-worker listeners a SO_REUSEPORT bind creates — collapse into one row
// carrying the count, since plain netstat's one-line-per-socket output is just
// the same row repeated. Rows are ordered by local port so "who's on port N"
// reads top-to-bottom.
func buildSocketRows(socks []model.Socket, query string, listeningOnly bool) []model.Socket {
	q := strings.ToLower(query)
	out := make([]model.Socket, 0, len(socks))
	at := make(map[socketKey]int, len(socks))
	for _, s := range socks {
		if listeningOnly && !s.Listening() {
			continue
		}
		if q != "" && !socketMatches(s, q) {
			continue
		}
		if i, ok := at[keyOf(s)]; ok {
			r := &out[i]
			r.Count++
			// Keep the largest queue depth of the set, so a backlog on one of
			// the collapsed sockets is never hidden by a quiet sibling.
			r.RxQueue = max(r.RxQueue, s.RxQueue)
			r.TxQueue = max(r.TxQueue, s.TxQueue)
			continue
		}
		s.Count = 1
		at[keyOf(s)] = len(out)
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.LocalPort != b.LocalPort {
			return a.LocalPort < b.LocalPort
		}
		if a.Protocol != b.Protocol {
			return a.Protocol < b.Protocol
		}
		if a.LocalIP != b.LocalIP {
			return a.LocalIP < b.LocalIP
		}
		if a.RemotePort != b.RemotePort {
			return a.RemotePort < b.RemotePort
		}
		return a.RemoteIP < b.RemoteIP
	})
	return out
}

// socketMatches reports whether a socket contains the (lower-cased) query in any
// of its user-visible fields.
func socketMatches(s model.Socket, q string) bool {
	return strings.Contains(strings.ToLower(s.Protocol), q) ||
		strings.Contains(strings.ToLower(s.Process), q) ||
		strings.Contains(strings.ToLower(s.State), q) ||
		strings.Contains(itoa(s.PID), q) ||
		strings.Contains(strconv.FormatUint(uint64(s.LocalPort), 10), q) ||
		strings.Contains(strings.ToLower(s.LocalIP), q) ||
		strings.Contains(strconv.FormatUint(uint64(s.RemotePort), 10), q) ||
		strings.Contains(strings.ToLower(s.RemoteIP), q)
}

// ---- rendering --------------------------------------------------------------

func (m Model) renderNetColumns() string {
	ws := m.netColWidths()
	sep := styleHeaderRow.Render(colSep)
	parts := make([]string, len(netCols))
	for i, c := range netCols {
		parts[i] = styleHeaderRow.Render(fmtCellW(c.right, ws[i], c.title))
	}
	return strings.Join(parts, sep)
}

func (m Model) renderNetRows() string {
	h := m.listHeight()
	ws := m.netColWidths()
	var b strings.Builder

	if len(m.netRows) == 0 {
		msg := "  (no listening sockets)"
		if !m.netListeningOnly {
			msg = "  (no sockets)"
		}
		if m.socketErr != nil {
			msg = "  " + m.socketErr.Error()
		} else if !m.socketsLoaded {
			// Only before the first poll lands — a poll that legitimately
			// returns nothing must read as empty, not as still loading.
			msg = "  (fetching sockets…)"
		}
		b.WriteString(styleDim.Render(msg))
		for i := 0; i < h; i++ {
			b.WriteByte('\n')
		}
		return b.String()
	}

	end := m.offset + h
	if end > len(m.netRows) {
		end = len(m.netRows)
	}
	drawn := 0
	for i := m.offset; i < end; i++ {
		b.WriteString(m.renderNetRow(m.netRows[i], i == m.cursor, ws))
		b.WriteByte('\n')
		drawn++
	}
	for ; drawn < h; drawn++ {
		b.WriteByte('\n')
	}
	return b.String()
}

func (m Model) renderNetRow(s model.Socket, selected bool, ws []int) string {
	vals := netCellValues(s)

	if selected {
		var plain strings.Builder
		for i := range netCols {
			plain.WriteString(fmtCellW(netCols[i].right, ws[i], vals[i]))
			if i < len(netCols)-1 {
				plain.WriteString(colSep)
			}
		}
		return styleSelected.Render(padRight(plain.String(), m.width))
	}

	sep := styleSep.Render(colSep)
	parts := make([]string, len(netCols))
	for i, c := range netCols {
		parts[i] = colorNetCell(c, ws[i], vals[i], s)
	}
	return strings.Join(parts, sep)
}

// netCellValues renders each column's text for a socket, in netCols order.
func netCellValues(s model.Socket) []string {
	return []string{
		s.Protocol,
		formatQueue(s.RxQueue),
		formatQueue(s.TxQueue),
		formatAddr(s.LocalIP, s.LocalPort),
		formatAddr(s.RemoteIP, s.RemotePort),
		s.State,
		pidLabel(s.PID),
		programLabel(s),
	}
}

func colorNetCell(c netCol, w int, v string, s model.Socket) string {
	cell := fmtCellW(c.right, w, v)
	switch c.title {
	case "Proto":
		return styleDim.Render(cell)
	case "State":
		return netStateStyle(s.State).Render(cell)
	case "PID":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("246")).Render(cell)
	case "Program":
		return styleCmd.Render(cell)
	case "Local Address":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Render(cell)
	default:
		return styleDim.Render(cell)
	}
}

// netStateStyle colours the State column: green for LISTEN, cyan for
// ESTABLISHED, yellow for the transient/closing states.
func netStateStyle(state string) lipgloss.Style {
	switch state {
	case "LISTEN":
		return lipgloss.NewStyle().Foreground(colGreen).Bold(true)
	case "ESTABLISHED":
		return lipgloss.NewStyle().Foreground(colCyan)
	case "TIME_WAIT", "CLOSE_WAIT", "FIN_WAIT1", "FIN_WAIT2", "CLOSING", "LAST_ACK", "SYN_SENT", "SYN_RECV":
		return lipgloss.NewStyle().Foreground(colYellow)
	default:
		return styleDim
	}
}

// ---- small helpers ----------------------------------------------------------

// formatAddr renders an endpoint the way netstat -n does: host:port, with a
// wildcard port shown as "*" and IPv6 hosts bracketed so the colons are
// unambiguous.
func formatAddr(ip string, port uint32) string {
	if ip == "" {
		ip = "*"
	}
	p := "*"
	if port != 0 {
		p = strconv.FormatUint(uint64(port), 10)
	}
	if strings.Contains(ip, ":") {
		return "[" + ip + "]:" + p
	}
	return ip + ":" + p
}

func formatQueue(v uint64) string {
	return strconv.FormatUint(v, 10)
}

func pidLabel(pid int32) string {
	if pid == 0 {
		return "-"
	}
	return itoa(pid)
}

func programLabel(s model.Socket) string {
	name := s.Process
	if name == "" {
		name = "-"
	}
	if s.Count > 1 {
		// Several identical sockets behind this row (a SO_REUSEPORT bind).
		name += " ×" + strconv.Itoa(s.Count)
	}
	return name
}

// fmtCellW renders a value padded/truncated to width w, right- or left-aligned.
func fmtCellW(right bool, w int, v string) string {
	if right {
		return fmt.Sprintf("%*s", w, truncate(v, w))
	}
	return fmt.Sprintf("%-*s", w, truncate(v, w))
}
