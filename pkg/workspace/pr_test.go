package workspace

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/axelgar/opentree/pkg/config"
	"github.com/axelgar/opentree/pkg/github"
	"github.com/axelgar/opentree/pkg/gitutil"
	"github.com/axelgar/opentree/pkg/state"
)

// publishFixture is a workspace on a real repo with a real origin, plus the
// mock gh that records what PublishPR asked of it.
func publishFixture(t *testing.T, name string, gh *mockGitHubManager) (*Service, *state.Workspace) {
	t.Helper()
	if !isGitAvailable() {
		t.Skip("git not available")
	}
	localDir := initRepoWithRemote(t, "feat/seed")
	cfg := config.Default()
	useAgent(t, cfg)
	cfg.Worktree.BaseDir = ".opentree"

	svc, err := newWithMockFull(localDir, cfg, &mockProcessManager{}, gh)
	if err != nil {
		t.Fatalf("newWithMockFull: %v", err)
	}
	ws, err := svc.Create(name, "main")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return svc, ws
}

// pushBranch puts the workspace's current commit on origin, so local and
// remote agree the way they do after an agent ran `git push` itself.
func pushBranch(t *testing.T, svc *Service, ws *state.Workspace) {
	t.Helper()
	cmd := exec.Command("git", "push", "origin", ws.Branch)
	cmd.Dir = svc.WorktreePath(ws.Name)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("push: %v\n%s", err, out)
	}
}

func TestPublishPR_CreatesWithGeneratedContentAndMarker(t *testing.T) {
	gh := &mockGitHubManager{createPRResult: "https://github.com/acme/repo/pull/1"}
	svc, ws := publishFixture(t, "feat/fresh", gh)

	out, err := svc.PublishPR(ws.Name, "", "")
	if err != nil {
		t.Fatalf("PublishPR: %v", err)
	}
	if !out.Created || !out.Pushed || out.Skipped != "" {
		t.Errorf("outcome = %+v, want Created and Pushed", out)
	}
	if len(gh.createPRCalls) != 1 {
		t.Fatalf("CreatePR calls = %d, want 1", len(gh.createPRCalls))
	}
	title, body, _ := strings.Cut(gh.createPRCalls[0], "\x00")
	if title != gitutil.BranchToTitle(ws.Branch) {
		t.Errorf("generated title = %q, want the branch read as a sentence", title)
	}
	if !strings.Contains(body, autopilotMarker) {
		t.Error("a body opentree wrote must carry the marker, or the next publish cannot tell it from a human's")
	}

	// Persisted so the dashboard and the poll pick it up without a fetch.
	got, err := svc.state.GetWorkspace(ws.Name)
	if err != nil {
		t.Fatalf("GetWorkspace: %v", err)
	}
	if got.PRURL != out.PRURL || got.PRStatus != "open" || !got.BranchPushed {
		t.Errorf("persisted %q/%q/pushed=%v, want the created PR recorded", got.PRURL, got.PRStatus, got.BranchPushed)
	}
}

func TestPublishPR_OpenPRAlreadyCurrentIsANoOp(t *testing.T) {
	gh := &mockGitHubManager{}
	svc, ws := publishFixture(t, "feat/done", gh)
	pushBranch(t, svc, ws)
	gh.findPRResult = &github.PRInfo{URL: "https://github.com/acme/repo/pull/2", State: "open", Body: "written by hand"}

	out, err := svc.PublishPR(ws.Name, "", "")
	if err != nil {
		t.Fatalf("PublishPR: %v", err)
	}
	if out.Created || out.Pushed || out.Skipped != "" || out.PRURL != gh.findPRResult.URL {
		t.Errorf("outcome = %+v, want the quiet no-op", out)
	}
	if len(gh.createPRCalls) != 0 || len(gh.updatePRCalls) != 0 {
		t.Errorf("create=%d update=%d, want the agent's own PR left exactly alone", len(gh.createPRCalls), len(gh.updatePRCalls))
	}
}

func TestPublishPR_PushesBehindOpenPRWithoutCreating(t *testing.T) {
	gh := &mockGitHubManager{}
	svc, ws := publishFixture(t, "feat/behind", gh)
	// Not pushed: the remote has never seen this branch, but a PR exists for
	// it — the shape left behind when a human deleted and recreated commits.
	gh.findPRResult = &github.PRInfo{URL: "https://github.com/acme/repo/pull/3", State: "open", Body: "human words"}

	out, err := svc.PublishPR(ws.Name, "", "")
	if err != nil {
		t.Fatalf("PublishPR: %v", err)
	}
	if out.Created || !out.Pushed {
		t.Errorf("outcome = %+v, want a push and no create", out)
	}
	if len(gh.updatePRCalls) != 0 {
		t.Error("a body without the marker was rewritten")
	}
}

func TestPublishPR_RewritesOnlyItsOwnBody(t *testing.T) {
	gh := &mockGitHubManager{}
	svc, ws := publishFixture(t, "feat/mine", gh)
	pushBranch(t, svc, ws)
	gh.findPRResult = &github.PRInfo{
		URL:   "https://github.com/acme/repo/pull/4",
		State: "open",
		Body:  "## Changes\n\n- older\n\n" + autopilotMarker,
	}

	out, err := svc.PublishPR(ws.Name, "New title", "New body")
	if err != nil {
		t.Fatalf("PublishPR: %v", err)
	}
	if len(gh.updatePRCalls) != 1 {
		t.Fatalf("UpdatePR calls = %d, want 1 — the marker says this body is ours to rewrite", len(gh.updatePRCalls))
	}
	title, body, _ := strings.Cut(gh.updatePRCalls[0], "\x00")
	if title != "New title" || !strings.Contains(body, autopilotMarker) {
		t.Errorf("update = %q / %q, want the new content still marked", title, body)
	}
	if out.PRURL != gh.findPRResult.URL {
		t.Errorf("PRURL = %q, want the existing PR's", out.PRURL)
	}
}

func TestPublishPR_RefusesToRepublishAFinishedPR(t *testing.T) {
	gh := &mockGitHubManager{}
	svc, ws := publishFixture(t, "feat/merged", gh)
	gh.findPRResult = &github.PRInfo{URL: "https://github.com/acme/repo/pull/5", State: "merged"}

	out, err := svc.PublishPR(ws.Name, "", "")
	if err != nil {
		t.Fatalf("PublishPR: %v", err)
	}
	if out.Skipped == "" || !strings.Contains(out.Skipped, "merged") {
		t.Errorf("Skipped = %q, want it to name the merged PR", out.Skipped)
	}
	if len(gh.createPRCalls) != 0 {
		t.Error("a second PR was created for a merged branch")
	}
}

func TestPublishPR_HonoursAutoPushOff(t *testing.T) {
	gh := &mockGitHubManager{createPRResult: "https://github.com/acme/repo/pull/6"}
	svc, ws := publishFixture(t, "feat/manual", gh)
	off := false
	svc.cfg.GitHub.AutoPush = &off

	out, err := svc.PublishPR(ws.Name, "", "")
	if err != nil {
		t.Fatalf("PublishPR: %v", err)
	}
	if out.Skipped == "" {
		t.Fatalf("outcome = %+v, want a skip: auto_push is off and nothing is on the remote", out)
	}
	if len(gh.createPRCalls) != 0 {
		t.Error("a PR was created from a branch the user chose to push themselves and had not")
	}

	// Once the user pushes, the same call publishes without pushing further.
	pushBranch(t, svc, ws)
	out, err = svc.PublishPR(ws.Name, "", "")
	if err != nil {
		t.Fatalf("PublishPR after manual push: %v", err)
	}
	if !out.Created || out.Pushed {
		t.Errorf("outcome = %+v, want a create with no push", out)
	}
}
