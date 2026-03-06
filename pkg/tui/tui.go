package tui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
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
				BorderForeground(lipgloss.Color("#F4A261")).
				Foreground(lipgloss.Color("#F4A261")).
				Padding(0, 1)

	itemStyle = lipgloss.NewStyle().
			Padding(0, 1)

	diffStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#555"))

	activeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#2A9D8F"))

	idleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E9C46A"))

	stoppedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666"))

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

	// agent preview panel styles
	previewBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#444")).
			Padding(0, 1).
			MarginTop(1)

	previewTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888"))

	previewLineStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#AAA"))

	// delete confirmation styles
	dangerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	deleteDialogStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("196")).
				Padding(1, 3)

	confirmKeyStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F4A261")).
			Bold(true)

	confirmLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#626262"))

	// two-step create dialog
	stepLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888")).
			Italic(true)

	// CI badge styles
	ciSuccessStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#2A9D8F")).
			Bold(true)

	ciFailureStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true)

	ciPendingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E9C46A"))

	// multi-select
	selectedMarkStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F4A261")).
				Bold(true)

	// filter prompt
	filterPromptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#F4A261"))

	// status bar
	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262"))

	// merged cleanup hint
	mergedHintStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#555")).
			Italic(true)

	// error log
	errLogTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("196")).
				Bold(true)

	errLogLineStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#AAA"))

	// uncommitted changes
	uncommittedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#E9C46A"))

	// diff view
	diffAddStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#2A9D8F"))
	diffRemoveStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
	diffHunkStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#88C0D0"))
	diffFileStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#888")).Bold(true)

	// file changes panel
	fileChangesBoxStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("#444")).
				Padding(0, 1).
				MarginTop(1)

	fileChangesTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888"))

	fileNameStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#AAA"))

	fileAddedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#2A9D8F"))

	fileRemovedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("196"))
)

type keyMap struct {
	Up     key.Binding
	Down   key.Binding
	New    key.Binding
	Issue  key.Binding
	Enter  key.Binding
	Diff   key.Binding
	PR     key.Binding
	Open   key.Binding
	Delete key.Binding
	Select key.Binding
	Filter key.Binding
	Sort   key.Binding
	ErrLog key.Binding
	Quit   key.Binding
	Help   key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.New, k.Issue, k.Enter, k.Diff, k.Delete, k.Quit, k.Help}
}

func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.New, k.Issue, k.Enter},
		{k.Diff, k.PR, k.Open, k.Select, k.Delete},
		{k.Filter, k.Sort, k.ErrLog, k.Quit, k.Help},
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
	Issue: key.NewBinding(
		key.WithKeys("i"),
		key.WithHelp("i", "from GH issue"),
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
	Select: key.NewBinding(
		key.WithKeys(" "),
		key.WithHelp("space", "multi-select"),
	),
	Filter: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "filter"),
	),
	Sort: key.NewBinding(
		key.WithKeys("s"),
		key.WithHelp("s", "cycle sort"),
	),
	ErrLog: key.NewBinding(
		key.WithKeys("E"),
		key.WithHelp("E", "error log"),
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
	DiffStat         string
	Active           bool
	WindowID         string
	UncommittedCount int
	LastActivity     time.Time
	FileChanges      []worktree.FileChange
}

const (
	sortByName     = 0
	sortByAge      = 1
	sortByActivity = 2
	sortByPR       = 3
)

var sortModeNames = []string{"name", "age", "activity", "PR"}

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

	// two-step create dialog
	input         textinput.Model
	creating      bool
	issueMode     bool
	createStep    int
	newBranchName string

	// delete confirmation (single or batch)
	deleting     bool
	deleteTarget string // single target; empty means batch (use m.selected)

	// agent output preview
	agentPreview string

	// PR creation dialog (improvement 5)
	prCreating     bool
	prGenerating   bool
	prStep         int // 0 = title, 1 = body
	prTitle        string
	prBodyPrefill  string
	prWsName       string
	prBranch       string
	prBase         string

	// CI status per workspace (improvement 1)
	ciStatus map[string]string // wsName -> "success"/"failure"/"pending"/""

	// multi-select (improvement 9)
	selected map[string]bool

	// sorting & filtering (improvement 4)
	sortMode    int
	filtering   bool
	filterQuery string

	// diff view
	diffViewing      bool
	diffContent      string
	diffScrollOffset int
	diffWsName       string

	// error log (improvement 10)
	errLog     []string
	showErrLog bool

	help help.Model
	keys keyMap

	err error
}

func NewModel() (*Model, error) {
	wt := worktree.New()
	st, err := state.New(".")
	if err != nil {
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
		ciStatus:    make(map[string]string),
		selected:    make(map[string]bool),
	}, nil
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.loadWorkspacesCmd,
		tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return prStatusTickMsg{} }),
		tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return refreshTickMsg{} }),
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
		// Error log overlay swallows all keys
		if m.showErrLog {
			m.showErrLog = false
			return m, nil
		}

		// Diff view mode
		if m.diffViewing {
			switch msg.String() {
			case "esc", "q":
				m.diffViewing = false
				m.diffContent = ""
				m.diffScrollOffset = 0
				m.diffWsName = ""
			case "up", "k":
				if m.diffScrollOffset > 0 {
					m.diffScrollOffset--
				}
			case "down", "j":
				m.diffScrollOffset++
			}
			return m, nil
		}

		// Delete confirmation mode
		if m.deleting {
			switch msg.String() {
			case "y", "Y":
				if m.deleteTarget != "" {
					target := m.deleteTarget
					m.deleting = false
					m.deleteTarget = ""
					return m, m.deleteWorkspaceCmd(target)
				}
				// batch delete
				targets := make([]string, 0, len(m.selected))
				for name := range m.selected {
					targets = append(targets, name)
				}
				m.deleting = false
				m.deleteTarget = ""
				m.selected = make(map[string]bool)
				return m, m.batchDeleteWorkspaceCmd(targets)
			case "n", "esc":
				m.deleting = false
				m.deleteTarget = ""
			}
			return m, nil
		}

		// PR creation dialog (improvement 5)
		if m.prCreating {
			switch msg.String() {
			case "enter":
				val := m.input.Value()
				if m.prStep == 0 {
					m.prTitle = val
					m.prStep = 1
					m.input.Placeholder = "PR body (optional)"
					m.input.SetValue(m.prBodyPrefill)
					return m, textinput.Blink
				}
				// step 1: body confirmed
				wsName := m.prWsName
				branch := m.prBranch
				base := m.prBase
				title := m.prTitle
				body := val
				m.prCreating = false
				m.prStep = 0
				m.prTitle = ""
				m.input.SetValue("")
				m.input.Placeholder = "New branch name"
				return m, m.createPRCmd(wsName, branch, base, title, body)
			case "esc":
				m.prCreating = false
				m.prStep = 0
				m.prTitle = ""
				m.prBodyPrefill = ""
				m.input.SetValue("")
				m.input.Placeholder = "New branch name"
				return m, nil
			}
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		// Filter mode (improvement 4)
		if m.filtering {
			switch msg.String() {
			case "esc", "enter":
				m.filtering = false
				m.cursor = 0
				return m, m.capturePreviewCmd()
			case "backspace":
				if len(m.filterQuery) > 0 {
					m.filterQuery = m.filterQuery[:len(m.filterQuery)-1]
				}
				m.cursor = 0
			default:
				if len(msg.String()) == 1 {
					m.filterQuery += msg.String()
					m.cursor = 0
				}
			}
			return m, nil
		}

		// Two-step workspace create / issue mode
		if m.creating {
			switch msg.String() {
			case "enter":
				val := m.input.Value()
				if val == "" {
					return m, nil
				}
				if m.issueMode {
					m.creating = false
					m.issueMode = false
					m.input.SetValue("")
					m.input.Placeholder = "New branch name"
					return m, m.createWorkspaceFromIssueCmd(val)
				}
				if m.createStep == 0 {
					m.newBranchName = val
					m.createStep = 1
					m.input.Placeholder = "Base branch"
					m.input.SetValue(m.cfg.Worktree.DefaultBase)
					return m, textinput.Blink
				}
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
				m.issueMode = false
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
		visible := m.visibleWorkspaces()
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Up):
			if m.cursor > 0 {
				m.cursor--
				return m, m.capturePreviewCmd()
			}
		case key.Matches(msg, m.keys.Down):
			if m.cursor < len(visible)-1 {
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
		case key.Matches(msg, m.keys.Issue):
			m.creating = true
			m.issueMode = true
			m.input.Placeholder = "GitHub issue number"
			m.input.SetValue("")
			m.input.Focus()
			return m, textinput.Blink
		case key.Matches(msg, m.keys.Enter):
			if len(visible) > 0 {
				ws := visible[m.cursor]
				return m, m.attachWorkspaceCmd(ws.Name)
			}
		case key.Matches(msg, m.keys.Diff):
			if len(visible) > 0 {
				return m, m.loadDiffCmd(visible[m.cursor])
			}
		case key.Matches(msg, m.keys.PR):
			if len(visible) > 0 {
				ws := visible[m.cursor]
				m.prGenerating = true
				m.prWsName = ws.Name
				m.prBranch = ws.Branch
				m.prBase = ws.BaseBranch
				return m, m.generatePRContentCmd(ws)
			}
		case key.Matches(msg, m.keys.Open):
			if len(visible) > 0 {
				ws := visible[m.cursor]
				if ws.PRURL != "" {
					return m, openURLCmd(ws.PRURL)
				}
			}
		case key.Matches(msg, m.keys.Select):
			// Improvement 9: multi-select toggle
			if len(visible) > 0 {
				ws := visible[m.cursor]
				if m.selected[ws.Name] {
					delete(m.selected, ws.Name)
				} else {
					m.selected[ws.Name] = true
				}
				// Advance cursor
				if m.cursor < len(visible)-1 {
					m.cursor++
				}
			}
		case key.Matches(msg, m.keys.Delete):
			if len(m.selected) > 0 {
				// batch delete confirmation
				m.deleting = true
				m.deleteTarget = ""
			} else if len(visible) > 0 {
				ws := visible[m.cursor]
				m.deleting = true
				m.deleteTarget = ws.Name
			}
		case key.Matches(msg, m.keys.Filter):
			// Improvement 4: enter filter mode
			m.filtering = true
			m.filterQuery = ""
			m.cursor = 0
		case key.Matches(msg, m.keys.Sort):
			// Improvement 4: cycle sort mode
			m.sortMode = (m.sortMode + 1) % 4
			m.cursor = 0
		case key.Matches(msg, m.keys.ErrLog):
			// Improvement 10: toggle error log
			m.showErrLog = !m.showErrLog
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
		}

	case loadedWorkspacesMsg:
		m.workspaces = msg.workspaces
		visible := m.visibleWorkspaces()
		if m.cursor >= len(visible) {
			m.cursor = max(0, len(visible)-1)
		}
		return m, m.capturePreviewCmd()

	case createdWorkspaceMsg:
		return m, m.loadWorkspacesCmd

	case deletedWorkspaceMsg:
		m.selected = make(map[string]bool)
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

	case prContentGeneratedMsg:
		m.prGenerating = false
		m.prCreating = true
		m.prStep = 0
		m.prBodyPrefill = msg.body
		m.input.Placeholder = "PR title"
		m.input.SetValue(msg.title)
		m.input.Focus()
		return m, textinput.Blink

	case prStatusTickMsg:
		cmds := []tea.Cmd{
			tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return prStatusTickMsg{} }),
		}
		for _, ws := range m.workspaces {
			if ws.PRURL != "" && ws.PRStatus != "merged" {
				cmds = append(cmds, m.checkPRStatusCmd(ws.Name, ws.Branch))
				cmds = append(cmds, m.checkCIStatusCmd(ws.Name, ws.Branch))
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

	// Improvement 1: CI status updated
	case ciStatusCheckedMsg:
		if m.ciStatus == nil {
			m.ciStatus = make(map[string]string)
		}
		m.ciStatus[msg.wsName] = msg.ciStatus

	case attachFinishedMsg:
		if msg.err != nil {
			m.err = msg.err
			m.appendErrLog(msg.err.Error())
			return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
				return clearErrorMsg{}
			})
		}
		return m, m.loadWorkspacesCmd

	case errMsg:
		m.err = msg.err
		m.appendErrLog(msg.err.Error())
		return m, tea.Tick(3*time.Second, func(t time.Time) tea.Msg {
			return clearErrorMsg{}
		})

	case diffLoadedMsg:
		m.diffViewing = true
		m.diffContent = msg.content
		m.diffScrollOffset = 0
		m.diffWsName = msg.wsName

	case clearErrorMsg:
		m.err = nil
	}

	return m, cmd
}

func (m *Model) appendErrLog(msg string) {
	ts := time.Now().Format("15:04:05")
	entry := fmt.Sprintf("[%s] %s", ts, msg)
	m.errLog = append(m.errLog, entry)
	if len(m.errLog) > 20 {
		m.errLog = m.errLog[len(m.errLog)-20:]
	}
}

func (m Model) View() string {
	// Improvement 10: error log overlay
	if m.showErrLog {
		var sb strings.Builder
		sb.WriteString(errLogTitleStyle.Render("Error Log") + "\n\n")
		if len(m.errLog) == 0 {
			sb.WriteString(errLogLineStyle.Render("No errors recorded."))
		} else {
			for _, entry := range m.errLog {
				sb.WriteString(errLogLineStyle.Render(entry))
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n" + helpStyle.Render("Any key to close"))
		return appStyle.Render(sb.String())
	}

	// Diff view overlay
	if m.diffViewing {
		lines := strings.Split(m.diffContent, "\n")
		availHeight := m.height - 8
		if availHeight < 5 {
			availHeight = 5
		}
		// clamp scroll
		maxScroll := len(lines) - availHeight
		if maxScroll < 0 {
			maxScroll = 0
		}
		if m.diffScrollOffset > maxScroll {
			m.diffScrollOffset = maxScroll
		}
		end := m.diffScrollOffset + availHeight
		if end > len(lines) {
			end = len(lines)
		}
		visible := lines[m.diffScrollOffset:end]

		var sb strings.Builder
		for _, line := range visible {
			sb.WriteString(renderDiffLine(line))
			sb.WriteString("\n")
		}

		scrollInfo := fmt.Sprintf("line %d/%d", m.diffScrollOffset+1, len(lines))
		footer := fmt.Sprintf("%s  •  %s  •  %s",
			helpStyle.Render("↑/k ↓/j scroll"),
			helpStyle.Render("esc to close"),
			helpStyle.Render(scrollInfo),
		)
		content := fmt.Sprintf("%s\n\n%s\n%s",
			titleStyle.Render("Diff: "+m.diffWsName),
			sb.String(),
			footer,
		)
		return appStyle.Render(content)
	}

	// Delete confirmation dialog
	if m.deleting {
		var titleMsg string
		if m.deleteTarget != "" {
			titleMsg = fmt.Sprintf("Delete workspace %q?", m.deleteTarget)
		} else {
			names := make([]string, 0, len(m.selected))
			for name := range m.selected {
				names = append(names, name)
			}
			sort.Strings(names)
			titleMsg = fmt.Sprintf("Delete %d workspaces: %s?", len(names), strings.Join(names, ", "))
		}
		footer := fmt.Sprintf("%s %s  •  %s %s",
			confirmKeyStyle.Render("y"), confirmLabelStyle.Render("confirm"),
			confirmKeyStyle.Render("esc/n"), confirmLabelStyle.Render("cancel"),
		)
		content := fmt.Sprintf("%s\n\n%s\n\n%s",
			dangerStyle.Render(titleMsg),
			confirmLabelStyle.Render("The worktree, tmux window, and all local changes will be removed."),
			footer,
		)
		return appStyle.Render(deleteDialogStyle.Render(content))
	}

	// Issue creation dialog
	if m.creating && m.issueMode {
		return appStyle.Render(fmt.Sprintf("%s\n\n%s\n\n%s",
			titleStyle.Render("Create Workspace from GitHub Issue"),
			m.input.View(),
			helpStyle.Render("Enter issue number • Esc to cancel"),
		))
	}

	// Two-step create dialog
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

	// PR content generation in progress
	if m.prGenerating {
		return appStyle.Render(fmt.Sprintf("%s\n\n%s",
			titleStyle.Render(fmt.Sprintf("Create PR: %s → %s", m.prBranch, m.prBase)),
			helpStyle.Render("Generating title and description from commits…"),
		))
	}

	// Improvement 5: PR creation dialog
	if m.prCreating {
		var stepLabel string
		if m.prStep == 0 {
			stepLabel = "Step 1/2 — PR title"
		} else {
			stepLabel = fmt.Sprintf("Step 2/2 — PR body  (title: %s)", m.prTitle)
		}
		return appStyle.Render(fmt.Sprintf("%s\n\n%s\n%s\n\n%s",
			titleStyle.Render(fmt.Sprintf("Create PR: %s → %s", m.prBranch, m.prBase)),
			stepLabelStyle.Render(stepLabel),
			m.input.View(),
			helpStyle.Render("Enter to continue • Esc to cancel"),
		))
	}

	var s strings.Builder

	// Header with sort/filter info
	header := "OpenTree Workspaces"
	s.WriteString(titleStyle.Render(header))
	s.WriteString("\n\n")

	// Improvement 4: filter prompt
	if m.filtering {
		prompt := filterPromptStyle.Render("/") + " " + m.filterQuery + "█"
		s.WriteString(prompt + "\n\n")
	} else if m.filterQuery != "" {
		s.WriteString(filterPromptStyle.Render(fmt.Sprintf("filter: %q  (/ to change, esc to clear)", m.filterQuery)) + "\n\n")
	}

	// Error message (transient)
	if m.err != nil {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render(fmt.Sprintf("Error: %v", m.err)))
		s.WriteString("\n\n")
	}

	visible := m.visibleWorkspaces()

	// Workspace list
	if len(visible) == 0 {
		if m.filterQuery != "" {
			s.WriteString(itemStyle.Render("No workspaces match the filter."))
		} else {
			s.WriteString(itemStyle.Render("No workspaces found. Press 'n' to create one."))
		}
		s.WriteString("\n")
	} else {
		for i, ws := range visible {
			style := itemStyle
			if i == m.cursor {
				style = selectedItemStyle
			}

			// Activity dot
			status := "○"
			statusColor := stoppedStyle
			if ws.Active {
				status = "●"
				statusColor = activeStyle
			} else if ws.WindowID != "" {
				status = "◎"
				statusColor = idleStyle
			}

			// Multi-select mark
			selectMark := "  "
			if m.selected[ws.Name] {
				selectMark = selectedMarkStyle.Render("✓ ")
			}

			title := selectMark + fmt.Sprintf("%s %s", statusColor.Render(status), ws.Name)

			// Badges
			if ws.IssueNumber > 0 {
				title += "  " + issueBadgeStyle.Render(fmt.Sprintf("#%d", ws.IssueNumber))
			}
			if ws.PRStatus == "merged" {
				title += "  " + mergedBadgeStyle.Render("merged · ready to delete")
			} else if ws.PRStatus == "open" {
				title += "  " + prOpenBadgeStyle.Render("PR open")
				// Improvement 1: CI badge
				if ci, ok := m.ciStatus[ws.Name]; ok {
					switch ci {
					case "success":
						title += " " + ciSuccessStyle.Render("✓ CI")
					case "failure":
						title += " " + ciFailureStyle.Render("✗ CI")
					case "pending":
						title += " " + ciPendingStyle.Render("⟳ CI")
					}
				}
			}

			// Description line
			descParts := []string{ws.Branch, ws.DiffStat, "created " + formatAge(ws.CreatedAt)}

			// Improvement 2: uncommitted changes
			if ws.UncommittedCount > 0 {
				descParts = append(descParts, uncommittedStyle.Render(fmt.Sprintf("~%d uncommitted", ws.UncommittedCount)))
			}

			// Improvement 8: last activity
			if !ws.LastActivity.IsZero() {
				descParts = append(descParts, "active "+formatAge(ws.LastActivity))
			}

			desc := "  " + strings.Join(descParts, " • ")

			s.WriteString(style.Render(fmt.Sprintf("%s\n%s", title, diffStyle.Render(desc))))
			s.WriteString("\n")

			// Improvement 7: merged cleanup hint
			if ws.PRStatus == "merged" && i == m.cursor {
				s.WriteString(mergedHintStyle.Render("  → Press x to clean up this merged workspace"))
				s.WriteString("\n")
			}
		}

		// Per-file changes panel for selected workspace
		if m.cursor < len(visible) {
			ws := visible[m.cursor]
			if len(ws.FileChanges) > 0 {
				previewWidth := m.width - 8
				if previewWidth < 20 {
					previewWidth = 60
				}
				content := m.renderFileChanges(ws.FileChanges, previewWidth)
				s.WriteString(fileChangesBoxStyle.Width(previewWidth).Render(content))
				s.WriteString("\n")
			}
		}

		// Agent output preview for selected workspace
		if m.agentPreview != "" && m.cursor < len(visible) {
			wsName := visible[m.cursor].Name
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

	// Improvement 6: status bar
	s.WriteString("\n")
	s.WriteString(m.statusBar())
	s.WriteString("\n")

	// Help
	s.WriteString(m.help.View(m.keys))

	return appStyle.Render(s.String())
}

// statusBar renders the bottom stats line.
func (m Model) statusBar() string {
	total := len(m.workspaces)
	active := 0
	openPRs := 0
	for _, ws := range m.workspaces {
		if ws.Active {
			active++
		}
		if ws.PRStatus == "open" {
			openPRs++
		}
	}
	parts := []string{
		fmt.Sprintf("%d workspaces", total),
		fmt.Sprintf("%d active", active),
		fmt.Sprintf("%d open PRs", openPRs),
		"sort: " + sortModeNames[m.sortMode],
	}
	if len(m.selected) > 0 {
		parts = append(parts, fmt.Sprintf("%d selected", len(m.selected)))
	}
	if len(m.errLog) > 0 {
		parts = append(parts, fmt.Sprintf("%d errors (E)", len(m.errLog)))
	}
	return statusBarStyle.Render(strings.Join(parts, "  •  "))
}

// visibleWorkspaces returns the sorted and filtered workspace list.
func (m Model) visibleWorkspaces() []WorkspaceItem {
	sorted := m.sortedWorkspaces()
	if m.filterQuery == "" {
		return sorted
	}
	q := strings.ToLower(m.filterQuery)
	var out []WorkspaceItem
	for _, ws := range sorted {
		if strings.Contains(strings.ToLower(ws.Name), q) {
			out = append(out, ws)
		}
	}
	return out
}

// sortedWorkspaces returns a copy of m.workspaces sorted by m.sortMode.
func (m Model) sortedWorkspaces() []WorkspaceItem {
	ws := make([]WorkspaceItem, len(m.workspaces))
	copy(ws, m.workspaces)
	switch m.sortMode {
	case sortByAge:
		sort.Slice(ws, func(i, j int) bool {
			return ws[i].CreatedAt.After(ws[j].CreatedAt)
		})
	case sortByActivity:
		sort.Slice(ws, func(i, j int) bool {
			return ws[i].LastActivity.After(ws[j].LastActivity)
		})
	case sortByPR:
		prOrder := func(s string) int {
			switch s {
			case "open":
				return 0
			case "merged":
				return 1
			default:
				return 2
			}
		}
		sort.Slice(ws, func(i, j int) bool {
			return prOrder(ws[i].PRStatus) < prOrder(ws[j].PRStatus)
		})
	default: // sortByName
		sort.Slice(ws, func(i, j int) bool {
			return ws[i].Name < ws[j].Name
		})
	}
	return ws
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
type prContentGeneratedMsg struct{ title, body string }
type prStatusCheckedMsg struct {
	wsName   string
	prURL    string
	prStatus string
}
type ciStatusCheckedMsg struct {
	wsName   string
	ciStatus string
}
type refreshTickMsg struct{}
type previewTickMsg struct{}
type diffLoadedMsg struct {
	content string
	wsName  string
}
type capturePreviewMsg struct {
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

		fileChanges, _ := m.worktreeMgr.DiffFileStats(ws.Branch)

		item := WorkspaceItem{
			Workspace:   ws,
			DiffStat:    diffStat,
			Active:      exists && win.Active,
			WindowID:    "",
			FileChanges: fileChanges,
		}
		if exists {
			item.WindowID = win.ID
		}

		// Improvement 2: count uncommitted changes
		if ws.WorktreeDir != "" {
			item.UncommittedCount = countUncommitted(ws.WorktreeDir)
		}

		// Improvement 8: last activity from tmux
		if exists {
			if t, err := m.tmuxCtrl.GetWindowActivity(ws.Name); err == nil {
				item.LastActivity = t
			}
		}

		items = append(items, item)
	}

	return loadedWorkspacesMsg{workspaces: items}
}

func (m Model) createWorkspaceCmd(name, baseBranch string) tea.Cmd {
	return func() tea.Msg {
		if err := m.worktreeMgr.Create(name, baseBranch); err != nil {
			return errMsg{err}
		}

		out, err := exec.Command("git", "rev-parse", "--show-toplevel").CombinedOutput()
		if err != nil {
			return errMsg{fmt.Errorf("failed to get repo root: %w", err)}
		}
		repoRoot := strings.TrimSpace(string(out))
		dirName := strings.ReplaceAll(name, "/", "-")
		worktreePath := filepath.Join(repoRoot, m.cfg.Worktree.BaseDir, dirName)

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

func (m Model) createWorkspaceFromIssueCmd(issueNumStr string) tea.Cmd {
	return func() tea.Msg {
		issueNum, err := strconv.Atoi(strings.TrimSpace(issueNumStr))
		if err != nil || issueNum <= 0 {
			return errMsg{fmt.Errorf("invalid issue number: %s", issueNumStr)}
		}
		issue, err := m.prMgr.GetIssue(issueNum)
		if err != nil {
			return errMsg{err}
		}

		branchName := github.IssueBranchName(issue.Number, issue.Title)
		baseBranch := m.cfg.Worktree.DefaultBase

		if err := m.worktreeMgr.Create(branchName, baseBranch); err != nil {
			return errMsg{err}
		}

		out, err := exec.Command("git", "rev-parse", "--show-toplevel").CombinedOutput()
		if err != nil {
			return errMsg{fmt.Errorf("failed to get repo root: %w", err)}
		}
		repoRoot := strings.TrimSpace(string(out))
		dirName := strings.ReplaceAll(branchName, "/", "-")
		worktreePath := filepath.Join(repoRoot, m.cfg.Worktree.BaseDir, dirName)

		taskFile := filepath.Join(worktreePath, "TASK.md")
		_ = os.WriteFile(taskFile, []byte(buildIssueTaskContent(issue)), 0644)

		agentCmd := m.cfg.Agent.Command
		if err := m.tmuxCtrl.CreateWindow(branchName, worktreePath, agentCmd, m.cfg.Agent.Args...); err != nil {
			return errMsg{err}
		}

		ws := &state.Workspace{
			Name:        branchName,
			Branch:      branchName,
			BaseBranch:  baseBranch,
			CreatedAt:   time.Now(),
			Status:      "active",
			Agent:       agentCmd,
			WorktreeDir: worktreePath,
			IssueNumber: issue.Number,
			IssueTitle:  issue.Title,
		}
		if err := m.stateStore.AddWorkspace(ws); err != nil {
			return errMsg{err}
		}

		return createdWorkspaceMsg{}
	}
}

func buildIssueTaskContent(issue *github.Issue) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Issue #%d: %s\n\n", issue.Number, issue.Title))
	if len(issue.Labels) > 0 {
		sb.WriteString(fmt.Sprintf("**Labels:** %s\n\n", strings.Join(issue.Labels, ", ")))
	}
	sb.WriteString("## Description\n\n")
	if issue.Body != "" {
		sb.WriteString(issue.Body)
		sb.WriteString("\n")
	} else {
		sb.WriteString("_No description provided._\n")
	}
	return sb.String()
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

// Improvement 9: batch delete
func (m Model) batchDeleteWorkspaceCmd(names []string) tea.Cmd {
	return func() tea.Msg {
		for _, name := range names {
			_ = m.tmuxCtrl.KillWindow(name)
			if err := m.worktreeMgr.Delete(name, true); err != nil {
				return errMsg{fmt.Errorf("delete %s: %w", name, err)}
			}
			if err := m.stateStore.DeleteWorkspace(name); err != nil {
				return errMsg{fmt.Errorf("delete state %s: %w", name, err)}
			}
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

func (m Model) generatePRContentCmd(ws WorkspaceItem) tea.Cmd {
	return func() tea.Msg {
		title, body := generatePRContent(ws.Branch, ws.BaseBranch, ws.WorktreeDir, ws.IssueNumber, ws.IssueTitle)
		return prContentGeneratedMsg{title: title, body: body}
	}
}

func generatePRContent(branch, baseBranch, worktreeDir string, issueNumber int, issueTitle string) (title, body string) {
	var commits []string
	if worktreeDir != "" {
		cmd := exec.Command("git", "log", baseBranch+"..HEAD", "--format=%s", "--no-merges")
		cmd.Dir = worktreeDir
		if out, err := cmd.CombinedOutput(); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if strings.TrimSpace(line) != "" {
					commits = append(commits, strings.TrimSpace(line))
				}
			}
		}
	}

	if issueTitle != "" {
		title = issueTitle
	} else if len(commits) > 0 {
		title = commits[0]
	} else {
		title = branch
	}

	var sb strings.Builder
	if len(commits) > 0 {
		sb.WriteString("## Changes\n\n")
		for _, c := range commits {
			sb.WriteString("- " + c + "\n")
		}
		sb.WriteString("\n")
	}
	if issueNumber > 0 {
		sb.WriteString(fmt.Sprintf("Closes #%d\n", issueNumber))
	}
	body = sb.String()
	return
}

// Improvement 5: createPRCmd now accepts title and body.
func (m Model) createPRCmd(wsName, branch, baseBranch, title, body string) tea.Cmd {
	return func() tea.Msg {
		prURL, err := m.prMgr.CreatePR(branch, baseBranch, title, body)
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

// Improvement 1: checkCIStatusCmd fetches CI check results for a PR.
func (m Model) checkCIStatusCmd(wsName, branch string) tea.Cmd {
	return func() tea.Msg {
		status, err := m.prMgr.GetPRCIStatus(branch)
		if err != nil || status == "" {
			return nil
		}
		return ciStatusCheckedMsg{wsName: wsName, ciStatus: status}
	}
}

func (m Model) capturePreviewCmd() tea.Cmd {
	if len(m.workspaces) == 0 {
		return nil
	}
	visible := m.visibleWorkspaces()
	if len(visible) == 0 || m.cursor >= len(visible) {
		return func() tea.Msg { return capturePreviewMsg{lines: ""} }
	}
	ws := visible[m.cursor]
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

func (m Model) loadDiffCmd(ws WorkspaceItem) tea.Cmd {
	return func() tea.Msg {
		content, err := m.worktreeMgr.DiffFull(ws.Branch)
		if err != nil {
			return errMsg{err}
		}
		if strings.TrimSpace(content) == "" {
			content = "No changes."
		}
		return diffLoadedMsg{content: content, wsName: ws.Name}
	}
}

// renderFileChanges builds the per-file changes panel content.
func (m Model) renderFileChanges(files []worktree.FileChange, width int) string {
	var sb strings.Builder
	sb.WriteString(fileChangesTitleStyle.Render(fmt.Sprintf("Changed files (%d)", len(files))))
	sb.WriteString("\n")

	maxName := 0
	for _, f := range files {
		name := shortenPath(f.FileName, width-20)
		if len(name) > maxName {
			maxName = len(name)
		}
	}

	for _, f := range files {
		name := shortenPath(f.FileName, width-20)
		padding := strings.Repeat(" ", maxName-len(name)+2)

		addStr := fileAddedStyle.Render(fmt.Sprintf("+%d", f.Added))
		remStr := fileRemovedStyle.Render(fmt.Sprintf("-%d", f.Removed))

		sb.WriteString(fmt.Sprintf(" %s%s%s %s\n", fileNameStyle.Render(name), padding, addStr, remStr))
	}

	return sb.String()
}

// shortenPath truncates a file path from the left, keeping the filename and nearest directories.
func shortenPath(path string, maxLen int) string {
	if len(path) <= maxLen || maxLen <= 0 {
		return path
	}
	parts := strings.Split(path, "/")
	if len(parts) <= 1 {
		return path
	}
	result := parts[len(parts)-1]
	for i := len(parts) - 2; i >= 0; i-- {
		candidate := parts[i] + "/" + result
		if len(candidate)+4 > maxLen {
			return ".../" + result
		}
		result = candidate
	}
	return result
}

// renderDiffLine colorizes a single line of unified diff output.
func renderDiffLine(line string) string {
	switch {
	case strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ "):
		return diffFileStyle.Render(line)
	case strings.HasPrefix(line, "@@"):
		return diffHunkStyle.Render(line)
	case strings.HasPrefix(line, "+"):
		return diffAddStyle.Render(line)
	case strings.HasPrefix(line, "-"):
		return diffRemoveStyle.Render(line)
	default:
		return line
	}
}

// Improvement 2: countUncommitted counts files with uncommitted changes in a worktree.
func countUncommitted(worktreePath string) int {
	cmd := exec.Command("git", "status", "--short")
	cmd.Dir = worktreePath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
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
