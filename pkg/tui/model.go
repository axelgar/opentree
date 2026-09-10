package tui

import (
	"path/filepath"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/bootstrap"
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
	// RepoRoot is the repository the workspace belongs to — the key into
	// Model.repos, and what the row is prefixed with when several show.
	RepoRoot         string
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
	// ServerListening is whether its port answered. The window being alive is
	// not the same as the server being up — a bundler spends a minute compiling
	// before it listens — and only the socket knows the difference.
	ServerListening bool
}

const (
	sortByName     = 0
	sortByAge      = 1
	sortByActivity = 2
	sortByPR       = 3
)

var sortModeNames = []string{"name", "age", "activity", "PR"}

// The top-level places opentree shows. Tabs rather than overlays: an
// overlay is something you open, act on, and close, while each of these is
// an inventory you come back to.
const (
	tabWorkspaces = 0
	tabAgents     = 1
	tabSkills     = 2
	tabPlugins    = 3
	tabServers    = 4
)

// Model is the main Bubble Tea model for the opentree TUI.
type Model struct {
	// svc, cfg and repoRoot are the repository the dashboard was opened in:
	// where n creates, what the Skills tab shows. All nil/empty outside one.
	svc      *workspace.Service
	cfg      *config.Config
	repoRoot string
	// repos is every repository on the list, by root. One entry in the
	// everyday case; every repository with state under --all or outside one.
	repos map[string]*workspace.Service
	// noRepo is the dashboard opened outside any repository, where nothing
	// can be created.
	noRepo bool

	workspaces []WorkspaceItem
	cursor     int

	// lastClick is the previous press on a row, for the double-click that
	// opens one.
	lastClick click

	// syncConflict is the merge-conflict dialog, or nil.
	syncConflict *syncConflict
	width        int
	height       int

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

	// promoting is the promote confirmation dialog; promoteWinner is the
	// sibling it keeps. Its losers are recomputed at confirm time rather than
	// stored: the dialog can sit open across a refresh, and a sibling deleted
	// meanwhile must not be deleted again.
	promoting     bool
	promoteWinner string

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

	// diff view; see diff.go
	diff diffView

	// confirming a 300MB adapter download, asked for from the Agents tab,
	// and the agent to switch to once it lands
	agentInstallConfirm *config.PredefinedAgent
	agentPendingSelect  *config.PredefinedAgent
	// agentPendingPath is the config file the pending selection writes to:
	// "" for the repository's, or the global one when the Agents tab's g
	// asked. The adapter install must not quietly narrow "everywhere" to
	// "here".
	agentPendingPath string

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

	// agentsTab is the Agents tab's own state, defined beside its behaviour
	// in agents.go.
	agentsTab agentsTab

	// skillsTab is the Skills tab's own state, defined beside its behaviour
	// in skills.go.
	skillsTab skillsTab

	// pluginsTab is the Plugins tab's own, in plugins.go, for the same reason.
	pluginsTab pluginsTab

	// serversTab is the Servers tab's own, in servers.go, for the same reason.
	serversTab serversTab

	// portless is what portless can do on this machine, re-read on each
	// refresh: it is a property of the machine rather than of a workspace, so
	// one answer serves every row.
	portless bootstrap.Portless

	help help.Model
	keys keyMap

	err error
}

// Messages

type loadedWorkspacesMsg struct {
	workspaces []WorkspaceItem
	portless   bootstrap.Portless
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
	// note is where the branch began, when that is worth saying — origin
	// could not be fetched, and the base is the local one.
	note string
}
type deletedWorkspaceMsg struct{ names []string }

// syncedMsg is what merging the base into a workspace did.
type syncedMsg struct {
	wsName string
	branch string
	res    worktree.SyncResult
}

// resolveAskedMsg is the agent having been handed a stopped merge.
type resolveAskedMsg struct {
	wsName string
	count  int
}

// syncConflict is a stopped merge waiting on the question of who resolves
// it, drawn as a dialog.
type syncConflict struct {
	wsName string
	branch string
	res    worktree.SyncResult
}

// pathCopiedMsg is the clipboard's answer to y.
type pathCopiedMsg struct {
	path string
	err  error
}

// editorFinishedMsg is the editor handing the terminal back after e.
type editorFinishedMsg struct{ err error }
type promotedWorkspaceMsg struct {
	winner  string
	deleted []string
	losers  []string // everything the promote was asked to delete
	err     error
}
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

// NewModel initializes a fully-configured TUI Model: for the repository the
// process stands in, or — with all, and always outside a repository — for
// every repository with state on this machine.
func NewModel(all bool) (*Model, error) {
	repoRoot, err := gitutil.RepoRoot()
	noRepo := err != nil
	if noRepo {
		repoRoot, all = "", true
	}
	cfg, err := config.Load("")
	if err != nil {
		cfg = config.Default()
	}
	var svc *workspace.Service
	repos := map[string]*workspace.Service{}
	if !noRepo {
		if svc, err = workspace.New(repoRoot, cfg); err != nil {
			return nil, err
		}
		repos[repoRoot] = svc
	}
	var errLog []string
	if all {
		roots, errs := state.Roots()
		for _, err := range errs {
			errLog = append(errLog, err.Error())
		}
		for _, root := range roots {
			if repos[root] != nil {
				continue
			}
			// Each repository has its own config: its agent, its base_dir,
			// its dev server. Loaded by path so the cwd's is never adopted.
			rcfg, err := config.Load(filepath.Join(root, "opentree.toml"))
			if err != nil {
				rcfg = config.Default()
			}
			s, err := workspace.New(root, rcfg)
			if err != nil {
				errLog = append(errLog, root+": "+err.Error())
				continue
			}
			repos[root] = s
		}
	}

	// No CharLimit: the same input holds generated PR titles and bodies,
	// which a limit would silently truncate.
	ti := textinput.New()
	ti.Placeholder = "New branch name"
	ti.Width = 30

	return &Model{
		svc:                    svc,
		cfg:                    cfg,
		repoRoot:               repoRoot,
		repos:                  repos,
		noRepo:                 noRepo,
		errLog:                 errLog,
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
		scanPluginsCmd,
		tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return prStatusTickMsg{} }),
		tea.Tick(10*time.Second, func(t time.Time) tea.Msg { return refreshTickMsg{} }),
	)
}

// Run is the entry point for the TUI application. all opens the dashboard
// across every repository with state rather than the one the cwd is in.
func Run(all bool) error {
	m, err := NewModel(all)
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
