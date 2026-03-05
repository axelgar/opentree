package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"


	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/tmux"
	"github.com/axelgar/opentree/pkg/worktree"
)

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
)

type keyMap struct {
	Up     key.Binding
	Down   key.Binding
	New    key.Binding
	Enter  key.Binding
	Diff   key.Binding
	PR     key.Binding
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
		{k.Diff, k.PR, k.Delete, k.Quit, k.Help},
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

	workspaces []WorkspaceItem
	cursor     int
	width      int
	height     int

	input    textinput.Model
	creating bool
	help     help.Model
	keys     keyMap

	err error
}

func NewModel() (*Model, error) {
	wt := worktree.New()
	st, err := state.New(".")
	if err != nil {
		// Try to find git root if "." fails
		if wd, err := os.Getwd(); err == nil {
			st, err = state.New(wd)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to initialize state: %w", err)
		}
	}
	tm := tmux.New("opentree")

	ti := textinput.New()
	ti.Placeholder = "New branch name"
	ti.CharLimit = 50
	ti.Width = 30

	return &Model{
		worktreeMgr: wt,
		tmuxCtrl:    tm,
		stateStore:  st,
		input:       ti,
		help:        help.New(),
		keys:        keys,
	}, nil
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.loadWorkspacesCmd,
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
		if m.creating {
			switch msg.String() {
			case "enter":
				name := m.input.Value()
				if name != "" {
					m.creating = false
					m.input.SetValue("")
					return m, m.createWorkspaceCmd(name)
				}
			case "esc":
				m.creating = false
				m.input.SetValue("")
				return m, nil
			}
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
			}
		case key.Matches(msg, m.keys.Down):
			if m.cursor < len(m.workspaces)-1 {
				m.cursor++
			}
		case key.Matches(msg, m.keys.New):
			m.creating = true
			m.input.Focus()
			return m, textinput.Blink
		case key.Matches(msg, m.keys.Enter):
			if len(m.workspaces) > 0 {
				ws := m.workspaces[m.cursor]
				return m, m.attachWorkspaceCmd(ws.Name)
			}
		case key.Matches(msg, m.keys.Diff):
			// For now, just refresh to update diff stats
			// In a real implementation, we might show a popup
			return m, m.loadWorkspacesCmd
		case key.Matches(msg, m.keys.Delete):
			if len(m.workspaces) > 0 {
				ws := m.workspaces[m.cursor]
				return m, m.deleteWorkspaceCmd(ws.Name)
			}
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
		}

	case loadedWorkspacesMsg:
		m.workspaces = msg.workspaces
		if m.cursor >= len(m.workspaces) {
			m.cursor = max(0, len(m.workspaces)-1)
		}

	case createdWorkspaceMsg:
		return m, m.loadWorkspacesCmd

	case deletedWorkspaceMsg:
		return m, m.loadWorkspacesCmd

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
		// Clear error after 3 seconds
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearErrorMsg{}
		})
		
	case clearErrorMsg:
		m.err = nil
	}

	return m, cmd
}

func (m Model) View() string {
	if m.creating {
		return appStyle.Render(
			fmt.Sprintf(
				"%s\n\n%s\n\n%s",
				titleStyle.Render("Create New Workspace"),
				m.input.View(),
				helpStyle.Render("Enter to confirm • Esc to cancel"),
			),
		)
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

	// List
	if len(m.workspaces) == 0 {
		s.WriteString(itemStyle.Render("No workspaces found. Press 'n' to create one."))
		s.WriteString("\n")
	} else {
		for i, ws := range m.workspaces {
			cursor := " "
			style := itemStyle
			if i == m.cursor {
				cursor = "│"
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

			title := fmt.Sprintf("%s %s %s", cursor, statusColor.Render(status), ws.Name)
			desc := fmt.Sprintf("   %s • %s", ws.Branch, ws.DiffStat)
			
			s.WriteString(style.Render(fmt.Sprintf("%s\n%s", title, diffStyle.Render(desc))))
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

// Commands

func (m Model) loadWorkspacesCmd() tea.Msg {
	// Get saved workspaces
	saved := m.stateStore.ListWorkspaces()
	
	// Get active windows
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
		// Get diff stats
		diff, _ := m.worktreeMgr.Diff(ws.Branch)
		diffStat := "No changes"
		lines := strings.Split(strings.TrimSpace(diff), "\n")
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			diffStat = lines[len(lines)-1]
		}

		// Check status
		win, exists := windowMap[ws.Name] // Assuming workspace name matches window name
		// Sanitize window name for lookup as tmux sanitizes it
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

func (m Model) createWorkspaceCmd(name string) tea.Cmd {
	return func() tea.Msg {
		// 1. Create git worktree
		if err := m.worktreeMgr.Create(name, "main"); err != nil {
			return errMsg{err}
		}

		// 2. Create tmux window with agent
		// Construct workspace path
		// We need to know where worktree created it.
		// Usually .opentree/<sanitized_name>
		dirName := strings.ReplaceAll(name, "/", "-")
		// Assume we can get repo root from somewhere or worktree returns it
		// For now let's assume standard path relative to CWD if we are in root
		// But better to ask worktree.Manager where the root is.
		// Since we don't have that exposed easily, let's just use the name.
		// The tmux command runs in the worktree dir.
		
		// We need the absolute path for tmux
		// worktree.Manager knows the path.
		// Let's rely on standard path structure for now: .opentree/<name>
		wd, _ := os.Getwd()
		worktreePath := fmt.Sprintf("%s/.opentree/%s", wd, dirName)

		if err := m.tmuxCtrl.CreateWindow(name, worktreePath, "opencode"); err != nil {
			return errMsg{err}
		}

		// 3. Save state
		ws := &state.Workspace{
			Name:        name,
			Branch:      name,
			BaseBranch:  "main",
			CreatedAt:   time.Now(),
			Status:      "active",
			Agent:       "opencode",
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
		// 1. Kill tmux window
		if err := m.tmuxCtrl.KillWindow(name); err != nil {
			// Continue even if window doesn't exist
		}

		// 2. Remove worktree
		if err := m.worktreeMgr.Delete(name, true); err != nil {
			return errMsg{err}
		}

		// 3. Remove from state
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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
