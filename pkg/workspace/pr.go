package workspace

import (
	"fmt"
	"strings"

	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
)

// autopilotMarker is how a PR body written by opentree identifies itself, so
// PublishPR can tell its own description from a human's before rewriting one.
// An HTML comment renders as nothing on GitHub.
const autopilotMarker = "<!-- opentree:autopilot -->"

// GeneratePRContent builds a PR title and body from what the workspace already
// knows: the issue that started it, or the branch name, and the commits since
// base. It is the same content the dashboard's PR dialog prefills, exported so
// the CLI and autopilot produce the same PR a human would have accepted.
func GeneratePRContent(worktreeDir, branch, baseBranch string, issueNumber int, issueTitle string) (title, body string) {
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

// PublishOutcome is what PublishPR did, spelled out because "publish" is
// several verbs and the caller reports which ones actually happened.
type PublishOutcome struct {
	PRURL   string
	Created bool
	Pushed  bool
	// Skipped is non-empty when nothing was done, and says why — a merged PR,
	// or auto_push off with nothing on the remote. Not an error: the workspace
	// is in a state somebody chose, and publishing declines to argue with it.
	Skipped string
}

// PublishPR drives a branch to a published pull request without ever
// duplicating one: push only what is not already pushed, create only when no
// PR exists, and rewrite a description only when opentree wrote it.
//
// The safeguards exist because publishing is no longer only a human pressing
// `p`: autopilot calls this after a green check, and the agent it supervises
// may itself have pushed or opened the PR mid-turn. Every path here is
// therefore a comparison first and a mutation second, and the no-op — open PR,
// remote already at the local commit — is a supported outcome, not a failure.
//
// Empty title and body mean "generate them": the same content the dashboard's
// PR dialog prefills, from the issue or branch name and the commits since
// base.
func (s *Service) PublishPR(name, title, body string) (PublishOutcome, error) {
	var out PublishOutcome

	ws, err := s.state.GetWorkspace(name)
	if err != nil {
		return out, fmt.Errorf("workspace not found: %w", err)
	}
	if !s.github.IsInstalled() {
		return out, fmt.Errorf("gh CLI is not installed — install it from https://cli.github.com/")
	}

	if title == "" && body == "" {
		title, body = GeneratePRContent(ws.WorktreeDir, ws.Branch, ws.BaseBranch, ws.IssueNumber, ws.IssueTitle)
	}

	localSha, err := gitutil.LocalHead(ws.WorktreeDir)
	if err != nil {
		return out, fmt.Errorf("failed to read the worktree's HEAD: %w", err)
	}
	remoteSha, err := gitutil.RemoteHead(ws.WorktreeDir, ws.Branch)
	if err != nil {
		return out, fmt.Errorf("failed to ask origin about %s: %w", ws.Branch, err)
	}

	pr, err := s.github.FindPR(ws.Branch, ws.WorktreeDir)
	if err != nil {
		return out, err
	}

	if pr != nil && pr.State != "open" {
		// A merged or closed PR is finished business. Creating a second one for
		// the same branch is a decision, and not autopilot's to make.
		out.PRURL, out.Skipped = pr.URL, fmt.Sprintf("the PR for this branch is %s", pr.State)
		return out, nil
	}

	if pr != nil {
		if localSha != remoteSha {
			if err := s.worktrees.Push(ws.Branch); err != nil {
				return out, fmt.Errorf("failed to push branch: %w", err)
			}
			out.Pushed = true
		}
		// The push is the update; the description is rewritten only where the
		// marker proves opentree wrote it. A body a human typed or edited —
		// including the agent opening the PR through gh itself — stays theirs.
		if strings.Contains(pr.Body, autopilotMarker) {
			if err := s.github.UpdatePR(ws.Branch, title, body+"\n\n"+autopilotMarker); err != nil {
				return out, err
			}
		}
		out.PRURL = pr.URL
		s.recordPublished(name, pr.URL)
		return out, nil
	}

	autoPush := s.cfg.GitHub.AutoPush != nil && *s.cfg.GitHub.AutoPush
	if !autoPush && remoteSha == "" {
		// The user switched auto_push off, and nothing is on the remote to
		// build a PR from. Pushing anyway would override the one setting whose
		// entire meaning is "I push myself".
		out.Skipped = "auto_push is off and the branch is not pushed — push it, then publish again"
		return out, nil
	}
	if autoPush && localSha != remoteSha {
		if err := s.worktrees.Push(ws.Branch); err != nil {
			return out, fmt.Errorf("failed to push branch: %w", err)
		}
		out.Pushed = true
	}

	prURL, err := s.github.CreatePR(ws.Branch, ws.BaseBranch, title, body+"\n\n"+autopilotMarker)
	if err != nil {
		return out, fmt.Errorf("failed to create PR: %w", err)
	}
	out.PRURL, out.Created = prURL, true
	s.recordPublished(name, prURL)
	return out, nil
}

// recordPublished is the best-effort state write after a publish: the 30s
// status poll self-corrects all of it from gh and ls-remote, so a failed write
// must not fail a PR that exists.
func (s *Service) recordPublished(name, prURL string) {
	_ = s.state.Update(name, func(w *state.Workspace) error {
		w.PRURL, w.PRStatus, w.BranchPushed = prURL, "open", true
		return nil
	})
}
