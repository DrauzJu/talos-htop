// Package ui implements the Bubble Tea terminal UI: an htop-style header of
// meters over a scrollable, sortable, tree-capable process list.
package ui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/drauzju/talos-htop/internal/model"
	"github.com/drauzju/talos-htop/internal/source"
)

type tickMsg time.Time
type snapshotMsg struct{ snap model.Snapshot }
type socketsMsg struct {
	socks []model.Socket
	err   error
}

// viewMode selects which table fills the screen below the meter header.
type viewMode int

const (
	viewProc viewMode = iota // the htop-style process list (default)
	viewNet                  // the netstat-style network sockets list
)

// Model is the root Bubble Tea model.
type Model struct {
	src      source.Source
	interval time.Duration

	snap    model.Snapshot // last snapshot (kept on error)
	lastErr error          // error from the most recent poll, if any
	loaded  bool

	view viewMode

	sortKey sortKey
	desc    bool
	tree    bool

	// Network view state.
	sockets          []model.Socket // last socket poll (kept on error)
	socketErr        error          // error from the most recent socket poll
	socketsLoaded    bool           // a socket poll has completed at least once
	netRows          []model.Socket // filtered + sorted sockets for display
	netListeningOnly bool           // show only listening sockets (netstat -l)

	search    textinput.Model
	searching bool
	query     string

	rows   []row
	cursor int // index into the active view's rows of the selection
	offset int // first visible row (scroll position)

	width, height int
	showHelp      bool
}

// New builds the initial model for the given data source and refresh interval.
func New(src source.Source, interval time.Duration) Model {
	ti := textinput.New()
	ti.Prompt = "Search: "
	ti.CharLimit = 128

	return Model{
		src:              src,
		interval:         interval,
		sortKey:          sortCPU,
		desc:             true,
		netListeningOnly: true, // default matches `netstat -tulpn`
		search:           ti,
		width:            80,
		height:           24,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetch(), tick(m.interval))
}

func tick(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// fetch polls the source off the UI goroutine, with a timeout so a hung node
// never wedges the refresh loop.
func (m Model) fetch() tea.Cmd {
	src := m.src
	timeout := m.interval
	if timeout < 3*time.Second {
		timeout = 3 * time.Second
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return snapshotMsg{snap: src.Snapshot(ctx)}
	}
}

// fetchSockets polls the node's network sockets off the UI goroutine. It is
// only issued while the network view is active.
func (m Model) fetchSockets() tea.Cmd {
	src := m.src
	timeout := m.interval
	if timeout < 3*time.Second {
		timeout = 3 * time.Second
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		socks, err := src.Sockets(ctx)
		return socketsMsg{socks: socks, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampScroll()
		return m, nil

	case tickMsg:
		cmds := []tea.Cmd{m.fetch(), tick(m.interval)}
		if m.view == viewNet {
			// The network view needs a fresh socket poll each tick; the process
			// snapshot still drives the meter header and task counts.
			cmds = append(cmds, m.fetchSockets())
		}
		return m, tea.Batch(cmds...)

	case snapshotMsg:
		m.loaded = true
		m.lastErr = msg.snap.Err
		if msg.snap.Err == nil {
			m.snap = msg.snap
		} else if m.snap.Taken.IsZero() {
			// Never got a good frame; still record identity for the header.
			m.snap = msg.snap
		}
		m.rebuild()
		return m, nil

	case socketsMsg:
		m.socketErr = msg.err
		if msg.err == nil {
			m.sockets = msg.socks
			m.socketsLoaded = true
		}
		m.rebuild()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	// Feed everything else to the search box while it is open.
	if m.searching {
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		m.query = m.search.Value()
		m.rebuild()
		return m, cmd
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searching {
		switch msg.String() {
		case "enter":
			m.searching = false
			m.search.Blur()
			return m, nil
		case "esc":
			m.searching = false
			m.search.Blur()
			m.search.SetValue("")
			m.query = ""
			m.rebuild()
			return m, nil
		}
		var cmd tea.Cmd
		m.search, cmd = m.search.Update(msg)
		m.query = m.search.Value()
		m.rebuild()
		return m, cmd
	}

	// While the help page is open, any key dismisses it (Ctrl-C still quits).
	if m.showHelp {
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
		m.showHelp = false
		return m, nil
	}

	switch msg.String() {
	case "q", "ctrl+c", "f10":
		return m, tea.Quit
	case "?", "f1":
		m.showHelp = true
		return m, nil
	case "s", "f2":
		// Toggle between the process list and the netstat-style network view.
		if m.view == viewProc {
			m.view = viewNet
		} else {
			m.view = viewProc
		}
		m.cursor, m.offset = 0, 0
		m.rebuild()
		if m.view == viewNet {
			// Fetch immediately so the view isn't blank until the next tick.
			return m, m.fetchSockets()
		}
		return m, nil
	case "l", "L", "f4":
		// In the network view, toggle between listening-only (netstat -l) and
		// all sockets. A no-op in the process view.
		if m.view == viewNet {
			m.netListeningOnly = !m.netListeningOnly
			m.cursor, m.offset = 0, 0
			m.rebuild()
		}
		return m, nil
	case "t", "f5":
		m.tree = !m.tree
		m.rebuild()
	case "i", "I":
		if !m.tree { // sorting (and thus its direction) is disabled in tree view
			m.desc = !m.desc
			m.rebuild()
		}
	case "p", "P":
		m.setSort(sortCPU)
	case "m", "M":
		m.setSort(sortMem)
	case "n", "N":
		m.setSort(sortPID)
	case "T":
		m.setSort(sortTime)
	case "c", "C":
		m.setSort(sortName)
	case "f6":
		m.setSort((m.sortKey + 1) % 5)
	case "/", "f3":
		m.searching = true
		m.search.Focus()
		return m, textinput.Blink
	case "esc":
		if m.query != "" {
			m.query = ""
			m.search.SetValue("")
			m.rebuild()
		}
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "pgup":
		m.moveCursor(-m.listHeight())
	case "pgdown":
		m.moveCursor(m.listHeight())
	case "home", "g":
		m.cursor = 0
		m.clampScroll()
	case "end", "G":
		m.cursor = m.activeLen() - 1
		m.clampScroll()
	}
	return m, nil
}

// activeLen is the number of rows in the currently displayed view.
func (m Model) activeLen() int {
	if m.view == viewNet {
		return len(m.netRows)
	}
	return len(m.rows)
}

// setSort selects a sort column, defaulting its direction (descending for the
// numeric columns, ascending for PID/name) the way htop does. Sorting is
// disabled while the tree view is active, so this is a no-op there.
func (m *Model) setSort(k sortKey) {
	if m.tree {
		return
	}
	if m.sortKey == k {
		m.desc = !m.desc
	} else {
		m.sortKey = k
		m.desc = k == sortCPU || k == sortMem || k == sortTime
	}
	m.rebuild()
}

// rebuild recomputes the visible rows for both views from the current data and
// settings. The process rows also feed the meter header (task counts), so they
// are always rebuilt; the selection is kept on the same PID where possible when
// the process view is active.
func (m *Model) rebuild() {
	var selPID int32 = -1
	if m.view == viewProc && m.cursor >= 0 && m.cursor < len(m.rows) {
		selPID = m.rows[m.cursor].proc.PID
	}

	procs := filterProcs(m.snap.Proc, m.query)
	m.rows = buildRows(procs, m.sortKey, m.desc, m.tree)

	if selPID >= 0 {
		for i, r := range m.rows {
			if r.proc.PID == selPID {
				m.cursor = i
				break
			}
		}
	}

	m.netRows = buildSocketRows(m.sockets, m.query, m.netListeningOnly)
	m.clampScroll()
}

func (m *Model) moveCursor(delta int) {
	m.cursor += delta
	m.clampScroll()
}

func (m *Model) clampScroll() {
	n := m.activeLen()
	if n == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= n {
		m.cursor = n - 1
	}
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}
