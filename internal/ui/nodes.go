package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/joehahndev/drupaltui/internal/api"
)

type nodesMode int

const (
	nodesNormal nodesMode = iota
	nodesFiltering
	nodesConfirmToggle
)

type nodesModel struct {
	inited bool
	ct     api.ContentType

	tbl   table.Model
	nodes []api.Node
	next  string

	filterInput  textinput.Model
	activeFilter string

	mode    nodesMode
	pending api.Node // node awaiting publish/unpublish confirmation

	width, height int
}

func newNodesModel(ct api.ContentType, width, height int) nodesModel {
	fi := textinput.New()
	fi.Prompt = "/ "
	fi.Placeholder = "title contains…"

	m := nodesModel{inited: true, ct: ct, filterInput: fi, width: width, height: height}
	m.tbl = table.New(
		table.WithColumns(m.columns()),
		table.WithFocused(true),
		table.WithHeight(m.tableHeight()),
	)
	return m
}

func (m *nodesModel) columns() []table.Column {
	titleW := m.width - 14 - 14 - 6
	if titleW < 20 {
		titleW = 20
	}
	return []table.Column{
		{Title: "Title", Width: titleW},
		{Title: "Status", Width: 14},
		{Title: "Changed", Width: 14},
	}
}

func (m *nodesModel) tableHeight() int {
	h := m.height - 5 // header, filter/help lines
	if h < 3 {
		h = 3
	}
	return h
}

func (m *nodesModel) resize(w, h int) {
	if !m.inited {
		return
	}
	m.width, m.height = w, h
	m.tbl.SetColumns(m.columns())
	m.tbl.SetHeight(m.tableHeight())
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}

func (m *nodesModel) rebuildRows() {
	rows := make([]table.Row, len(m.nodes))
	for i, n := range m.nodes {
		status := unpublishedStyle.Render("unpublished")
		if n.Status {
			status = publishedStyle.Render("published")
		}
		rows[i] = table.Row{n.Title, status, relTime(n.Changed)}
	}
	m.tbl.SetRows(rows)
}

func (m *nodesModel) setPage(page api.NodePage, appendPage bool) {
	if appendPage {
		m.nodes = append(m.nodes, page.Nodes...)
	} else {
		m.nodes = page.Nodes
	}
	m.next = page.Next
	m.rebuildRows()
	if !appendPage && len(m.nodes) > 0 {
		m.tbl.SetCursor(0)
	}
}

func (m *nodesModel) updateRow(node api.Node) {
	for i := range m.nodes {
		if m.nodes[i].UUID == node.UUID {
			m.nodes[i].Status = node.Status
			m.nodes[i].Changed = node.Changed
			if node.Title != "" {
				m.nodes[i].Title = node.Title
			}
		}
	}
	m.rebuildRows()
}

func (m nodesModel) selected() (api.Node, bool) {
	i := m.tbl.Cursor()
	if i < 0 || i >= len(m.nodes) {
		return api.Node{}, false
	}
	return m.nodes[i], true
}

func (m nodesModel) update(msg tea.Msg) (nodesModel, tea.Cmd) {
	key, isKey := msg.(tea.KeyMsg)
	if !isKey {
		var cmd tea.Cmd
		m.tbl, cmd = m.tbl.Update(msg)
		return m, cmd
	}

	switch m.mode {
	case nodesFiltering:
		switch key.String() {
		case "enter":
			m.mode = nodesNormal
			m.filterInput.Blur()
			filter := m.filterInput.Value()
			return m, func() tea.Msg { return reloadNodesMsg{filter: filter} }
		case "esc":
			m.mode = nodesNormal
			m.filterInput.Blur()
			m.filterInput.SetValue(m.activeFilter)
			return m, nil
		}
		var cmd tea.Cmd
		m.filterInput, cmd = m.filterInput.Update(msg)
		return m, cmd

	case nodesConfirmToggle:
		switch key.String() {
		case "y":
			m.mode = nodesNormal
			node, toPublish := m.pending, !m.pending.Status
			return m, func() tea.Msg { return requestToggleMsg{node: node, toPublish: toPublish} }
		case "n", "esc":
			m.mode = nodesNormal
			return m, nil
		}
		return m, nil
	}

	switch key.String() {
	case "enter":
		if n, ok := m.selected(); ok {
			return m, func() tea.Msg { return openNodeMsg{uuid: n.UUID} }
		}
	case "e":
		if n, ok := m.selected(); ok {
			return m, func() tea.Msg { return editNodeMsg{uuid: n.UUID} }
		}
	case "n":
		return m, func() tea.Msg { return startCreateMsg{} }
	case "p":
		if n, ok := m.selected(); ok {
			if !m.ct.Editable() {
				reason := m.ct.ReadOnlyReason()
				return m, func() tea.Msg { return statusNoteMsg{reason} }
			}
			m.pending = n
			m.mode = nodesConfirmToggle
		}
		return m, nil
	case "m":
		if m.next != "" {
			next := m.next
			return m, func() tea.Msg { return loadMoreMsg{next: next} }
		}
		return m, func() tea.Msg { return statusNoteMsg{"No more pages."} }
	case "r":
		filter := m.activeFilter
		return m, func() tea.Msg { return reloadNodesMsg{filter: filter} }
	case "/":
		m.mode = nodesFiltering
		m.filterInput.Focus()
		return m, nil
	case "esc", "q":
		return m, func() tea.Msg { return goBackMsg{} }
	}

	var cmd tea.Cmd
	m.tbl, cmd = m.tbl.Update(msg)
	return m, cmd
}

func (m nodesModel) view() string {
	header := titleStyle.Render(m.ct.Name) + " " + badge(m.ct.Editable(), m.ct.Verified)
	if m.activeFilter != "" {
		header += dimStyle.Render("  filter: " + m.activeFilter)
	}

	var footer string
	switch m.mode {
	case nodesFiltering:
		footer = m.filterInput.View()
	case nodesConfirmToggle:
		verb := "Publish"
		if m.pending.Status {
			verb = "Unpublish"
		}
		footer = promptStyle.Render(fmt.Sprintf("%s %q? (y/n)", verb, m.pending.Title))
	default:
		hint := "enter view · e edit · n new · p publish/unpublish · / filter · r refresh · esc back"
		if m.next != "" {
			hint = "m more · " + hint
		}
		if len(m.nodes) == 0 {
			footer = dimStyle.Render("  no content found") + "\n" + helpStyle.Render(hint)
		} else {
			footer = helpStyle.Render(hint)
		}
	}
	return header + "\n" + m.tbl.View() + "\n" + footer
}
