package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/joehahndev/drupaltui/internal/api"
)

type formatItem struct{ f api.FilterFormat }

func (i formatItem) Title() string       { return i.f.Name }
func (i formatItem) Description() string { return i.f.Machine }
func (i formatItem) FilterValue() string { return i.f.Name + " " + i.f.Machine }

// pickerModel selects the text format used for markdown bodies. When the
// account can't list formats, it falls back to free-text entry.
type pickerModel struct {
	inited   bool
	freeText bool
	lst      list.Model
	input    textinput.Model
}

func newPickerModel(formats []api.FilterFormat, width, height int) pickerModel {
	m := pickerModel{inited: true}
	if len(formats) == 0 {
		m.freeText = true
		m.input = textinput.New()
		m.input.Prompt = "Text format machine name > "
		m.input.Placeholder = "markdown_easy"
		m.input.Focus()
		return m
	}
	items := make([]list.Item, len(formats))
	for i, f := range formats {
		items[i] = formatItem{f}
	}
	m.lst = list.New(items, list.NewDefaultDelegate(), width, height)
	m.lst.Title = "Pick the text format for markdown bodies"
	m.lst.SetShowStatusBar(false)
	return m
}

func (m *pickerModel) resize(w, h int) {
	if m.inited && !m.freeText {
		m.lst.SetSize(w, h)
	}
}

func (m pickerModel) update(msg tea.Msg) (pickerModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.String() == "enter" {
		if m.freeText {
			v := strings.TrimSpace(m.input.Value())
			if v == "" {
				return m, func() tea.Msg { return statusNoteMsg{"Enter a text format machine name."} }
			}
			return m, func() tea.Msg { return formatChosenMsg{machine: v} }
		}
		if it, ok := m.lst.SelectedItem().(formatItem); ok {
			return m, func() tea.Msg { return formatChosenMsg{machine: it.f.Machine} }
		}
		return m, nil
	}
	var cmd tea.Cmd
	if m.freeText {
		m.input, cmd = m.input.Update(msg)
	} else {
		m.lst, cmd = m.lst.Update(msg)
	}
	return m, cmd
}

func (m pickerModel) view() string {
	if m.freeText {
		return titleStyle.Render("Choose text format") + "\n\n" +
			dimStyle.Render("  Couldn't list the site's text formats (permission denied).\n"+
				"  Enter the machine name of the markdown format to use:") + "\n\n" +
			"  " + m.input.View() + "\n\n" +
			helpStyle.Render("  enter confirm · ctrl+c quit")
	}
	return m.lst.View()
}
