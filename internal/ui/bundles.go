package ui

import (
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/joehahndev/drupaltui/internal/api"
)

type bundleItem struct{ ct api.ContentType }

func (i bundleItem) Title() string {
	return i.ct.Name + "  " + badge(i.ct.Editable(), i.ct.Verified)
}

func (i bundleItem) Description() string {
	if reason := i.ct.ReadOnlyReason(); reason != "" {
		return i.ct.Machine + " · " + reason
	}
	return i.ct.Machine
}

func (i bundleItem) FilterValue() string { return i.ct.Name + " " + i.ct.Machine }

type bundlesModel struct {
	inited          bool
	lst             list.Model
	revisionsFailed bool
}

func newBundlesModel(types []api.ContentType, revisionsFailed bool, width, height int) bundlesModel {
	items := make([]list.Item, len(types))
	for i, ct := range types {
		items[i] = bundleItem{ct}
	}
	l := list.New(items, list.NewDefaultDelegate(), width, height)
	l.Title = "Content types"
	l.SetShowStatusBar(false)
	return bundlesModel{inited: true, lst: l, revisionsFailed: revisionsFailed}
}

func (m *bundlesModel) resize(w, h int) {
	if m.inited {
		m.lst.SetSize(w, h)
	}
}

func (m bundlesModel) update(msg tea.Msg) (bundlesModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		// Don't hijack keys while the list's fuzzy filter is open.
		if m.lst.FilterState() != list.Filtering {
			switch key.String() {
			case "enter":
				if it, ok := m.lst.SelectedItem().(bundleItem); ok {
					ct := it.ct
					return m, func() tea.Msg { return openBundleMsg{ct: ct} }
				}
			case "q", "esc":
				return m, tea.Quit
			}
		}
	}
	var cmd tea.Cmd
	m.lst, cmd = m.lst.Update(msg)
	return m, cmd
}

func (m bundlesModel) view() string {
	out := m.lst.View()
	if m.revisionsFailed {
		out += "\n" + dimStyle.Render(
			"  Note: this account can't read node type config, so revision settings are\n"+
				"  unverified — all content types are read-only for safety.")
	}
	return out
}
