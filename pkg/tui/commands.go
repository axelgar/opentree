package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"unicode"

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
	gone := deletedDespite(names, err)
	failed := func() tea.Msg { return errMsg{oneLine(err)} }
	if len(gone) == 0 {
		return failed()
	}
	return tea.Batch(func() tea.Msg { return deletedWorkspaceMsg{names: gone} }, failed)()
}

// deletedDespite picks out the workspaces a failed batch delete still finished.
//
// DeleteMultiple joins one error per workspace it could not remove and names
// each of them, so the names the error does not mention are the ones that went.
// Reading names back out of an error is unlovely, and it is deliberately not
// coupled to the wording: a name is looked up as a whole identifier, so nothing
// here breaks if the joined error is phrased differently tomorrow. A name the
// error does not mention is assumed gone, which is the direction that
// self-corrects — deletedWorkspaceMsg reloads the list, and a workspace that is
// still there simply reappears.
func deletedDespite(names []string, err error) []string {
	blamed := identifiers(err.Error())
	gone := make([]string, 0, len(names))
	for _, name := range names {
		if !blamed[name] {
			gone = append(gone, name)
		}
	}
	return gone
}

// identifiers splits text into the runs that could be a workspace name, which
// is what keeps "delete feat/a: ..." from reading as a complaint about "a". The
// characters held together are the ones a branch name may contain.
func identifiers(text string) map[string]bool {
	set := make(map[string]bool)
	for _, field := range strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("-_./", r)
	}) {
		set[field] = true
	}
	return set
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
		title, body := generatePRContent(ws.Branch, base, ws.WorktreeDir, ws.IssueNumber, ws.IssueTitle)
		return prContentGeneratedMsg{wsName: ws.Name, title: title, body: body}
	}
}

func generatePRContent(branch, baseBranch, worktreeDir string, issueNumber int, issueTitle string) (title, body string) {
	var commits []string
	if worktreeDir != "" {
		if out, err := gitutil.Output(worktreeDir, "log", baseBranch+"..HEAD", "--format=%s", "--no-merges"); err == nil {
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if strings.TrimSpace(line) != "" {
					commits = append(commits, strings.TrimSpace(line))
				}
			}
		}
	}

	if issueTitle != "" {
		title = issueTitle
	} else {
		title = gitutil.BranchToTitle(branch)
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
		fmt.Fprintf(&sb, "Closes #%d\n", issueNumber)
	}
	return title, sb.String()
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
