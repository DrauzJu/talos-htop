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

// Model is the root Bubble Tea model.
type Model struct {
	src      source.Source
	interval time.Duration

	snap    model.Snapshot // last snapshot (kept on error)
	lastErr error          // error from the most recent poll, if any
	loaded  bool

	sortKey sortKey
	desc    bool
	tree    bool

	search    textinput.Model
	searching bool
	query     string

	rows   []row
	cursor int // index into rows of the selected process
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
		src:      src,
		interval: interval,
		sortKey:  sortCPU,
		desc:     true,
		search:   ti,
		width:    80,
		height:   24,
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

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampScroll()
		return m, nil

	case tickMsg:
		return m, tea.Batch(m.fetch(), tick(m.interval))

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

	switch msg.String() {
	case "q", "ctrl+c", "f10":
		return m, tea.Quit
	case "?", "f1":
		m.showHelp = !m.showHelp
		return m, nil
	case "t", "f5":
		m.tree = !m.tree
		m.rebuild()
	case "i", "I":
		m.desc = !m.desc
		m.rebuild()
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
		m.cursor = len(m.rows) - 1
		m.clampScroll()
	}
	return m, nil
}

// setSort selects a sort column, defaulting its direction (descending for the
// numeric columns, ascending for PID/name) the way htop does.
func (m *Model) setSort(k sortKey) {
	if m.sortKey == k {
		m.desc = !m.desc
	} else {
		m.sortKey = k
		m.desc = k == sortCPU || k == sortMem || k == sortTime
	}
	m.rebuild()
}

// rebuild recomputes the visible rows from the current snapshot and settings,
// keeping the selection on the same PID where possible.
func (m *Model) rebuild() {
	var selPID int32 = -1
	if m.cursor >= 0 && m.cursor < len(m.rows) {
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
	m.clampScroll()
}

func (m *Model) moveCursor(delta int) {
	m.cursor += delta
	m.clampScroll()
}

func (m *Model) clampScroll() {
	if len(m.rows) == 0 {
		m.cursor, m.offset = 0, 0
		return
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
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
