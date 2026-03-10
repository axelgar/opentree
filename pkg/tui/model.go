package tui

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/github"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/tmux"
	"github.com/axelgar/opentree/pkg/worktree"
	"github.com/axelgar/opentree/pkg/workspace"
)

// WorkspaceItem enriches a state.Workspace with display-specific data.
type WorkspaceItem struct {
	*state.Workspace
	DiffStat         string
	Active           bool
	WindowID         string
	UncommittedCount int
	LastActivity     time.Time
	FileChanges      []worktree.FileChange
	AgentStatus      *AgentStatus
}

const (
	sortByName     = 0
	sortByAge      = 1
	sortByActivity = 2
	sortByPR       = 3
)

var sortModeNames = []string{"name", "age", "activity", "PR"}

// Model is the main Bubble Tea model for the opentree TUI.
type Model struct {
	svc         *workspace.Service
	worktreeMgr *worktree.Manager
	stateStore  *state.Store
	prMgr       *github.PRManager
	cfg         *config.Config
	repoRoot    string

	workspaces []WorkspaceItem
	cursor     int
	width      int
	height     int

	// two-step create dialog
	input            textinput.Model
	creating         bool
	issueMode        bool
	remoteBranchMode bool
	createStep       int
	newBranchName    string

	// remote branch suggestion list (used in remoteBranchMode)
	remoteBranches         []string
	filteredBranches       []string
	branchSuggestionCursor int

	// delete confirmation (single or batch)
	deleting     bool
	deleteTarget string // single target; empty means batch (use m.selected)

	// in-flight operation feedback
	workspaceCreating      bool
	workspaceCreatingName  string
	workspaceDeleting      bool
	workspaceDeletingName  string
	workspaceDeletingNames map[string]bool
	spinnerFrame           int

	// agent output preview
	agentPreview string

	// PR creation dialog
	prCreating    bool
	prGenerating  bool
	prStep        int // 0 = title, 1 = body
	prTitle       string
	prBodyPrefill string
	prWsName      string
	prBranch      string
	prBase        string

	// CI status per workspace
	ciStatus map[string]string // wsName -> "success"/"failure"/"pending"/""

	// multi-select
	selected map[string]bool

	// sorting & filtering
	sortMode    int
	filtering   bool
	filterQuery string

	// diff view
	diffViewing      bool
	diffContent      string
	diffScrollOffset int
	diffWsName       string

	// error log
	errLog     []string
	showErrLog bool

	help help.Model
	keys keyMap

	err error
}

// Messages

type loadedWorkspacesMsg struct {
	workspaces []WorkspaceItem
}

type remoteBranchesLoadedMsg struct {
	branches []string
}

type createdWorkspaceMsg struct {
	wsName      string
	branch      string
	worktreeDir string
}
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
type branchStatusCheckedMsg struct {
	wsName string
	status github.BranchStatus
}
type refreshTickMsg struct{}
type previewTickMsg struct{}
type spinnerTickMsg struct{}
type diffLoadedMsg struct {
	content string
	wsName  string
}
type capturePreviewMsg struct {
	lines string
}

// NewModel initializes a fully-configured TUI Model.
func NewModel() (*Model, error) {
	// Resolve the git repository root for state persistence
	repoRoot, err := gitutil.RepoRoot()
	if err != nil {
		if wd, err2 := os.Getwd(); err2 == nil {
			repoRoot = wd
		}
	}
	cfg, err := config.Load("")
	if err != nil {
		cfg = config.Default()
	}
	wt := worktree.New(repoRoot, cfg.Worktree.BaseDir)
	st, err := state.New(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize state store: %w", err)
	}
	tm := tmux.New(cfg.Tmux.SessionPrefix)
	gh := github.New()
	pm := workspace.NewTmuxProcessManager(tm)
	svc := workspace.NewService(repoRoot, cfg, wt, pm, st, gh)

	ti := textinput.New()
	ti.Placeholder = "New branch name"
	ti.CharLimit = 50
	ti.Width = 30

	return &Model{
		svc:         svc,
		worktreeMgr: wt,
		stateStore:  st,
		prMgr:       gh,
		cfg:         cfg,
		repoRoot:    repoRoot,
		input:       ti,
		help:        help.New(),
		keys:        keys,
		ciStatus:               make(map[string]string),
		selected:               make(map[string]bool),
		workspaceDeletingNames: make(map[string]bool),
	}, nil
}

// Init starts the initial commands: load workspaces, periodic tickers.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.loadWorkspacesCmd,
		tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return prStatusTickMsg{} }),
		tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return refreshTickMsg{} }),
		tea.Tick(5*time.Second, func(t time.Time) tea.Msg { return previewTickMsg{} }),
	)
}

// Run is the entry point for the TUI application.
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
