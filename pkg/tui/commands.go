package tui

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/bootstrap"
	"github.com/axelgar/opentree/pkg/chat"
	"github.com/axelgar/opentree/pkg/github"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/skills"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/workspace"
)

// copyErrLogCmd puts the whole log on the system clipboard. The whole log
// rather than the entry under some cursor: there is no cursor here, and an
// error worth reporting is usually the one before or after the one that got
// noticed. The lines are snapshotted before the command runs, so a failure
// arriving while the log is being written to still copies what was on screen.
func (m Model) copyErrLogCmd() tea.Cmd {
	entries := append([]string(nil), m.errLog...)
	text := strings.Join(entries, "\n") + "\n"
	return func() tea.Msg {
		if err := copyToClipboard(text); err != nil {
			return errLogCopiedMsg{err: err}
		}
		return errLogCopiedMsg{count: len(entries)}
	}
}

// baseOr returns base, falling back to the configured default base branch
// for workspaces persisted before the base was recorded.
func (m Model) baseOr(base string) string {
	if base != "" {
		return base
	}
	return m.cfg.Worktree.DefaultBase
}

func (m Model) loadWorkspacesCmd() tea.Msg {
	// Re-read state from disk so workspaces created or deleted by another
	// process (CLI in a second terminal) show up on the periodic refresh.
	// On failure, fall back to the in-memory snapshot; any mutation will
	// surface the same error with context.
	_ = m.stateStore.Load()
	saved := m.stateStore.ListWorkspaces()

	windows, _ := m.svc.Process().ListWindows()

	windowMap := make(map[string]workspace.Window)
	for _, w := range windows {
		windowMap[w.Name] = w
	}

	items := make([]WorkspaceItem, len(saved))

	var wg sync.WaitGroup
	wg.Add(len(saved))
	for i, ws := range saved {
		go func(i int, ws *state.Workspace) {
			defer wg.Done()

			diff, fileChanges, err := m.worktreeMgr.DiffStats(ws.Branch, m.baseOr(ws.BaseBranch))
			diffStat := "No changes"
			if err != nil {
				diffStat = "diff unavailable"
			} else {
				lines := strings.Split(strings.TrimSpace(diff), "\n")
				if len(lines) > 0 && lines[len(lines)-1] != "" {
					diffStat = lines[len(lines)-1]
				}
			}

			win, exists := windowMap[ws.Name]
			sanitizedName := gitutil.SanitizeBranchName(ws.Name)
			if !exists {
				win, exists = windowMap[sanitizedName]
			}

			_, serving := windowMap[m.svc.ServerWindow(ws.Name)]
			// Dialled only for a server that could be up: a port nothing was
			// started on has nothing to say, and the refresh cannot afford
			// pointless waits.
			listening := serving && ws.Port != 0 && bootstrap.Listening(ws.Port)

			item := WorkspaceItem{
				Workspace:       ws,
				DiffStat:        diffStat,
				Active:          exists && win.Active,
				FileChanges:     fileChanges,
				ServerRunning:   serving,
				ServerListening: listening,
			}
			if exists {
				item.WindowID = win.ID
			}

			if ws.WorktreeDir != "" {
				item.UncommittedCount = countUncommitted(ws.WorktreeDir)
				item.ChatStatus = readChatStatus(m.repoRoot, ws.Name)
				// A couple of stats per workspace, so it rides the same refresh
				// rather than needing a poll of its own.
				item.MissingSkills = skills.Missing(m.repoRoot, ws.WorktreeDir)
			}

			if exists {
				if t, err := m.svc.Process().GetWindowActivity(ws.Name); err == nil {
					item.LastActivity = t
				}
			}

			items[i] = item
		}(i, ws)
	}
	wg.Wait()

	// Asked once per refresh rather than once per row: whether portless can
	// serve is a fact about this machine. Only asked at all when the project
	// configures a server, since it is a LookPath and a dial for nothing
	// otherwise.
	var portless bootstrap.Portless
	if m.cfg.Workspace.Run != "" {
		portless = bootstrap.CheckPortless()
	}

	return loadedWorkspacesMsg{workspaces: items, portless: portless}
}

func (m Model) createWorkspaceCmd(name, baseBranch string) tea.Cmd {
	return func() tea.Msg {
		ws, err := m.svc.Create(name, baseBranch)
		if err != nil {
			return errMsg{err}
		}
		return createdWorkspaceMsg{wsName: ws.Name, branch: ws.Branch, worktreeDir: ws.WorktreeDir}
	}
}

func (m Model) createWorkspaceFromRemoteCmd(branchName string) tea.Cmd {
	return func() tea.Msg {
		ws, err := m.svc.CreateFromRemoteBranch(branchName)
		if err != nil {
			return errMsg{err}
		}
		return createdWorkspaceMsg{wsName: ws.Name, branch: ws.Branch, worktreeDir: ws.WorktreeDir}
	}
}

func (m Model) loadRemoteBranchesCmd() tea.Cmd {
	return func() tea.Msg {
		// 50 keeps an exactly-typed branch likely to be in the loaded set
		// (the list is recency-sorted; the local for-each-ref call is cheap).
		branches, err := gitutil.ListRemoteBranches(m.repoRoot, 50)
		return remoteBranchesLoadedMsg{branches: branches, err: err}
	}
}

// filterBranches returns the subset of branches that contain query (case-insensitive).
// If query is empty, all branches are returned.
func filterBranches(branches []string, query string) []string {
	if query == "" {
		return branches
	}
	q := strings.ToLower(query)
	var out []string
	for _, b := range branches {
		if strings.Contains(strings.ToLower(b), q) {
			out = append(out, b)
		}
	}
	return out
}

func (m Model) createWorkspaceFromIssueCmd(issueNumStr string) tea.Cmd {
	return func() tea.Msg {
		issueNum, err := strconv.Atoi(strings.TrimSpace(issueNumStr))
		if err != nil || issueNum <= 0 {
			return errMsg{fmt.Errorf("invalid issue number: %s", issueNumStr)}
		}
		ws, err := m.svc.CreateFromIssue(issueNum, m.cfg.Worktree.DefaultBase)
		if err != nil {
			return errMsg{err}
		}
		return createdWorkspaceMsg{wsName: ws.Name, branch: ws.Branch, worktreeDir: ws.WorktreeDir}
	}
}

func (m Model) deleteWorkspaceCmd(name string) tea.Cmd {
	return func() tea.Msg {
		if err := m.svc.Delete(name); err != nil {
			return errMsg{err}
		}
		return deletedWorkspaceMsg{names: []string{name}}
	}
}

// toggleServerCmd starts a workspace's dev server, or stops it if it is already
// running. One key for both because the state is on screen: the row says
// whether a server is up, so the key means "change that".
//
// Which of the two it does is decided here rather than from what the list last
// drew — a refresh is up to a tick stale, and a server killed by hand in its own
// window would otherwise be "stopped" a second time.
func (m Model) toggleServerCmd(name string) tea.Cmd {
	return func() tea.Msg {
		if m.svc.ServerRunning(name) {
			if err := m.svc.StopServer(name); err != nil {
				return errMsg{err}
			}
			return serverToggledMsg{wsName: name, action: "server stopped"}
		}
		port, err := m.svc.StartServer(name)
		if err != nil {
			return errMsg{err}
		}
		return serverToggledMsg{wsName: name, action: fmt.Sprintf("server started on :%d", port)}
	}
}

// startServerCmd, stopServerCmd and restartServerCmd are the Servers tab's
// three actions. They are separate rather than one toggle because that tab
// draws the state it is acting on — a row that says "stopped" wants a key that
// means start, not one that means "the other thing".
func (m Model) startServerCmd(name string) tea.Cmd {
	return func() tea.Msg {
		port, err := m.svc.StartServer(name)
		if err != nil {
			return errMsg{err}
		}
		return serverToggledMsg{wsName: name, action: fmt.Sprintf("server started on :%d", port)}
	}
}

func (m Model) stopServerCmd(name string) tea.Cmd {
	return func() tea.Msg {
		if err := m.svc.StopServer(name); err != nil {
			return errMsg{err}
		}
		return serverToggledMsg{wsName: name, action: "server stopped"}
	}
}

// restartServerCmd is the everyday one: a config file the server does not watch,
// a dependency it loaded at boot. Stopping a server that is not running is not
// an error here — restart means "be running, freshly".
func (m Model) restartServerCmd(name string) tea.Cmd {
	return func() tea.Msg {
		if m.svc.ServerRunning(name) {
			if err := m.svc.StopServer(name); err != nil {
				return errMsg{err}
			}
		}
		port, err := m.svc.StartServer(name)
		if err != nil {
			return errMsg{err}
		}
		return serverToggledMsg{wsName: name, action: fmt.Sprintf("server restarted on :%d", port)}
	}
}

// attachServerCmd opens the window the server is running in, which holds all of
// its output — the answer to "where did that log line go".
func (m Model) attachServerCmd(name string) tea.Cmd {
	return func() tea.Msg {
		cmd, err := m.svc.Process().AttachCmd(m.svc.ServerWindow(name))
		if err != nil {
			return errMsg{fmt.Errorf("failed to attach to %s's server: %w", name, err)}
		}
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			if err != nil {
				err = fmt.Errorf("failed to attach to %s's server: %w", name, err)
			}
			return attachFinishedMsg{err: err}
		})()
	}
}

func (m Model) batchDeleteWorkspaceCmd(names []string) tea.Cmd {
	return func() tea.Msg {
		return batchDeleteResult(names, m.svc.DeleteMultiple(names))
	}
}

// batchDeleteResult turns DeleteMultiple's single verdict on a whole batch into
// what the dashboard has to do about it.
//
// A batch that half worked used to come back as a bare errMsg, which is the one
// path that does not refresh the list — so the workspaces that really had gone
// stayed on screen, spinner and all, until the ten-second tick swept them away.
// The successes are reported first so the list corrects itself, and the failure
// follows so the banner still says what went wrong and to which of them.
func batchDeleteResult(names []string, err error) tea.Msg {
	if err == nil {
		return deletedWorkspaceMsg{names: names}
	}
	gone := deletedDespite(err)
	failed := func() tea.Msg { return errMsg{oneLine(err)} }
	if len(gone) == 0 {
		return failed()
	}
	return tea.Batch(func() tea.Msg { return deletedWorkspaceMsg{names: gone} }, failed)()
}

// deletedDespite is the workspaces a failed batch delete still finished, so the
// list can drop them instead of showing rows for worktrees that have gone.
//
// Asked of the error rather than read out of it. DeleteMultiple returns a typed
// error carrying the two lists, which is the only reliable way to know: the
// first attempt at this parsed names out of the message text and treated
// anything unmentioned as deleted — and the message names the successes too, in
// its "(deleted a, b)" tail, so every one of them looked blamed and the list
// never refreshed at all.
//
// Any other error means the batch did not report per-workspace outcomes, and
// nothing is assumed to have gone.
func deletedDespite(err error) []string {
	var batch *workspace.DeleteBatchError
	if errors.As(err, &batch) {
		return batch.Deleted
	}
	return nil
}

// oneLine folds a joined error onto a single line. errors.Join separates its
// causes with newlines and the error banner is one row tall, so a join left as
// it is shows its first cause and silently hides every other.
func oneLine(err error) error {
	text := strings.TrimSpace(err.Error())
	if !strings.Contains(text, "\n") {
		return err
	}
	return errors.New(strings.Join(strings.Split(text, "\n"), "; "))
}

func (m Model) attachWorkspaceCmd(name string) tea.Cmd {
	return func() tea.Msg {
		// Quitting the chat closes its window; the worktree and the agent
		// conversation both survive, so attaching reopens rather than refuses.
		if _, err := m.svc.EnsureWindow(name); err != nil {
			return errMsg{err}
		}
		cmd, err := m.svc.Process().AttachCmd(name)
		if err != nil {
			return errMsg{err}
		}
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			if err != nil {
				// tmux's own message is lost to ExecProcess, so a bare
				// "exit status 1" is all that survives — at least name the
				// workspace it was trying to reach.
				err = fmt.Errorf("failed to attach to %q: %w", name, err)
			}
			return attachFinishedMsg{err: err}
		})()
	}
}

func (m Model) generatePRContentCmd(ws WorkspaceItem) tea.Cmd {
	base := m.baseOr(ws.BaseBranch)
	return func() tea.Msg {
		title, body := workspace.GeneratePRContent(ws.WorktreeDir, ws.Branch, base, ws.IssueNumber, ws.IssueTitle)
		return prContentGeneratedMsg{wsName: ws.Name, title: title, body: body}
	}
}

func (m Model) createPRCmd(wsName, title, body string) tea.Cmd {
	return func() tea.Msg {
		prURL, err := m.svc.CreatePR(wsName, title, body)
		if err != nil {
			return errMsg{err}
		}
		return prCreatedMsg{wsName: wsName, prURL: prURL}
	}
}

// checkBranchStatusCmd asks whether the branch is on the remote and what its
// pull request is doing. The two halves fail independently: git ls-remote needs
// neither gh nor GitHub, so on a GitLab remote, or with a lapsed gh login, it
// keeps answering long after the PR half stops. Discarding its answer with the
// error is why a push badge on such a repo never corrected itself — the poll ran
// every thirty seconds and threw away the half that worked every time.
//
// Exactly one message comes back either way: statusChecksInFlight counts one
// reply per check, and a check answering twice would let the next thirty-second
// round start on top of an unfinished one. The price is that a gh failure the
// ls-remote half survived reaches no log, since only statusCheckErrMsg writes
// there — worth revisiting the day branchStatusCheckedMsg can carry an error of
// its own.
func (m Model) checkBranchStatusCmd(wsName, branch, repoDir string, wasPushed bool) tea.Cmd {
	// gh failing says nothing about whether the branch merges cleanly, so the
	// last answer is carried forward rather than letting a zero value clear the
	// conflict badge until the next poll that does reach GitHub.
	knownConflicts := false
	if i := m.workspaceIndex(wsName); i >= 0 {
		knownConflicts = m.workspaces[i].MergeConflicts
	}
	return func() tea.Msg {
		status, err := m.prMgr.GetBranchAndPRStatus(branch, repoDir, wasPushed)
		return branchStatusResult(wsName, status, err, knownConflicts)
	}
}

// branchStatusResult decides how much of a failed poll is still worth having.
// GetBranchAndPRStatus returns whatever it managed to learn alongside its error,
// and RemoteCheckFailed is how it says the ls-remote half is not among it.
func branchStatusResult(wsName string, status github.BranchStatus, err error, knownConflicts bool) tea.Msg {
	if err != nil {
		if status.RemoteCheckFailed {
			return statusCheckErrMsg{err: err}
		}
		status.MergeConflicts = knownConflicts
	}
	return branchStatusCheckedMsg{wsName: wsName, status: status}
}

// sendReviewsCmd hands a workspace's open PR review comments to its agent as a
// prompt, over the chat's control socket. The socket refuses a prompt the chat
// cannot honour — mid-turn, or no chat running — so "sent" stays truthful.
func (m Model) sendReviewsCmd(ws WorkspaceItem) tea.Cmd {
	repoRoot, wsName, branch := m.repoRoot, ws.Name, ws.Branch
	return func() tea.Msg {
		comments, err := m.prMgr.FetchPRReviews(branch)
		// Partial results (top-level reviews fetched, inline-thread fetch
		// failed) are still sent rather than discarded.
		if err != nil && len(comments) == 0 {
			return errMsg{fmt.Errorf("failed to fetch PR reviews: %w", err)}
		}
		if len(comments) == 0 {
			return reviewsSentMsg{wsName: wsName}
		}
		if err := chat.Send(chat.SocketPath(repoRoot, wsName), wsName, chat.Command{
			Type: chat.CommandPrompt, Text: github.FormatReviewsPrompt(comments),
		}); err != nil {
			return errMsg{fmt.Errorf("%s: %w", wsName, err)}
		}
		return reviewsSentMsg{wsName: wsName, count: len(comments)}
	}
}

func (m Model) loadDiffCmd(ws WorkspaceItem) tea.Cmd {
	base := m.baseOr(ws.BaseBranch)
	return func() tea.Msg {
		content, err := m.worktreeMgr.DiffCombined(ws.Branch, base)
		if err != nil {
			return errMsg{err}
		}
		return diffLoadedMsg{content: content, wsName: ws.Name}
	}
}

// groupDiffSection is one sibling's contribution to a fan-out comparison.
type groupDiffSection struct {
	name    string
	agent   string
	content string
}

// loadGroupDiffCmd loads every sibling's combined diff into one document for
// the existing diff viewer. One scroll rather than a split pane, because the
// question a fan-out comparison answers — which agent's change do I keep — is
// answered by reading each approach whole, and the viewer already knows how
// to style the section headers that separate them.
//
// A sibling whose diff fails becomes an inline error section rather than
// failing the whole comparison: two readable approaches and one broken
// worktree is still a comparison worth seeing.
func (m Model) loadGroupDiffCmd(ws WorkspaceItem) tea.Cmd {
	var siblings []WorkspaceItem
	for _, w := range m.workspaces {
		if w.FanoutGroup == ws.FanoutGroup {
			siblings = append(siblings, w)
		}
	}
	sort.Slice(siblings, func(i, j int) bool { return siblings[i].Name < siblings[j].Name })
	group := ws.FanoutGroup
	return func() tea.Msg {
		sections := make([]groupDiffSection, 0, len(siblings))
		for _, sib := range siblings {
			content, err := m.worktreeMgr.DiffCombined(sib.Branch, m.baseOr(sib.BaseBranch))
			if err != nil {
				content = "(error: " + err.Error() + ")"
			}
			sections = append(sections, groupDiffSection{name: sib.Name, agent: sib.Agent, content: content})
		}
		title := fmt.Sprintf("%s · %d siblings", group, len(sections))
		return diffLoadedMsg{content: buildGroupDiff(sections), wsName: title}
	}
}

// buildGroupDiff joins the siblings' diffs under headers naming each one —
// the same ══════ shape DiffCombined itself emits for its committed and
// uncommitted halves, so renderDiffLine styles the seams between agents
// without the viewer learning that groups exist.
func buildGroupDiff(sections []groupDiffSection) string {
	parts := make([]string, 0, len(sections))
	for _, s := range sections {
		header := fmt.Sprintf("══════════ %s (%s) ══════════", s.name, s.agent)
		parts = append(parts, header+"\n\n"+strings.TrimSpace(s.content))
	}
	return strings.Join(parts, "\n\n")
}
