// Package ui implements the drupaltui terminal interface.
//
// Screen hierarchy is linear: connect → (format picker) → bundles → nodes →
// detail → edit; esc always goes one level up. All API calls run as tea.Cmd
// and report back via the typed messages below.
package ui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/joehahndev/drupaltui/internal/api"
	"github.com/joehahndev/drupaltui/internal/config"
)

const requestTimeout = 60 * time.Second

type screen int

const (
	screenConnect screen = iota
	screenFormatPicker
	screenBundles
	screenNodes
	screenDetail
	screenEdit
)

// ---- messages ----

type apiErrMsg struct{ err error }

type connectedMsg struct {
	types               []api.ContentType
	formats             []api.FilterFormat
	revisionCheckFailed bool
	formatsForbidden    bool
}

type nodesLoadedMsg struct {
	page   api.NodePage
	append bool
}

type nodeLoadedMsg struct {
	node    api.Node
	forEdit bool // open the edit flow instead of the detail view
}

type nodeSavedMsg struct {
	node    api.Node
	created bool
}

type statusChangedMsg struct{ node api.Node }

type mdRenderedMsg struct {
	target string // "detail" or "edit"
	out    string
}

type editorFinishedMsg struct {
	body    string
	changed bool
	err     error
}

// navigation messages emitted by sub-models
type goBackMsg struct{}
type openBundleMsg struct{ ct api.ContentType }
type openNodeMsg struct{ uuid string }
type editNodeMsg struct{ uuid string }
type startCreateMsg struct{}
type requestToggleMsg struct { // user confirmed publish/unpublish
	node      api.Node
	toPublish bool
}
type connectSubmitMsg struct{ baseURL, clientID, clientSecret string }
type formatChosenMsg struct{ machine string }
type statusNoteMsg struct{ text string } // transient info line
type loadMoreMsg struct{ next string }   // fetch the next page (appends)
type reloadNodesMsg struct{ filter string }

// ---- root model ----

type App struct {
	cfg       *config.Config
	cfgPath   string
	envSecret string

	client *api.Client
	scr    screen

	types               []api.ContentType
	formats             []api.FilterFormat
	revisionCheckFailed bool

	connect connectModel
	picker  pickerModel
	bundles bundlesModel
	nodes   nodesModel
	detail  detailModel
	edit    editModel

	spinner spinner.Model
	loading bool
	status  string
	isErr   bool

	width, height int
}

func NewApp(cfg *config.Config, cfgPath, envClientSecret string) *App {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	a := &App{
		cfg:       cfg,
		cfgPath:   cfgPath,
		envSecret: envClientSecret,
		spinner:   sp,
		scr:       screenConnect,
	}
	a.connect = newConnectModel(cfg, envClientSecret)
	return a
}

func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{a.spinner.Tick, tea.SetWindowTitle("drupaltui")}
	if cmd := a.connect.autoSubmit(); cmd != nil {
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// contentType finds a bundle's ContentType by machine name.
func (a *App) contentType(machine string) api.ContentType {
	for _, ct := range a.types {
		if ct.Machine == machine {
			return ct
		}
	}
	return api.ContentType{Machine: machine}
}

func (a *App) setError(err error) {
	a.loading = false
	a.status = api.UserMessage(err)
	a.isErr = true
}

func (a *App) note(text string) {
	a.status = text
	a.isErr = false
}

// ---- commands ----

func withCtx(fn func(ctx context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		return fn(ctx)
	}
}

func connectCmd(client *api.Client) tea.Cmd {
	return withCtx(func(ctx context.Context) tea.Msg {
		bundles, err := client.Ping(ctx)
		if err != nil {
			return apiErrMsg{err}
		}
		var msg connectedMsg
		types, err := client.ListContentTypes(ctx)
		switch {
		case err == nil:
			msg.types = types
		case errors.Is(err, api.ErrRevisionCheckForbidden):
			msg.revisionCheckFailed = true
			for _, b := range bundles {
				msg.types = append(msg.types, api.ContentType{Machine: b, Name: b, Verified: false})
			}
		default:
			return apiErrMsg{err}
		}
		formats, err := client.ListFilterFormats(ctx)
		switch {
		case err == nil:
			msg.formats = formats
		case api.IsForbidden(err):
			msg.formatsForbidden = true
		default:
			return apiErrMsg{err}
		}
		return msg
	})
}

func loadNodesCmd(client *api.Client, bundle string, opts api.ListOpts, appendPage bool) tea.Cmd {
	return withCtx(func(ctx context.Context) tea.Msg {
		page, err := client.ListNodes(ctx, bundle, opts)
		if err != nil {
			return apiErrMsg{err}
		}
		return nodesLoadedMsg{page: page, append: appendPage}
	})
}

func loadNodeCmd(client *api.Client, bundle, uuid string, forEdit bool) tea.Cmd {
	return withCtx(func(ctx context.Context) tea.Msg {
		node, err := client.GetNode(ctx, bundle, uuid)
		if err != nil {
			return apiErrMsg{err}
		}
		return nodeLoadedMsg{node: node, forEdit: forEdit}
	})
}

// toggleStatusCmd re-checks the revision gate before writing: even if a UI
// bug lets the request through, no PATCH is issued for a non-editable bundle.
func toggleStatusCmd(client *api.Client, ct api.ContentType, node api.Node, toPublish bool) tea.Cmd {
	return withCtx(func(ctx context.Context) tea.Msg {
		if !ct.Editable() {
			return apiErrMsg{fmt.Errorf("refusing to write: %s", ct.ReadOnlyReason())}
		}
		verb := "Unpublished"
		if toPublish {
			verb = "Published"
		}
		updated, err := client.SetStatus(ctx, ct.Machine, node.UUID, toPublish, verb+" via drupaltui")
		if err != nil {
			return apiErrMsg{err}
		}
		return statusChangedMsg{node: updated}
	})
}

// ---- update ----

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.resizeAll()
		if a.scr == screenDetail {
			return a, a.detail.rerender()
		}
		return a, nil

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return a, tea.Quit
		}
		if a.status != "" {
			a.status = ""
			a.isErr = false
		}
		if a.loading {
			return a, nil // ignore input while a request is in flight
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		a.spinner, cmd = a.spinner.Update(msg)
		return a, cmd

	case apiErrMsg:
		a.setError(msg.err)
		if a.scr == screenEdit {
			a.edit.onError() // back to confirm; the pending content is preserved
		}
		return a, nil

	case statusNoteMsg:
		a.note(msg.text)
		return a, nil

	case connectSubmitMsg:
		client, err := api.New(msg.baseURL, msg.clientID, msg.clientSecret, a.cfg.PageLimit)
		if err != nil {
			a.setError(err)
			return a, nil
		}
		a.client = client
		a.cfg.BaseURL = msg.baseURL
		a.cfg.ClientID = msg.clientID
		a.loading = true
		return a, connectCmd(client)

	case connectedMsg:
		return a.handleConnected(msg)

	case formatChosenMsg:
		a.cfg.TextFormat = msg.machine
		if err := a.cfg.Save(a.cfgPath); err != nil {
			a.setError(fmt.Errorf("saving config: %w", err))
		} else {
			a.note("Saved config to " + a.cfgPath)
		}
		a.enterBundles()
		return a, nil

	case openBundleMsg:
		a.nodes = newNodesModel(msg.ct, a.width, a.contentHeight())
		a.scr = screenNodes
		a.loading = true
		return a, loadNodesCmd(a.client, msg.ct.Machine, api.ListOpts{}, false)

	case nodesLoadedMsg:
		a.loading = false
		a.nodes.setPage(msg.page, msg.append)
		return a, nil

	case loadMoreMsg:
		a.loading = true
		return a, loadNodesCmd(a.client, a.nodes.ct.Machine, api.ListOpts{NextURL: msg.next}, true)

	case reloadNodesMsg:
		a.nodes.activeFilter = msg.filter
		a.loading = true
		return a, loadNodesCmd(a.client, a.nodes.ct.Machine, api.ListOpts{TitleContains: msg.filter}, false)

	case openNodeMsg:
		a.loading = true
		return a, loadNodeCmd(a.client, a.nodes.ct.Machine, msg.uuid, false)

	case editNodeMsg:
		ct := a.nodes.ct
		if !ct.Editable() {
			a.note(ct.ReadOnlyReason())
			return a, nil
		}
		a.loading = true
		return a, loadNodeCmd(a.client, ct.Machine, msg.uuid, true)

	case nodeLoadedMsg:
		a.loading = false
		if msg.forEdit {
			a.edit = newEditModel(a.client, a.nodes.ct, a.cfg.TextFormat, &msg.node, a.width, a.contentHeight())
			a.scr = screenEdit
			return a, nil
		}
		a.detail = newDetailModel(a.nodes.ct, msg.node, a.width, a.contentHeight())
		a.scr = screenDetail
		return a, a.detail.rerender()

	case startCreateMsg:
		ct := a.nodes.ct
		if !ct.Editable() {
			a.note(ct.ReadOnlyReason())
			return a, nil
		}
		a.edit = newEditModel(a.client, ct, a.cfg.TextFormat, nil, a.width, a.contentHeight())
		a.scr = screenEdit
		return a, nil

	case requestToggleMsg:
		ct := a.nodes.ct
		if !ct.Editable() {
			a.note(ct.ReadOnlyReason())
			return a, nil
		}
		a.loading = true
		return a, toggleStatusCmd(a.client, ct, msg.node, msg.toPublish)

	case statusChangedMsg:
		a.loading = false
		verb := "Unpublished"
		if msg.node.Status {
			verb = "Published"
		}
		a.note(fmt.Sprintf("%s %q (new revision created)", verb, msg.node.Title))
		a.nodes.updateRow(msg.node)
		if a.scr == screenDetail {
			a.detail.node.Status = msg.node.Status
		}
		return a, nil

	case nodeSavedMsg:
		a.loading = false
		verb := "Updated"
		if msg.created {
			verb = "Created"
		}
		a.note(fmt.Sprintf("%s %q (new revision created)", verb, msg.node.Title))
		a.scr = screenNodes
		a.loading = true
		return a, loadNodesCmd(a.client, a.nodes.ct.Machine, api.ListOpts{TitleContains: a.nodes.activeFilter}, false)

	case goBackMsg:
		return a.goBack()
	}

	return a.routeToScreen(msg)
}

func (a *App) handleConnected(msg connectedMsg) (tea.Model, tea.Cmd) {
	a.loading = false
	a.types = msg.types
	a.formats = msg.formats
	a.revisionCheckFailed = msg.revisionCheckFailed

	// Persist the working connection details (never the password).
	if err := a.cfg.Save(a.cfgPath); err != nil {
		a.setError(fmt.Errorf("saving config: %w", err))
	}

	needPicker := a.cfg.TextFormat == ""
	if !needPicker && len(msg.formats) > 0 && !hasFormat(msg.formats, a.cfg.TextFormat) {
		a.note(fmt.Sprintf("Configured text format %q not found on the site — pick another.", a.cfg.TextFormat))
		needPicker = true
	}
	if needPicker {
		a.picker = newPickerModel(msg.formats, a.width, a.contentHeight())
		a.scr = screenFormatPicker
		return a, nil
	}
	if msg.formatsForbidden && a.cfg.TextFormat != "" {
		a.note(fmt.Sprintf("Can't list text formats (permission denied) — trusting configured %q.", a.cfg.TextFormat))
	}
	a.enterBundles()
	return a, nil
}

func hasFormat(formats []api.FilterFormat, machine string) bool {
	for _, f := range formats {
		if f.Machine == machine {
			return true
		}
	}
	return false
}

func (a *App) enterBundles() {
	a.bundles = newBundlesModel(a.types, a.revisionCheckFailed, a.width, a.contentHeight())
	a.scr = screenBundles
}

func (a *App) goBack() (tea.Model, tea.Cmd) {
	switch a.scr {
	case screenNodes:
		a.scr = screenBundles
	case screenDetail:
		a.scr = screenNodes
	case screenEdit:
		if a.edit.existing != nil {
			a.scr = screenDetail
		} else {
			a.scr = screenNodes
		}
	}
	return a, nil
}

func (a *App) routeToScreen(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch a.scr {
	case screenConnect:
		a.connect, cmd = a.connect.update(msg)
	case screenFormatPicker:
		a.picker, cmd = a.picker.update(msg)
	case screenBundles:
		a.bundles, cmd = a.bundles.update(msg)
	case screenNodes:
		a.nodes, cmd = a.nodes.update(msg)
	case screenDetail:
		a.detail, cmd = a.detail.update(msg)
	case screenEdit:
		a.edit, cmd = a.edit.update(msg)
	}
	return a, cmd
}

// ---- view ----

func (a *App) contentHeight() int {
	h := a.height - 2 // status bar + spacing
	if h < 3 {
		h = 3
	}
	return h
}

func (a *App) resizeAll() {
	w, h := a.width, a.contentHeight()
	a.picker.resize(w, h)
	a.bundles.resize(w, h)
	a.nodes.resize(w, h)
	a.detail.resize(w, h)
	a.edit.resize(w, h)
}

func (a *App) View() string {
	var body string
	switch a.scr {
	case screenConnect:
		body = a.connect.view()
	case screenFormatPicker:
		body = a.picker.view()
	case screenBundles:
		body = a.bundles.view()
	case screenNodes:
		body = a.nodes.view()
	case screenDetail:
		body = a.detail.view()
	case screenEdit:
		body = a.edit.view()
	}
	return lipgloss.JoinVertical(lipgloss.Left, body, a.statusBar())
}

func (a *App) statusBar() string {
	left := ""
	if a.cfg.BaseURL != "" {
		left = statusBarStyle.Render(a.cfg.BaseURL)
	}
	mid := ""
	switch {
	case a.loading:
		mid = a.spinner.View() + " loading…"
	case a.isErr:
		mid = errorStyle.Render(a.status)
	case a.status != "":
		mid = okStyle.Render(a.status)
	}
	if mid == "" {
		return left
	}
	return left + "  " + mid
}
