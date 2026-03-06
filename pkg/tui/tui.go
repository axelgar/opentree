package tui

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/github"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/tmux"
	"github.com/axelgar/opentree/pkg/worktree"
)

// ansiEscapeRe strips ANSI escape sequences from tmux pane output.
var ansiEscapeRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b[()][0-9A-Za-z]`)

// Styles
var (
	appStyle = lipgloss.NewStyle().Padding(1, 2)

	titleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFF7DB")).
			Background(lipgloss.Color("#888B7E")).
			Padding(0, 1)

	statusStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#DDD")).
			Padding(0, 1)

	selectedItemStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder(), false, false, false, true).
				BorderForeground(lipgloss.Color("#F4A261")). // Orange accent
				Foreground(lipgloss.Color("#F4A261")).
				Padding(0, 1)

	itemStyle = lipgloss.NewStyle().
			Padding(0, 1)

	diffStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#555"))

	activeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#2A9D8F")) // Teal

	idleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E9C46A")) // Yellow

	stoppedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666")) // Grey

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			MarginTop(1)

	mergedBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("#6E40C9")).
				Padding(0, 1)

	prOpenBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("#1F7A4D")).
				Padding(0, 1)

	issueBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#FFF")).
				Background(lipgloss.Color("#0969DA")).
				Padding(0, 1)

	// Improvement 2: agent preview panel styles
	previewBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#444")).
			Padding(0, 1).
			MarginTop(1)

	previewTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888"))

	previewLineStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#AAA"))

	// Improvement 1: delete confirmation styles
	dangerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196"))

	confirmKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F4A261")).
			Bold(true)

	// Improvement 4: two-step create dialog
	stepLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888")).
			Italic(true)
)

type keyMap struct {
	Up     key.Binding
	Down   key.Binding
	New    key.Binding
	Enter  key.Binding
	Diff   key.Binding
	PR     key.Binding
	Open   key.Binding // Improvement 5: open PR in browser
	Delete key.Binding
	Quit   key.Binding
	Help   key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.New, k.Enter, k.Diff, k.Delete, k.Quit, k.Help}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.New, k.Enter},
		{k.Diff, k.PR, k.Open, k.Delete, k.Quit, k.Help},
	}
}

var keys = keyMap{
	Up: key.NewBinding(
		key.WithKeys("up", "k"),
		key.WithHelp("↑/k", "up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down", "j"),
		key.WithHelp("↓/j", "down"),
	),
	New: key.NewBinding(
		key.WithKeys("n"),
		key.WithHelp("n", "new workspace"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("enter", "attach"),
	),
	Diff: key.NewBinding(
		key.WithKeys("d"),
		key.WithHelp("d", "diff"),
	),
	PR: key.NewBinding(
		key.WithKeys("p"),
		key.WithHelp("p", "create PR"),
	),
	Open: key.NewBinding(
		key.WithKeys("o"),
		key.WithHelp("o", "open PR in browser"),
	),
	Delete: key.NewBinding(
		key.WithKeys("x"),
		key.WithHelp("x", "delete"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "toggle help"),
	),
}

type WorkspaceItem struct {
	*state.Workspace
	DiffStat string
	Active   bool
	WindowID string
}

type Model struct {
	worktreeMgr *worktree.Manager
	tmuxCtrl    *tmux.Controller
	stateStore  *state.Store
	prMgr       *github.PRManager
	cfg         *config.Config

	workspaces []WorkspaceItem
	cursor     int
	width      int
	height     int

	// Improvement 4: two-step create dialog
	input         textinput.Model
	creating      bool
	createStep    int    // 0 = branch name, 1 = base branch
	newBranchName string // stores branch name between steps

	// Improvement 1: delete confirmation
	deleting     bool
	deleteTarget string

	// Improvement 2: agent output preview
	agentPreview string

	help help.Model
	keys keyMap

	err error
}

func NewModel() (*Model, error) {
	wt := worktree.New()
	st, err := state.New(".")
	if err != nil {
		// Try to find git root if "." fails
		if wd, err2 := os.Getwd(); err2 == nil {
			st, err = state.New(wd)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to initialize state: %w", err)
		}
	}
	cfg, err := config.Load("")
	if err != nil {
		cfg = config.Default()
	}
	tm := tmux.New(cfg.Tmux.SessionPrefix)

	ti := textinput.New()
	ti.Placeholder = "New branch name"
	ti.CharLimit = 50
	ti.Width = 30

	return &Model{
		worktreeMgr: wt,
		tmuxCtrl:    tm,
		stateStore:  st,
		prMgr:       github.New(),
		cfg:         cfg,
		input:       ti,
		help:        help.New(),
		keys:        keys,
	}, nil
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.loadWorkspacesCmd,
		tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return prStatusTickMsg{} }),
		// Improvement 3: auto-refresh workspace status every 10 seconds
		tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return refreshTickMsg{} }),
		// Improvement 2: agent preview refresh every 5 seconds
		tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return previewTickMsg{} }),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.help.Width = msg.Width

	case tea.KeyMsg:
		// Improvement 1: delete confirmation mode takes priority
		if m.deleting {
			switch msg.String() {
			case "y", "Y":
				target := m.deleteTarget
				m.deleting = false
				m.deleteTarget = ""
				return m, m.deleteWorkspaceCmd(target)
			case "n", "esc":
				m.deleting = false
				m.deleteTarget = ""
			}
			return m, nil
		}

		// Improvement 4: two-step create dialog
		if m.creating {
			switch msg.String() {
			case "enter":
				val := m.input.Value()
				if val == "" {
					return m, nil
				}
				if m.createStep == 0 {
					// Advance to step 2: collect base branch
					m.newBranchName = val
					m.createStep = 1
					m.input.Placeholder = "Base branch"
					m.input.SetValue(m.cfg.Worktree.DefaultBase)
					return m, textinput.Blink
				}
				// Step 2 confirmed: create workspace
				branchName := m.newBranchName
				baseBranch := val
				m.creating = false
				m.createStep = 0
				m.newBranchName = ""
				m.input.SetValue("")
				m.input.Placeholder = "New branch name"
				return m, m.createWorkspaceCmd(branchName, baseBranch)
			case "esc":
				m.creating = false
				m.createStep = 0
				m.newBranchName = ""
				m.input.SetValue("")
				m.input.Placeholder = "New branch name"
				return m, nil
			}
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		// Normal mode
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
				return m, m.capturePreviewCmd()
			}
		case key.Matches(msg, m.keys.Down):
			if m.cursor < len(m.workspaces)-1 {
				m.cursor++
				return m, m.capturePreviewCmd()
			}
		case key.Matches(msg, m.keys.New):
			m.creating = true
			m.createStep = 0
			m.input.Placeholder = "New branch name"
			m.input.SetValue("")
			m.input.Focus()
			return m, textinput.Blink
		case key.Matches(msg, m.keys.Enter):
			if len(m.workspaces) > 0 {
				ws := m.workspaces[m.cursor]
				return m, m.attachWorkspaceCmd(ws.Name)
			}
		case key.Matches(msg, m.keys.Diff):
			return m, m.loadWorkspacesCmd
		case key.Matches(msg, m.keys.PR):
			if len(m.workspaces) > 0 {
				ws := m.workspaces[m.cursor]
				return m, m.createPRCmd(ws.Name, ws.Branch, ws.BaseBranch)
			}
		// Improvement 5: open PR URL in browser
		case key.Matches(msg, m.keys.Open):
			if len(m.workspaces) > 0 {
				ws := m.workspaces[m.cursor]
				if ws.PRURL != "" {
					return m, openURLCmd(ws.PRURL)
				}
			}
		// Improvement 1: show confirmation instead of immediate delete
		case key.Matches(msg, m.keys.Delete):
			if len(m.workspaces) > 0 {
				ws := m.workspaces[m.cursor]
				m.deleting = true
				m.deleteTarget = ws.Name
			}
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
		}

	case loadedWorkspacesMsg:
		m.workspaces = msg.workspaces
		if m.cursor >= len(m.workspaces) {
			m.cursor = max(0, len(m.workspaces)-1)
		}
		// Refresh preview for the newly selected workspace
		return m, m.capturePreviewCmd()

	case createdWorkspaceMsg:
		return m, m.loadWorkspacesCmd

	case deletedWorkspaceMsg:
		return m, m.loadWorkspacesCmd

	// Improvement 2: agent preview received
	case capturePreviewMsg:
		m.agentPreview = msg.lines

	// Improvement 3: periodic workspace status refresh
	case refreshTickMsg:
		return m, tea.Batch(
			m.loadWorkspacesCmd,
			tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return refreshTickMsg{} }),
		)

	// Improvement 2: periodic preview refresh
	case previewTickMsg:
		return m, tea.Batch(
			m.capturePreviewCmd(),
			tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return previewTickMsg{} }),
		)

	case prCreatedMsg:
		ws, err := m.stateStore.GetWorkspace(msg.wsName)
		if err == nil {
			ws.PRURL = msg.prURL
			ws.PRStatus = "open"
			_ = m.stateStore.UpdateWorkspace(ws)
		}
		return m, tea.Batch(m.loadWorkspacesCmd, m.checkPRStatusCmd(msg.wsName, ""))

	case prStatusTickMsg:
		cmds := []tea.Cmd{
			tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return prStatusTickMsg{} }),
		}
		for _, ws := range m.workspaces {
			if ws.PRURL != "" && ws.PRStatus != "merged" {
				cmds = append(cmds, m.checkPRStatusCmd(ws.Name, ws.Branch))
			}
		}
		return m, tea.Batch(cmds...)

	case prStatusCheckedMsg:
		ws, err := m.stateStore.GetWorkspace(msg.wsName)
		if err == nil {
			ws.PRURL = msg.prURL
			ws.PRStatus = msg.prStatus
			_ = m.stateStore.UpdateWorkspace(ws)
		}
		for i, item := range m.workspaces {
			if item.Name == msg.wsName {
				m.workspaces[i].PRURL = msg.prURL
				m.workspaces[i].PRStatus = msg.prStatus
				break
			}
		}

	case attachFinishedMsg:
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearErrorMsg{}
			})
		}
		return m, m.loadWorkspacesCmd

	case errMsg:
		m.err = msg.err
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearErrorMsg{}
		})

	case clearErrorMsg:
		m.err = nil
	}

	return m, cmd
}

func (m Model) View() string {
	// Improvement 1: delete confirmation dialog
	if m.deleting {
		body := dangerStyle.Render(fmt.Sprintf("Delete workspace %q?", m.deleteTarget)) +
			"\n" +
			helpStyle.Render("The worktree, tmux window, and all local changes will be removed.")
		footer := fmt.Sprintf("%s %s  •  %s %s",
			confirmKeyStyle.Render("y"), helpStyle.Render("confirm"),
			confirmKeyStyle.Render("esc/n"), helpStyle.Render("cancel"),
		)
		return appStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s",
			titleStyle.Render("Delete Workspace"),
			body,
			footer,
		))
	}

	// Improvement 4: two-step create dialog
	if m.creating {
		var stepLabel string
		if m.createStep == 0 {
			stepLabel = "Step 1/2 — Branch name"
		} else {
			stepLabel = fmt.Sprintf("Step 2/2 — Base branch  (branching from: %s)", m.newBranchName)
		}
		return appStyle.Render(fmt.Sprintf("%s\n\n%s\n%s\n\n%s",
			titleStyle.Render("Create New Workspace"),
			stepLabelStyle.Render(stepLabel),
			m.input.View(),
			helpStyle.Render("Enter to continue • Esc to cancel"),
		))
	}

	var s strings.Builder

	// Header
	s.WriteString(titleStyle.Render("OpenTree Workspaces"))
	s.WriteString("\n\n")

	// Error message
	if m.err != nil {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(fmt.Sprintf("Error: %v", m.err)))
		s.WriteString("\n\n")
	}

	// Workspace list
	if len(m.workspaces) == 0 {
		s.WriteString(itemStyle.Render("No workspaces found. Press 'n' to create one."))
		s.WriteString("\n")
	} else {
		for i, ws := range m.workspaces {
			style := itemStyle
			if i == m.cursor {
				style = selectedItemStyle
			}
			status := "○"
			statusColor := stoppedStyle
			if ws.Active {
				status = "●"
				statusColor = activeStyle
			} else if ws.WindowID != "" {
				status = "◎"
				statusColor = idleStyle
			}

			title := fmt.Sprintf("%s %s", statusColor.Render(status), ws.Name)
			if ws.IssueNumber > 0 {
				title += "  " + issueBadgeStyle.Render(fmt.Sprintf("#%d", ws.IssueNumber))
			}
			if ws.PRStatus == "merged" {
				title += "  " + mergedBadgeStyle.Render("merged · ready to delete")
			} else if ws.PRStatus == "open" {
				title += "  " + prOpenBadgeStyle.Render("PR open")
			}
			desc := fmt.Sprintf("  %s • %s • %s", ws.Branch, ws.DiffStat, formatAge(ws.CreatedAt))

			s.WriteString(style.Render(fmt.Sprintf("%s\n%s", title, diffStyle.Render(desc))))
			s.WriteString("\n")
		}

		// Improvement 2: agent output preview for selected workspace
		if m.agentPreview != "" {
			wsName := m.workspaces[m.cursor].Name
			previewWidth := m.width - 8
			if previewWidth < 20 {
				previewWidth = 60
			}
			content := previewTitleStyle.Render("Agent Output: "+wsName) + "\n" +
				previewLineStyle.Render(m.agentPreview)
			s.WriteString(previewBoxStyle.Width(previewWidth).Render(content))
			s.WriteString("\n")
		}
	}

	// Footer
	s.WriteString("\n")
	s.WriteString(m.help.View(m.keys))

	return appStyle.Render(s.String())
}

// Messages

type loadedWorkspacesMsg struct {
	workspaces []WorkspaceItem
}

type createdWorkspaceMsg struct{}
type deletedWorkspaceMsg struct{}
type errMsg struct{ err error }
type clearErrorMsg struct{}
type attachFinishedMsg struct{ err error }
type prStatusTickMsg struct{}
type prCreatedMsg struct{ wsName, prURL string }
type prStatusCheckedMsg struct {
	wsName   string
	prURL    string
	prStatus string
}
type refreshTickMsg struct{}    // Improvement 3
type previewTickMsg struct{}    // Improvement 2
type capturePreviewMsg struct { // Improvement 2
	lines string
}

// Commands

func (m Model) loadWorkspacesCmd() tea.Msg {
	saved := m.stateStore.ListWorkspaces()

	windows, err := m.tmuxCtrl.ListWindows()
	if err != nil {
		// Log error but continue
	}

	windowMap := make(map[string]tmux.Window)
	for _, w := range windows {
		windowMap[w.Name] = w
	}

	var items []WorkspaceItem
	for _, ws := range saved {
		diff, _ := m.worktreeMgr.Diff(ws.Branch)
		diffStat := "No changes"
		lines := strings.Split(strings.TrimSpace(diff), "\n")
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			diffStat = lines[len(lines)-1]
		}

		win, exists := windowMap[ws.Name]
		sanitizedName := strings.ReplaceAll(ws.Name, "/", "-")
		sanitizedName = strings.ReplaceAll(sanitizedName, ":", "-")
		if !exists {
			win, exists = windowMap[sanitizedName]
		}

		item := WorkspaceItem{
			Workspace: ws,
			DiffStat:  diffStat,
			Active:    exists && win.Active,
			WindowID:  "",
		}
		if exists {
			item.WindowID = win.ID
		}

		items = append(items, item)
	}

	return loadedWorkspacesMsg{workspaces: items}
}

// Improvement 4: createWorkspaceCmd now accepts baseBranch instead of hardcoding "main"
func (m Model) createWorkspaceCmd(name, baseBranch string) tea.Cmd {
	return func() tea.Msg {
		if err := m.worktreeMgr.Create(name, baseBranch); err != nil {
			return errMsg{err}
		}

		dirName := strings.ReplaceAll(name, "/", "-")
		wd, _ := os.Getwd()
		worktreePath := fmt.Sprintf("%s/.opentree/%s", wd, dirName)

		agentCmd := m.cfg.Agent.Command
		if err := m.tmuxCtrl.CreateWindow(name, worktreePath, agentCmd, m.cfg.Agent.Args...); err != nil {
			return errMsg{err}
		}

		ws := &state.Workspace{
			Name:        name,
			Branch:      name,
			BaseBranch:  baseBranch,
			CreatedAt:   time.Now(),
			Status:      "active",
			Agent:       agentCmd,
			WorktreeDir: worktreePath,
		}
		if err := m.stateStore.AddWorkspace(ws); err != nil {
			return errMsg{err}
		}

		return createdWorkspaceMsg{}
	}
}

func (m Model) deleteWorkspaceCmd(name string) tea.Cmd {
	return func() tea.Msg {
		if err := m.tmuxCtrl.KillWindow(name); err != nil {
			// Continue even if window doesn't exist
		}

		if err := m.worktreeMgr.Delete(name, true); err != nil {
			return errMsg{err}
		}

		if err := m.stateStore.DeleteWorkspace(name); err != nil {
			return errMsg{err}
		}

		return deletedWorkspaceMsg{}
	}
}

func (m Model) attachWorkspaceCmd(name string) tea.Cmd {
	return func() tea.Msg {
		cmd, err := m.tmuxCtrl.AttachCmd(name)
		if err != nil {
			return errMsg{err}
		}
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			return attachFinishedMsg{err: err}
		})()
	}
}

func (m Model) createPRCmd(wsName, branch, baseBranch string) tea.Cmd {
	return func() tea.Msg {
		prURL, err := m.prMgr.CreatePR(branch, baseBranch, "", "")
		if err != nil {
			return errMsg{err}
		}
		return prCreatedMsg{wsName: wsName, prURL: prURL}
	}
}

func (m Model) checkPRStatusCmd(wsName, branch string) tea.Cmd {
	return func() tea.Msg {
		prURL, prStatus, err := m.prMgr.GetFullPRStatus(branch)
		if err != nil || prURL == "" {
			return nil
		}
		return prStatusCheckedMsg{wsName: wsName, prURL: prURL, prStatus: prStatus}
	}
}

// Improvement 2: capturePreviewCmd fetches the last 5 lines of agent output for the selected workspace.
func (m Model) capturePreviewCmd() tea.Cmd {
	if len(m.workspaces) == 0 {
		return nil
	}
	ws := m.workspaces[m.cursor]
	if ws.WindowID == "" {
		return func() tea.Msg { return capturePreviewMsg{lines: ""} }
	}
	wsName := ws.Name
	return func() tea.Msg {
		output, err := m.tmuxCtrl.CapturePane(wsName, 5)
		if err != nil {
			return capturePreviewMsg{lines: ""}
		}
		return capturePreviewMsg{lines: cleanPreview(output)}
	}
}

// cleanPreview strips ANSI codes and returns the last 5 non-empty lines.
func cleanPreview(s string) string {
	s = ansiEscapeRe.ReplaceAllString(s, "")
	lines := strings.Split(s, "\n")
	var out []string
	for _, l := range lines {
		if trimmed := strings.TrimRight(l, " \t"); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) > 5 {
		out = out[len(out)-5:]
	}
	return strings.Join(out, "\n")
}

// openURLCmd opens a URL in the system default browser (fire-and-forget).
func openURLCmd(url string) tea.Cmd {
	return func() tea.Msg {
		var cmd *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			cmd = exec.Command("open", url)
		case "windows":
			cmd = exec.Command("cmd", "/c", "start", url)
		default:
			cmd = exec.Command("xdg-open", url)
		}
		_ = cmd.Start()
		return nil
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// formatAge returns a human-readable age string for a given timestamp.
func formatAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		m := int(d.Minutes())
		return fmt.Sprintf("%dm ago", m)
	case d < 24*time.Hour:
		h := int(d.Hours())
		return fmt.Sprintf("%dh ago", h)
	default:
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	}
}

func Run() error {
	m, err := NewModel()
	if err != nil {
		return err
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return err
	}
	return nil
}
