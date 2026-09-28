package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/joehahndev/drupaltui/internal/api"
	"github.com/joehahndev/drupaltui/internal/editor"
)

type editStep int

const (
	editStepTitle editStep = iota
	editStepConfirm
	editStepSaving
)

// editModel drives create/edit: title in-TUI, body via the external editor,
// then a confirm screen with markdown preview and revision log.
type editModel struct {
	inited bool
	client *api.Client
	ct     api.ContentType
	format string

	existing *api.Node // nil when creating
	step     editStep

	titleInput textinput.Model
	logInput   textinput.Model
	logFocused bool

	pendingBody string
	bodyChanged bool

	preview        viewport.Model
	discardConfirm bool

	width, height int
}

func newEditModel(client *api.Client, ct api.ContentType, format string, existing *api.Node, width, height int) editModel {
	ti := textinput.New()
	ti.Prompt = "Title > "
	ti.CharLimit = 255
	ti.Focus()

	li := textinput.New()
	li.Prompt = "Revision log > "
	li.CharLimit = 255
	defaultLog := "Created via drupaltui"
	if existing != nil {
		ti.SetValue(existing.Title)
		defaultLog = "Edited via drupaltui"
	}
	li.Placeholder = defaultLog

	m := editModel{
		inited:     true,
		client:     client,
		ct:         ct,
		format:     format,
		existing:   existing,
		titleInput: ti,
		logInput:   li,
		width:      width,
		height:     height,
	}
	if existing != nil {
		m.pendingBody = existing.Body
	}
	m.preview = viewport.New(width, m.previewHeight())
	return m
}

func (m editModel) creating() bool { return m.existing == nil }

// bodyEditable reports whether this flow should open the body editor. On
// create we can't know if the bundle has a body field, so we offer the
// editor and simply omit an empty body from the payload.
func (m editModel) bodyEditable() bool {
	return m.creating() || m.existing.HasBody
}

func (m *editModel) previewHeight() int {
	h := m.height - 7 // header, title, log input, help
	if h < 3 {
		h = 3
	}
	return h
}

func (m *editModel) resize(w, h int) {
	if !m.inited {
		return
	}
	m.width, m.height = w, h
	m.preview.Width = w
	m.preview.Height = m.previewHeight()
}

// onError returns the flow to the confirm step after a failed save so the
// pending content is preserved.
func (m *editModel) onError() {
	if m.step == editStepSaving {
		m.step = editStepConfirm
	}
}

func launchEditorCmd(initial string) tea.Cmd {
	s, err := editor.NewSession(initial)
	if err != nil {
		return func() tea.Msg { return editorFinishedMsg{err: err} }
	}
	cmd, err := s.Command()
	if err != nil {
		s.Cleanup()
		return func() tea.Msg { return editorFinishedMsg{err: err} }
	}
	return tea.ExecProcess(cmd, func(execErr error) tea.Msg {
		defer s.Cleanup()
		if execErr != nil {
			return editorFinishedMsg{err: fmt.Errorf("editor: %w", execErr)}
		}
		body, changed, err := s.Result()
		if err != nil {
			return editorFinishedMsg{err: err}
		}
		return editorFinishedMsg{body: body, changed: changed}
	})
}

func (m editModel) saveCmd() tea.Cmd {
	client, ct := m.client, m.ct
	creating := m.creating()

	in := api.NodeInput{Title: strings.TrimSpace(m.titleInput.Value())}
	includeBody := false
	if creating {
		includeBody = strings.TrimSpace(m.pendingBody) != ""
	} else {
		includeBody = m.existing.HasBody && m.bodyChanged
	}
	if includeBody {
		body := m.pendingBody
		in.Body = &body
		in.BodyFormat = m.format
	}
	if creating {
		unpublished := false
		in.Status = &unpublished // drafts start unpublished; publish explicitly
	}
	in.RevisionLog = strings.TrimSpace(m.logInput.Value())
	if in.RevisionLog == "" {
		in.RevisionLog = m.logInput.Placeholder
	}

	var uuid string
	if !creating {
		uuid = m.existing.UUID
	}

	return withCtx(func(ctx context.Context) tea.Msg {
		// Defense in depth: never write to a bundle without verified revisions,
		// even if a UI bug routed us here.
		if !ct.Editable() {
			return apiErrMsg{fmt.Errorf("refusing to write: %s", ct.ReadOnlyReason())}
		}
		if creating {
			node, err := client.CreateNode(ctx, ct.Machine, in)
			if err != nil {
				return apiErrMsg{err}
			}
			return nodeSavedMsg{node: node, created: true}
		}
		node, err := client.UpdateNode(ctx, ct.Machine, uuid, in)
		if err != nil {
			return apiErrMsg{err}
		}
		return nodeSavedMsg{node: node, created: false}
	})
}

func (m editModel) update(msg tea.Msg) (editModel, tea.Cmd) {
	switch msg := msg.(type) {
	case editorFinishedMsg:
		if msg.err != nil {
			return m, func() tea.Msg { return apiErrMsg{msg.err} }
		}
		m.pendingBody = msg.body
		if msg.changed {
			m.bodyChanged = true
		}
		m.step = editStepConfirm
		return m, renderMarkdownCmd("edit", m.pendingBody, m.width)

	case mdRenderedMsg:
		if msg.target == "edit" {
			m.preview.SetContent(msg.out)
		}
		return m, nil

	case tea.KeyMsg:
		switch m.step {
		case editStepTitle:
			return m.updateTitleStep(msg)
		case editStepConfirm:
			return m.updateConfirmStep(msg)
		case editStepSaving:
			return m, nil
		}
	}

	if m.step == editStepConfirm && !m.logFocused {
		var cmd tea.Cmd
		m.preview, cmd = m.preview.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m editModel) updateTitleStep(key tea.KeyMsg) (editModel, tea.Cmd) {
	switch key.String() {
	case "enter":
		if strings.TrimSpace(m.titleInput.Value()) == "" {
			return m, func() tea.Msg { return statusNoteMsg{"Title is required."} }
		}
		if !m.bodyEditable() {
			m.step = editStepConfirm
			m.preview.SetContent(dimStyle.Render("\n  (this content type has no body field)"))
			return m, nil
		}
		return m, launchEditorCmd(m.pendingBody)
	case "esc":
		return m, func() tea.Msg { return goBackMsg{} }
	}
	var cmd tea.Cmd
	m.titleInput, cmd = m.titleInput.Update(key)
	return m, cmd
}

func (m editModel) updateConfirmStep(key tea.KeyMsg) (editModel, tea.Cmd) {
	if m.discardConfirm {
		switch key.String() {
		case "y":
			return m, func() tea.Msg { return goBackMsg{} }
		case "n", "esc":
			m.discardConfirm = false
		}
		return m, nil
	}

	if m.logFocused {
		switch key.String() {
		case "enter", "esc":
			m.logFocused = false
			m.logInput.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.logInput, cmd = m.logInput.Update(key)
		return m, cmd
	}

	switch key.String() {
	case "y", "ctrl+s":
		m.step = editStepSaving
		return m, m.saveCmd()
	case "e":
		if m.bodyEditable() {
			return m, launchEditorCmd(m.pendingBody)
		}
		return m, nil
	case "t":
		m.step = editStepTitle
		m.titleInput.Focus()
		return m, nil
	case "l":
		m.logFocused = true
		m.logInput.Focus()
		return m, nil
	case "esc":
		m.discardConfirm = true
		return m, nil
	}

	var cmd tea.Cmd
	m.preview, cmd = m.preview.Update(key)
	return m, cmd
}

func (m editModel) view() string {
	action := "Edit"
	if m.creating() {
		action = "New"
	}
	header := titleStyle.Render(fmt.Sprintf("%s %s", action, m.ct.Name))

	switch m.step {
	case editStepTitle:
		return header + "\n\n  " + m.titleInput.View() + "\n\n" +
			helpStyle.Render("  enter continue to body editor · esc cancel")

	case editStepSaving:
		return header + "\n\n  Saving…"

	default: // confirm
		title := headerStyle.Render(strings.TrimSpace(m.titleInput.Value()))
		var footer string
		switch {
		case m.discardConfirm:
			footer = promptStyle.Render("Discard changes? (y/n)")
		case m.logFocused:
			footer = helpStyle.Render("enter/esc done editing log")
		default:
			note := ""
			if m.creating() {
				note = dimStyle.Render("will be created unpublished · ")
			}
			footer = note + helpStyle.Render("y save · e edit body · t edit title · l revision log · esc discard")
		}
		return header + "  " + title + "\n" +
			m.preview.View() + "\n" +
			"  " + m.logInput.View() + "\n" +
			footer
	}
}
