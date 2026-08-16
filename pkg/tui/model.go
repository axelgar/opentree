package tui

import (
	"fmt"
	"os"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/github"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/skills"
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
	ChatStatus       *chat.Status
	// MissingSkills are the repository skill trees this worktree cannot see —
	// empty for the common case where the repo has none or git carries them.
	MissingSkills []string
	// ServerRunning is whether this workspace's run window exists, read from
	// the same window list the rest of the row uses. A server is a process, and
	// the process list is the only thing about it that cannot be stale.
	ServerRunning bool
}

const (
	sortByName     = 0
	sortByAge      = 1
	sortByActivity = 2
	sortByPR       = 3
)

var sortModeNames = []string{"name", "age", "activity", "PR"}

// The two top-level places opentree shows. Tabs rather than overlays: an
// overlay is something you open, act on, and close, while both of these are
// inventories you come back to.
const (
	tabWorkspaces = 0
	tabSkills     = 1
)

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

	// transient success notice (e.g. "sent N review comments")
	notice string

	// sequence numbers so an old banner's 3s clear-timer can't wipe a newer
	// banner raised in the meantime
	errSeq    int
	noticeSeq int

	// in-flight guards so slow git/gh work can't pile up under the periodic
	// refresh ticks
	refreshing           bool
	statusChecksInFlight int

	// diff view
	diffViewing      bool
	diffContent      string
	diffScrollOffset int
	diffWsName       string

	// agent selection overlay
	agentSelecting bool
	agentCursor    int

	// confirming a 300MB adapter download, and the agent to switch to once it
	// lands
	agentInstallConfirm *config.PredefinedAgent
	agentPendingSelect  *config.PredefinedAgent

	// agentReadiness overrides the real check in tests; nil uses it.
	agentReadiness func(config.PredefinedAgent) (string, bool)

	// answering a chat agent's permission prompt without attaching
	answering    bool
	answerWs     string
	answerPerm   *chat.Permission
	answerCursor int

	// sending a prompt to a chat agent without attaching
	prompting bool
	promptWs  string

	// tmuxMissing is the one dependency check the dashboard makes. Every
	// workspace's agent runs in a tmux window, so without it nothing on this
	// screen can be created — and the failure otherwise arrives only after a
	// create has been attempted, as a truncated toast. Asked once at startup:
	// installing tmux is not something that happens mid-session.
	tmuxMissing bool

	// error log
	errLog     []string
	showErrLog bool

	// which top-level place is showing
	tab int

	// skillsTab is the Skills tab's own state, defined beside its behaviour
	// in skills.go.
	skillsTab skillsTab

	help help.Model
	keys keyMap

	err error
}

// Messages

type loadedWorkspacesMsg struct {
	workspaces []WorkspaceItem
}

type skillsScannedMsg struct {
	skills []skills.Skill
}
type skillEditedMsg struct{ err error }
type skillsRelinkedMsg struct {
	bridged []string // repo trees pointed at the one the repository has
	count   int      // workspaces repaired
}
type skillClonedMsg struct {
	name  string
	trees int // how many trees it landed in
	err   error
}

// skillsDiscoveredMsg is what a site answered when asked for its skill index.
// An err here is ordinary — most addresses are not publishers — so it carries
// the address back for the git clone that follows rather than being shown.
type skillsDiscoveredMsg struct {
	site    string
	entries []skills.Entry
	err     error
}
type skillProbedMsg struct {
	agent    string
	commands map[string]bool
	err      error
}

// skillUpdatedMsg is what the publisher said about an installed skill.
// changed is false when the digest still matched, which is the usual answer
// and the one that costs nothing.
type skillUpdatedMsg struct {
	name    string
	changed bool
	err     error
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
type deletedWorkspaceMsg struct{ names []string }
type errMsg struct{ err error }
type clearErrorMsg struct{ seq int }
type clearNoticeMsg struct{ seq int }
type attachFinishedMsg struct{ err error }
type prStatusTickMsg struct{}
type prCreatedMsg struct{ wsName, prURL string }
type prContentGeneratedMsg struct{ wsName, title, body string }
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
type statusCheckErrMsg struct{ err error }
type refreshTickMsg struct{}
type spinnerTickMsg struct{}
type diffLoadedMsg struct {
	content string
	wsName  string
}
type adapterInstalledMsg struct {
	adapter string
	err     error
}

type agentCommandSentMsg struct {
	wsName string
	action string
}

// serverToggledMsg is what the w key did, for the notice line.
type serverToggledMsg struct {
	wsName string
	action string
}

type reviewsSentMsg struct {
	wsName string
	count  int
}

// browserOpenedMsg reports that a PR URL was handed to the system browser,
// so the dashboard can say so instead of the key answering with silence.
type browserOpenedMsg struct{ url string }

// errLogCopiedMsg is what the clipboard said. It is answered in the error
// log's own footer rather than the toast slot, which that screen does not
// draw — a copy whose outcome lands on a screen the user is not looking at is
// a copy they have to test by pasting.
type errLogCopiedMsg struct {
	count int
	err   error
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

	// No CharLimit: the same input holds generated PR titles and bodies,
	// which a limit would silently truncate.
	ti := textinput.New()
	ti.Placeholder = "New branch name"
	ti.Width = 30

	return &Model{
		svc:                    svc,
		worktreeMgr:            wt,
		stateStore:             st,
		prMgr:                  gh,
		cfg:                    cfg,
		repoRoot:               repoRoot,
		input:                  ti,
		help:                   help.New(),
		keys:                   keys,
		ciStatus:               make(map[string]string),
		selected:               make(map[string]bool),
		workspaceDeletingNames: make(map[string]bool),
		tmuxMissing:            !tmux.Installed(),
	}, nil
}

// Init starts the initial commands: load workspaces, periodic tickers.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.loadWorkspacesCmd,
		m.scanSkillsCmd,
		tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return prStatusTickMsg{} }),
		tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return refreshTickMsg{} }),
	)
}

// Run is the entry point for the TUI application.
func Run() error {
	m, err := NewModel()
	if err != nil {
		return err
	}

	// WithMouseCellMotion routes scroll/click to the app so the terminal stops
	// scrolling its own scrollback behind the alt-screen (revealing shell history).
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		return err
	}
	return nil
}
