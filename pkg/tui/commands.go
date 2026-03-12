package tui

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
	"github.com/axelgar/opentree/pkg/workspace"
)

func (m Model) loadWorkspacesCmd() tea.Msg {
	saved := m.stateStore.ListWorkspaces()

	windows, err := m.svc.Process().ListWindows()
	if err != nil {
		// Log error but continue
	}

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

			diff, fileChanges, err := m.worktreeMgr.DiffStats(ws.Branch, ws.BaseBranch)
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

			item := WorkspaceItem{
				Workspace:   ws,
				DiffStat:    diffStat,
				Active:      exists && win.Active,
				FileChanges: fileChanges,
			}
			if exists {
				item.WindowID = win.ID
			}

			if ws.WorktreeDir != "" {
				item.UncommittedCount = countUncommitted(ws.WorktreeDir)
				item.AgentStatus = readAgentStatus(ws.WorktreeDir)
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

	return loadedWorkspacesMsg{workspaces: items}
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
		branches, _ := gitutil.ListRemoteBranches(m.repoRoot, 10)
		return remoteBranchesLoadedMsg{branches: branches}
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
		return deletedWorkspaceMsg{}
	}
}

func (m Model) batchDeleteWorkspaceCmd(names []string) tea.Cmd {
	return func() tea.Msg {
		if err := m.svc.DeleteMultiple(names); err != nil {
			return errMsg{err}
		}
		return deletedWorkspaceMsg{}
	}
}

func (m Model) attachWorkspaceCmd(name string) tea.Cmd {
	return func() tea.Msg {
		cmd, err := m.svc.Process().AttachCmd(name)
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

func (m Model) createPRCmd(wsName, title, body string) tea.Cmd {
	return func() tea.Msg {
		prURL, err := m.svc.CreatePR(wsName, title, body)
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

func (m Model) checkCIStatusCmd(wsName, branch string) tea.Cmd {
	return func() tea.Msg {
		status, err := m.prMgr.GetPRCIStatus(branch)
		if err != nil || status == "" {
			return nil
		}
		return ciStatusCheckedMsg{wsName: wsName, ciStatus: status}
	}
}

func (m Model) checkBranchStatusCmd(wsName, branch, repoDir string, wasPushed bool) tea.Cmd {
	return func() tea.Msg {
		status, err := m.prMgr.GetBranchAndPRStatus(branch, repoDir, wasPushed)
		if err != nil {
			return nil
		}
		return branchStatusCheckedMsg{wsName: wsName, status: status}
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
		output, err := m.svc.Process().CapturePane(wsName, 5)
		if err != nil {
			return capturePreviewMsg{lines: ""}
		}
		return capturePreviewMsg{lines: cleanPreview(output)}
	}
}

func (m Model) sendReviewsCmd(wsName string) tea.Cmd {
	return func() tea.Msg {
		count, err := m.svc.SendReviewsToAgent(wsName)
		if err != nil {
			return errMsg{err}
		}
		return reviewsSentMsg{wsName: wsName, count: count}
	}
}

func (m Model) loadDiffCmd(ws WorkspaceItem) tea.Cmd {
	return func() tea.Msg {
		committed, err := m.worktreeMgr.DiffFull(ws.Branch, ws.BaseBranch)
		if err != nil {
			return errMsg{err}
		}

		uncommitted, _ := m.worktreeMgr.DiffUncommitted(ws.Branch)

		committedTrimmed := strings.TrimSpace(committed)
		uncommittedTrimmed := strings.TrimSpace(uncommitted)

		var sections []string

		if committedTrimmed != "" {
			header := "══════ Committed Changes ══════"
			if uncommittedTrimmed != "" {
				sections = append(sections, header+"\n\n"+committedTrimmed)
			} else {
				sections = append(sections, committedTrimmed)
			}
		}

		if uncommittedTrimmed != "" {
			header := "══════ Uncommitted Changes ══════"
			sections = append(sections, header+"\n\n"+uncommittedTrimmed)
		}

		content := strings.Join(sections, "\n\n")
		if content == "" {
			content = "No changes."
		}

		return diffLoadedMsg{content: content, wsName: ws.Name}
	}
}
