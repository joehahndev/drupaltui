package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/joehahndev/drupaltui/internal/config"
)

// connectModel collects site URL, OAuth client ID, and client secret. The
// secret is prefilled from the environment and lives only in memory.
type connectModel struct {
	inputs []textinput.Model // 0: url, 1: client id, 2: client secret
	focus  int
}

func newConnectModel(cfg *config.Config, envSecret string) connectModel {
	urlIn := textinput.New()
	urlIn.Placeholder = "https://example.com"
	urlIn.Prompt = "Site URL      > "
	urlIn.SetValue(cfg.BaseURL)
	urlIn.CharLimit = 300

	idIn := textinput.New()
	idIn.Prompt = "Client ID     > "
	idIn.SetValue(cfg.ClientID)
	idIn.CharLimit = 200

	secretIn := textinput.New()
	secretIn.Prompt = "Client secret > "
	secretIn.EchoMode = textinput.EchoPassword
	secretIn.SetValue(envSecret)
	secretIn.CharLimit = 300

	m := connectModel{inputs: []textinput.Model{urlIn, idIn, secretIn}}
	m.focusIndex(m.firstEmpty())
	return m
}

func (m connectModel) firstEmpty() int {
	for i, in := range m.inputs {
		if strings.TrimSpace(in.Value()) == "" {
			return i
		}
	}
	return len(m.inputs) - 1
}

func (m *connectModel) focusIndex(i int) {
	m.focus = i
	for j := range m.inputs {
		if j == i {
			m.inputs[j].Focus()
		} else {
			m.inputs[j].Blur()
		}
	}
}

func (m connectModel) complete() bool {
	for _, in := range m.inputs {
		if strings.TrimSpace(in.Value()) == "" {
			return false
		}
	}
	return true
}

// autoSubmit connects immediately when config + env secret cover all fields,
// skipping the form entirely.
func (m connectModel) autoSubmit() tea.Cmd {
	if !m.complete() {
		return nil
	}
	return m.submit()
}

func (m connectModel) submit() tea.Cmd {
	msg := connectSubmitMsg{
		baseURL:      strings.TrimSpace(m.inputs[0].Value()),
		clientID:     strings.TrimSpace(m.inputs[1].Value()),
		clientSecret: m.inputs[2].Value(),
	}
	return func() tea.Msg { return msg }
}

func (m connectModel) update(msg tea.Msg) (connectModel, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "enter":
			if m.focus < len(m.inputs)-1 {
				m.focusIndex(m.focus + 1)
				return m, nil
			}
			if !m.complete() {
				m.focusIndex(m.firstEmpty())
				return m, func() tea.Msg { return statusNoteMsg{"All fields are required."} }
			}
			return m, m.submit()
		case "tab", "down":
			m.focusIndex((m.focus + 1) % len(m.inputs))
			return m, nil
		case "shift+tab", "up":
			m.focusIndex((m.focus + len(m.inputs) - 1) % len(m.inputs))
			return m, nil
		case "esc":
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	return m, cmd
}

func (m connectModel) view() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("drupaltui — connect to your Drupal site"))
	b.WriteString("\n\n")
	for _, in := range m.inputs {
		b.WriteString("  " + in.View() + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("  enter next/connect · tab move · esc quit"))
	b.WriteString("\n" + dimStyle.Render("  OAuth client credentials (simple_oauth consumer). Set DRUPALTUI_CLIENT_SECRET\n"+
		"  to skip the secret prompt — it is never saved to disk."))
	return b.String()
}
