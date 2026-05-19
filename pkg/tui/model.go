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
	"github.com/axelgar/opentree/pkg/workspace"
	"github.com/axelgar/opentree/pkg/worktree"
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

// Per-mode state sub-structs. Active-mode booleans live on m.mode instead;
// these structs hold only the data fields each mode reads or writes.

type listState struct {
	cursor      int
	selected    map[string]bool
	sortMode    int
	filtering   bool // sub-state: filter prompt visible
	filterQuery string
}

type createState struct {
	step                   int
	newBranchName          string
	remoteBranches         []string
	filteredBranches       []string
	branchSuggestionCursor int
}

type deleteState struct {
	target string // empty = batch delete using m.list.selected
}

type diffState struct {
	content      string
	scrollOffset int
	wsName       string
}

type prState struct {
	step        int // 0 = title, 1 = body
	title       string
	bodyPrefill string
	wsName      string
	branch      string
	base        string
	cancelled   bool // set when esc'd out of ModePRGenerating; suppresses late content msg
}

type agentSelectState struct {
	cursor int
}

type errorLogState struct {
	entries []string
}

// Model is the main Bubble Tea model for the opentree TUI.
type Model struct {
	svc         *workspace.Service
	worktreeMgr *worktree.Manager
	stateStore  *state.Store
	prMgr       *github.PRManager
	cfg         *config.Config
	repoRoot    string

	// Active mode — single source of truth for the dispatcher.
	mode Mode

	width  int
	height int

	// Workspaces and async-refreshed data (shared across modes).
	workspaces []WorkspaceItem
	ciStatus   map[string]string // wsName -> CI status

	// Shared textinput widget; each mode borrows it via m.focusInput.
	input textinput.Model

	// Per-mode state.
	list     listState
	create   createState
	del      deleteState
	diff     diffState
	pr       prState
	agentSel agentSelectState
	errorLog errorLogState

	// Agent output preview (periodically refreshed).
	agentPreview string

	// In-flight tracking for workspace creation/deletion.
	workspaceCreating      bool
	workspaceCreatingName  string
	workspaceDeleting      bool
	workspaceDeletingName  string
	workspaceDeletingNames map[string]bool
	spinnerFrame           int

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
	err      error
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
type reviewsSentMsg struct {
	wsName string
	count  int
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
		svc:                    svc,
		worktreeMgr:            wt,
		stateStore:             st,
		prMgr:                  gh,
		cfg:                    cfg,
		repoRoot:               repoRoot,
		mode:                   ModeList,
		input:                  ti,
		help:                   help.New(),
		keys:                   keys,
		ciStatus:               make(map[string]string),
		list:                   listState{selected: make(map[string]bool)},
		workspaceDeletingNames: make(map[string]bool),
	}, nil
}

// focusInput resets the shared textinput widget for a new mode/step.
// Consolidates the placeholder/value/Focus dance used by every modal that
// reuses m.input (Create, CreateFromIssue, CreateFromRemote, PRCreating).
func (m *Model) focusInput(placeholder, value string) {
	m.input.Placeholder = placeholder
	m.input.SetValue(value)
	m.input.Focus()
}

// resetToList exhaustively returns the model to ModeList, zeroing every
// per-mode sub-struct so a stale flag from an aborted dialog never leaks
// into the next session. Bug A fix: the errMsg handler used to clear only
// some flags; calling this from any error-recovery path is now sufficient.
// errorLog.entries are preserved so the user can still review past errors.
func (m *Model) resetToList() {
	m.mode = ModeList
	m.list.filtering = false
	m.create = createState{}
	m.del = deleteState{}
	m.diff = diffState{}
	m.pr = prState{}
	m.agentSel = agentSelectState{}
	m.input.SetValue("")
	m.input.Placeholder = "New branch name"
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
