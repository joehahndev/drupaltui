package ui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

	"github.com/joehahndev/drupaltui/internal/api"
)

// renderMarkdownCmd renders markdown with glamour off the update loop.
// target routes the result to the right screen ("detail" or "edit").
func renderMarkdownCmd(target, md string, width int) tea.Cmd {
	return func() tea.Msg {
		wrap := width - 2
		if wrap > 100 {
			wrap = 100
		}
		if wrap < 20 {
			wrap = 20
		}
		r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(wrap))
		if err != nil {
			return mdRenderedMsg{target: target, out: md}
		}
		out, err := r.Render(md)
		if err != nil {
			return mdRenderedMsg{target: target, out: md}
		}
		return mdRenderedMsg{target: target, out: out}
	}
}

type detailModel struct {
	inited        bool
	ct            api.ContentType
	node          api.Node
	vp            viewport.Model
	confirmToggle bool
	width, height int
}

func newDetailModel(ct api.ContentType, node api.Node, width, height int) detailModel {
	m := detailModel{inited: true, ct: ct, node: node, width: width, height: height}
	m.vp = viewport.New(width, m.bodyHeight())
	return m
}

func (m *detailModel) bodyHeight() int {
	h := m.height - 4 // title, meta, help
	if h < 3 {
		h = 3
	}
	return h
}

func (m *detailModel) resize(w, h int) {
	if !m.inited {
		return
	}
	m.width, m.height = w, h
	m.vp.Width = w
	m.vp.Height = m.bodyHeight()
}

// rerender kicks off a glamour render of the current body.
func (m *detailModel) rerender() tea.Cmd {
	if !m.node.HasBody {
		m.vp.SetContent(dimStyle.Render("\n  (this content type has no body field)"))
		return nil
	}
	if m.node.Body == "" {
		m.vp.SetContent(dimStyle.Render("\n  (empty body)"))
		return nil
	}
	return renderMarkdownCmd("detail", m.node.Body, m.width)
}

func (m detailModel) update(msg tea.Msg) (detailModel, tea.Cmd) {
	switch msg := msg.(type) {
	case mdRenderedMsg:
		if msg.target == "detail" {
			m.vp.SetContent(msg.out)
		}
		return m, nil

	case tea.KeyMsg:
		if m.confirmToggle {
			switch msg.String() {
			case "y":
				m.confirmToggle = false
				node, toPublish := m.node, !m.node.Status
				return m, func() tea.Msg { return requestToggleMsg{node: node, toPublish: toPublish} }
			case "n", "esc":
				m.confirmToggle = false
			}
			return m, nil
		}
		switch msg.String() {
		case "e":
			uuid := m.node.UUID
			return m, func() tea.Msg { return editNodeMsg{uuid: uuid} }
		case "p":
			if !m.ct.Editable() {
				reason := m.ct.ReadOnlyReason()
				return m, func() tea.Msg { return statusNoteMsg{reason} }
			}
			m.confirmToggle = true
			return m, nil
		case "esc", "q":
			return m, func() tea.Msg { return goBackMsg{} }
		}
	}
	var cmd tea.Cmd
	m.vp, cmd = m.vp.Update(msg)
	return m, cmd
}

func (m detailModel) view() string {
	status := unpublishedStyle.Render("unpublished")
	if m.node.Status {
		status = publishedStyle.Render("published")
	}
	meta := fmt.Sprintf("%s · %s · changed %s", m.ct.Name, status, relTime(m.node.Changed))

	footer := helpStyle.Render("e edit · p publish/unpublish · ↑/↓ scroll · esc back")
	if m.confirmToggle {
		verb := "Publish"
		if m.node.Status {
			verb = "Unpublish"
		}
		footer = promptStyle.Render(fmt.Sprintf("%s %q? (y/n)", verb, m.node.Title))
	}

	return titleStyle.Render(m.node.Title) + "\n" +
		dimStyle.Render(meta) + "\n" +
		m.vp.View() + "\n" +
		footer
}
