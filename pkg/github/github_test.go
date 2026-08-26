package github

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isGHAvailable returns true when the gh CLI is found on PATH.
func isGHAvailable() bool {
	return exec.Command("gh", "--version").Run() == nil
}

// skipIfGHUnauthenticated skips when gh is installed but not authenticated,
// since PR-view failures other than "no PR" now surface as real errors.
func skipIfGHUnauthenticated(t *testing.T) {
	t.Helper()
	if isGHAvailable() && exec.Command("gh", "auth", "status").Run() != nil {
		t.Skip("gh installed but not authenticated")
	}
}

func TestPRViewError_NoPRVsRealFailure(t *testing.T) {
	exitErr := errors.New("exit status 1")
	if err := prViewError([]byte(`no pull requests found for branch "x"`), exitErr); err != nil {
		t.Errorf("prViewError() on 'no pull requests found' = %v, want nil", err)
	}
	if err := prViewError([]byte("no git remotes found"), exitErr); err != nil {
		t.Errorf("prViewError() on 'no git remotes found' = %v, want nil", err)
	}
	err := prViewError([]byte("HTTP 401: Bad credentials"), exitErr)
	if err == nil {
		t.Fatal("prViewError() on auth failure = nil, want error")
	}
	if !strings.Contains(err.Error(), "Bad credentials") {
		t.Errorf("prViewError() = %q, want to contain the gh output", err.Error())
	}
}

// ---- which repositories gh can be asked about at all ----

func TestRemoteHost(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://github.com/acme/tool.git", "github.com"},
		{"http://github.com/acme/tool", "github.com"},
		{"https://user:token@github.com/acme/tool.git", "github.com"},
		{"ssh://git@github.com/acme/tool.git", "github.com"},
		{"ssh://git@github.acme.io:2222/acme/tool.git", "github.acme.io"},
		{"git://github.com/acme/tool.git", "github.com"},
		{"git@github.com:acme/tool.git", "github.com"},
		{"github.com:acme/tool.git", "github.com"},
		{"git@GitHub.COM:acme/tool.git", "github.com"},
		{"  https://github.com/acme/tool.git\t", "github.com"},
		// No host to speak of: a local clone, a relative path, nothing at all.
		{"/srv/git/tool.git", ""},
		{"file:///srv/git/tool.git", ""},
		{"../sibling", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := remoteHost(tt.url); got != tt.want {
			t.Errorf("remoteHost(%q) = %q, want %q", tt.url, got, tt.want)
		}
	}
}

// The unrecognised hosts below are the deliberate half of this: a self-hosted
// GitHub and a self-hosted GitLab are indistinguishable by hostname, and the
// tie is broken in favour of asking gh, so that an Enterprise user with an
// expired token still sees the failure instead of silence.
func TestHostIsGitHub(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"github.com", true},
		{"codeload.github.com", true},
		{"github.acme.io", true}, // GitHub Enterprise, named the obvious way
		{"git.acme.io", true},    // Enterprise, named some other way
		{"gitlab.acme.io", true}, // self-hosted GitLab: a poll wasted, on purpose
		{"gitlab.com", false},
		{"altssh.gitlab.com", false},
		{"bitbucket.org", false},
		{"git.sr.ht", false},
		{"ssh.dev.azure.com", false},
		{"acme.visualstudio.com", false},
		{"codeberg.org", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := hostIsGitHub(tt.host); got != tt.want {
			t.Errorf("hostIsGitHub(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestAnyGitHubRemote(t *testing.T) {
	tests := []struct {
		name    string
		listing string
		want    bool
	}{
		{"no remotes", "", false},
		{"github origin", "origin\thttps://github.com/acme/tool.git (fetch)\n" +
			"origin\thttps://github.com/acme/tool.git (push)\n", true},
		{"gitlab only", "origin\tgit@gitlab.com:acme/tool.git (fetch)\n" +
			"origin\tgit@gitlab.com:acme/tool.git (push)\n", false},
		{"gitlab origin, github upstream", "origin\tgit@gitlab.com:acme/tool.git (fetch)\n" +
			"upstream\thttps://github.com/acme/tool.git (fetch)\n", true},
		{"enterprise host", "origin\tgit@github.acme.io:acme/tool.git (fetch)\n", true},
		{"local mirror", "origin\t/srv/git/tool.git (fetch)\n", false},
		{"a name with no URL", "origin\n", false},
	}
	for _, tt := range tests {
		if got := anyGitHubRemote([]byte(tt.listing)); got != tt.want {
			t.Errorf("%s: anyGitHubRemote() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// stubGH puts a fake gh first on PATH, running the given shell body with the
// subcommand in $1. The stub always exits 0: IsInstalled asks `gh --version`
// before anything else and caches the answer, and CreatePR asks `gh auth
// status`, so a stub that failed those would gate every call before the code
// under test was reached.
func stubGH(t *testing.T, body string) {
	t.Helper()
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "gh"), []byte("#!/bin/sh\n"+body+"\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// runGit runs a git command in dir and fails the test if it does not succeed.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// tempRepoWithRemotes creates a git repository whose remotes are the given
// URLs, named origin, upstream, fork in that order.
func tempRepoWithRemotes(t *testing.T, urls ...string) string {
	t.Helper()
	names := []string{"origin", "upstream", "fork"}
	dir := t.TempDir()
	runGit(t, dir, "init")
	for i, url := range urls {
		runGit(t, dir, "remote", "add", names[i], url)
	}
	return dir
}

func TestHasGitHubRemote(t *testing.T) {
	if exec.Command("git", "--version").Run() != nil {
		t.Skip("git not available")
	}
	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"gitlab only", tempRepoWithRemotes(t, "git@gitlab.com:acme/tool.git"), false},
		{"fork with a github upstream", tempRepoWithRemotes(t,
			"git@gitlab.com:acme/tool.git", "https://github.com/acme/tool.git"), true},
		{"github enterprise", tempRepoWithRemotes(t, "git@github.acme.io:acme/tool.git"), true},
		{"no remotes at all", tempRepoWithRemotes(t), false},
		// Not a repository, so not a question about remotes — gh keeps its say
		// and gets to produce its own diagnosis.
		{"not a repository", t.TempDir(), true},
	}
	for _, tt := range cases {
		if got := hasGitHubRemote(tt.dir); got != tt.want {
			t.Errorf("%s: hasGitHubRemote() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// Regression: a workspace on a GitLab remote used to pay a full `gh pr view`
// on every thirty-second status poll, for an answer gh could only ever give as
// an error. The Enterprise half of this test is what keeps the first half
// honest: if the gate stopped calling gh altogether, it would fail.
func TestGetBranchAndPRStatus_SkipsGHForNonGitHubRemote(t *testing.T) {
	if exec.Command("git", "--version").Run() != nil {
		t.Skip("git not available")
	}
	marker := filepath.Join(t.TempDir(), "gh-was-asked")
	// A gh that records every `pr` invocation and answers a PR-shaped nothing.
	stubGH(t, "if [ \"$1\" = \"pr\" ]; then\n"+
		"  echo \"$@\" >> "+marker+"\n"+
		"  echo '{\"url\":\"\",\"state\":\"\",\"mergeable\":\"\",\"statusCheckRollup\":[]}'\n"+
		"fi")

	// origin is a local path that does not exist, so ls-remote fails at once
	// and nothing in this test reaches the network.
	unreachable := filepath.Join(t.TempDir(), "nowhere.git")

	gitlab := tempRepoWithRemotes(t, unreachable, "git@gitlab.com:acme/tool.git")
	status, err := New().GetBranchAndPRStatus("main", gitlab, false)
	if err != nil {
		t.Fatalf("GetBranchAndPRStatus: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("gh was asked about a repository with no GitHub remote")
	}
	if !status.RemoteCheckFailed {
		t.Error("the ls-remote half of the status was not reported; only the gh half should be skipped")
	}

	enterprise := tempRepoWithRemotes(t, unreachable, "git@github.acme.io:acme/tool.git")
	if _, err := New().GetBranchAndPRStatus("main", enterprise, false); err != nil {
		t.Fatalf("GetBranchAndPRStatus: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("gh was not asked about a GitHub Enterprise remote: %v", err)
	}
}

// The stub below would answer "failure" if it were consulted, so an empty
// status is proof that the GitLab checkout never reached gh.
func TestGetPRCIStatus_NonGitHubRemoteNeverAsksGH(t *testing.T) {
	if exec.Command("git", "--version").Run() != nil {
		t.Skip("git not available")
	}
	stubGH(t, `if [ "$1" = "pr" ]; then echo '{"statusCheckRollup":[{"state":"FAILURE"}]}'; fi`)

	t.Chdir(tempRepoWithRemotes(t, "git@gitlab.com:acme/tool.git"))
	status, err := New().GetPRCIStatus("main")
	if err != nil {
		t.Fatalf("GetPRCIStatus: %v", err)
	}
	if status != "" {
		t.Errorf("GetPRCIStatus() = %q for a GitLab checkout, want %q", status, "")
	}
}

func TestGetPRCIStatus_DecodesRollup(t *testing.T) {
	if exec.Command("git", "--version").Run() != nil {
		t.Skip("git not available")
	}
	stubGH(t, `if [ "$1" = "pr" ]; then echo '{"statusCheckRollup":[{"state":"FAILURE"}]}'; fi`)

	t.Chdir(tempRepoWithRemotes(t, "https://github.com/acme/tool.git"))
	status, err := New().GetPRCIStatus("main")
	if err != nil {
		t.Fatalf("GetPRCIStatus: %v", err)
	}
	if status != "failure" {
		t.Errorf("GetPRCIStatus() = %q, want %q", status, "failure")
	}
}

// A checkout gh cannot serve has no PR, and no PR has no reviews — the same
// silent, non-error condition as a branch nobody has opened a PR for.
func TestFetchPRReviews_NonGitHubRemoteIsNotAnError(t *testing.T) {
	if exec.Command("git", "--version").Run() != nil {
		t.Skip("git not available")
	}
	stubGH(t, `if [ "$1" = "pr" ]; then echo '{"url":"u","reviews":[`+
		`{"author":{"login":"alice"},"body":"Fix.","state":"CHANGES_REQUESTED"}]}'; fi`)

	t.Chdir(tempRepoWithRemotes(t, "git@gitlab.com:acme/tool.git"))
	comments, err := New().FetchPRReviews("main")
	if err != nil {
		t.Fatalf("FetchPRReviews: %v", err)
	}
	if len(comments) != 0 {
		t.Errorf("FetchPRReviews() = %+v for a GitLab checkout, want none", comments)
	}
}

// Two gh calls make up a review fetch — `pr view` for the top-level reviews
// and `api graphql` for the unresolved inline threads — and the agent is meant
// to receive both, general reviews first.
func TestFetchPRReviews_JoinsReviewsAndInlineThreads(t *testing.T) {
	if exec.Command("git", "--version").Run() != nil {
		t.Skip("git not available")
	}
	stubGH(t, `case "$1" in
  pr) echo '{"url":"https://github.com/acme/tool/pull/7","reviews":[{"author":{"login":"alice"},"body":"Handle the error.","state":"CHANGES_REQUESTED"}]}' ;;
  api) echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[{"isResolved":false,"comments":{"nodes":[{"author":{"login":"bob"},"body":"Extract this.","path":"pkg/x.go","line":12}]}}]}}}}}' ;;
esac`)

	t.Chdir(tempRepoWithRemotes(t, "https://github.com/acme/tool.git"))
	got, err := New().FetchPRReviews("main")
	if err != nil {
		t.Fatalf("FetchPRReviews: %v", err)
	}
	want := []ReviewComment{
		{Author: "alice", Body: "Handle the error.", State: "CHANGES_REQUESTED"},
		{Author: "bob", Body: "Extract this.", State: "COMMENTED", Path: "pkg/x.go", Line: 12},
	}
	if !commentsEqual(got, want) {
		t.Errorf("FetchPRReviews() = %+v, want %+v", got, want)
	}
}

// Regression: gh writes progress and warnings to stderr and the PR URL to
// stdout. Reading a merged stream once stored the chatter as the PR URL.
func TestCreatePR_URLComesFromStdoutAlone(t *testing.T) {
	stubGH(t, "if [ \"$1\" = \"pr\" ]; then\n"+
		"  echo 'Warning: 3 uncommitted changes' >&2\n"+
		"  echo 'https://github.com/acme/tool/pull/7'\n"+
		"fi")

	url, err := New().CreatePR("feat/add-dark-mode", "main", "", "Body.")
	if err != nil {
		t.Fatalf("CreatePR: %v", err)
	}
	if url != "https://github.com/acme/tool/pull/7" {
		t.Errorf("CreatePR() = %q, want the URL from stdout with no stderr in it", url)
	}
}

func TestFindPR_DecodesAndLowercasesState(t *testing.T) {
	stubGH(t, `if [ "$1" = "pr" ]; then`+
		` echo '{"url":"https://github.com/acme/tool/pull/9","state":"OPEN","headRefOid":"abc123","body":"words"}'; fi`)

	pr, err := New().FindPR("feat/thing", "")
	if err != nil {
		t.Fatalf("FindPR: %v", err)
	}
	if pr == nil {
		t.Fatal("FindPR() = nil for a branch with a PR")
	}
	if pr.URL != "https://github.com/acme/tool/pull/9" || pr.State != "open" ||
		pr.HeadSha != "abc123" || pr.Body != "words" {
		t.Errorf("FindPR() = %+v, want the decoded PR with a lowercased state", pr)
	}
}

func TestFindPR_NoPRIsNilNotError(t *testing.T) {
	stubGH(t, `if [ "$1" = "pr" ]; then`+
		` echo 'no pull requests found for branch "feat/thing"' >&2; exit 1; fi`)

	pr, err := New().FindPR("feat/thing", "")
	if err != nil {
		t.Fatalf("FindPR with no PR: %v, want nil — it is the answer that makes creating one right", err)
	}
	if pr != nil {
		t.Errorf("FindPR() = %+v, want nil", pr)
	}
}

func TestUpdatePR_PassesTitleAndBody(t *testing.T) {
	log := filepath.Join(t.TempDir(), "calls")
	stubGH(t, `if [ "$1" = "pr" ]; then printf '%s\n' "$@" > `+log+`; fi`)

	if err := New().UpdatePR("feat/thing", "Title", "Body."); err != nil {
		t.Fatalf("UpdatePR: %v", err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatalf("the stub was never asked to edit: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{"pr", "edit", "feat/thing", "--title", "Title", "--body", "Body."}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("gh args = %q, want %q", got, want)
	}
}

func TestGetIssue_DecodesResponse(t *testing.T) {
	stubGH(t, `if [ "$1" = "issue" ]; then`+
		` echo '{"number":42,"title":"Add dark mode","body":"Too bright.","labels":[{"name":"ui"}]}'; fi`)

	issue, err := New().GetIssue(42)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Number != 42 || issue.Title != "Add dark mode" {
		t.Errorf("GetIssue() = %+v, want issue 42 with its title", issue)
	}
	if len(issue.Labels) != 1 || issue.Labels[0] != "ui" {
		t.Errorf("GetIssue() labels = %v, want [ui]", issue.Labels)
	}
}

func TestNew(t *testing.T) {
	pm := New()
	if pm == nil {
		t.Fatal("New() returned nil")
	}
}

func TestIsGHInstalled(t *testing.T) {
	pm := New()
	// Just verify it doesn't panic and returns a consistent result.
	got := pm.IsInstalled()
	want := isGHAvailable()
	if got != want {
		t.Errorf("IsInstalled() = %v, want %v", got, want)
	}
}

func TestGetPRStatus_GHNotInstalled(t *testing.T) {
	if isGHAvailable() {
		t.Skip("gh is installed; skipping test for missing gh")
	}
	pm := New()
	url, err := pm.GetPRStatus("some-branch")
	if err != nil {
		t.Fatalf("GetPRStatus() expected nil error when gh not installed, got: %v", err)
	}
	if url != "" {
		t.Errorf("GetPRStatus() expected empty string when gh not installed, got %q", url)
	}
}

func TestGetFullPRStatus_GHNotInstalled(t *testing.T) {
	if isGHAvailable() {
		t.Skip("gh is installed; skipping test for missing gh")
	}
	pm := New()
	url, state, err := pm.GetFullPRStatus("some-branch")
	if err != nil {
		t.Fatalf("GetFullPRStatus() expected nil error when gh not installed, got: %v", err)
	}
	if url != "" || state != "" {
		t.Errorf("GetFullPRStatus() = (%q, %q), want (\"\", \"\") when gh not installed", url, state)
	}
}

func TestCreatePR_GHNotInstalled(t *testing.T) {
	if isGHAvailable() {
		t.Skip("gh is installed; skipping test for missing gh")
	}
	pm := New()
	_, err := pm.CreatePR("branch", "main", "title", "body")
	if err == nil {
		t.Fatal("CreatePR() expected error when gh not installed, got nil")
	}
	if !strings.Contains(err.Error(), "gh CLI is not installed") {
		t.Errorf("CreatePR() error = %q, expected message about gh CLI not installed", err.Error())
	}
}

// ---- IssueBranchName tests ----

func TestIssueBranchName_Basic(t *testing.T) {
	got := IssueBranchName(42, "Add dark mode")
	want := "issue-42-add-dark-mode"
	if got != want {
		t.Errorf("IssueBranchName(42, %q) = %q, want %q", "Add dark mode", got, want)
	}
}

func TestIssueBranchName_SpecialChars(t *testing.T) {
	got := IssueBranchName(7, "Fix: login bug (regression!)")
	want := "issue-7-fix-login-bug-regression"
	if got != want {
		t.Errorf("IssueBranchName = %q, want %q", got, want)
	}
}

func TestIssueBranchName_LongTitle(t *testing.T) {
	title := "This is a very long issue title that exceeds the maximum allowed length for branch names"
	got := IssueBranchName(1, title)
	// Must start with "issue-1-" and be at most 8+40 chars
	if !strings.HasPrefix(got, "issue-1-") {
		t.Errorf("IssueBranchName prefix wrong: %q", got)
	}
	slug := strings.TrimPrefix(got, "issue-1-")
	if len(slug) > 40 {
		t.Errorf("slug too long: %d chars: %q", len(slug), slug)
	}
	if strings.HasSuffix(slug, "-") {
		t.Errorf("slug has trailing dash: %q", slug)
	}
}

func TestIssueBranchName_Uppercase(t *testing.T) {
	got := IssueBranchName(10, "UPPERCASE TITLE")
	want := "issue-10-uppercase-title"
	if got != want {
		t.Errorf("IssueBranchName = %q, want %q", got, want)
	}
}

func TestIssueBranchName_EmptyTitle(t *testing.T) {
	got := IssueBranchName(5, "")
	// When title is empty, slug is also empty → just "issue-5-"
	if !strings.HasPrefix(got, "issue-5") {
		t.Errorf("IssueBranchName with empty title = %q, want prefix 'issue-5'", got)
	}
}

// ---- GetIssue when gh is not installed ----

func TestGetIssue_GHNotInstalled(t *testing.T) {
	if isGHAvailable() {
		t.Skip("gh is installed; skipping test for missing gh")
	}
	pm := New()
	_, err := pm.GetIssue(1)
	if err == nil {
		t.Fatal("GetIssue() expected error when gh not installed, got nil")
	}
	if !strings.Contains(err.Error(), "gh CLI is not installed") {
		t.Errorf("GetIssue() error = %q, expected message about gh CLI", err.Error())
	}
}

// ---- GetBranchAndPRStatus tests ----

func TestGetBranchAndPRStatus_LSRemoteError(t *testing.T) {
	if exec.Command("git", "--version").Run() != nil {
		t.Skip("git not available")
	}
	skipIfGHUnauthenticated(t)
	dir := t.TempDir()
	initCmd := exec.Command("git", "init")
	initCmd.Dir = dir
	if err := initCmd.Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	pm := New()
	// wasPushed=true: without the fix this would incorrectly set RemoteDeleted=true
	status, err := pm.GetBranchAndPRStatus("main", dir, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !status.RemoteCheckFailed {
		t.Error("expected RemoteCheckFailed=true when git ls-remote fails")
	}
	if status.RemoteDeleted {
		t.Error("expected RemoteDeleted=false when remote check failed")
	}
	if status.Pushed {
		t.Error("expected Pushed=false when remote check failed")
	}
}

// ---- parsePRURL tests ----

func TestParsePRURL_Valid(t *testing.T) {
	tests := []struct {
		url        string
		wantOwner  string
		wantRepo   string
		wantNumber int
	}{
		{
			url:        "https://github.com/acme/myrepo/pull/42",
			wantOwner:  "acme",
			wantRepo:   "myrepo",
			wantNumber: 42,
		},
		{
			url:        "https://github.com/org-name/repo.with.dots/pull/1",
			wantOwner:  "org-name",
			wantRepo:   "repo.with.dots",
			wantNumber: 1,
		},
		{
			// URL with trailing path (e.g. #issuecomment anchor)
			url:        "https://github.com/owner/repo/pull/999/files",
			wantOwner:  "owner",
			wantRepo:   "repo",
			wantNumber: 999,
		},
	}
	for _, tt := range tests {
		owner, repo, number, err := parsePRURL(tt.url)
		if err != nil {
			t.Errorf("parsePRURL(%q) unexpected error: %v", tt.url, err)
			continue
		}
		if owner != tt.wantOwner {
			t.Errorf("parsePRURL(%q) owner = %q, want %q", tt.url, owner, tt.wantOwner)
		}
		if repo != tt.wantRepo {
			t.Errorf("parsePRURL(%q) repo = %q, want %q", tt.url, repo, tt.wantRepo)
		}
		if number != tt.wantNumber {
			t.Errorf("parsePRURL(%q) number = %d, want %d", tt.url, number, tt.wantNumber)
		}
	}
}

func TestParsePRURL_Invalid(t *testing.T) {
	invalid := []string{
		"",
		"not-a-url",
		"https://github.com/owner/repo/issues/42",
		"https://gitlab.com/owner/repo/merge_requests/42",
		"https://github.com/owner/pull/42", // missing repo segment
	}
	for _, url := range invalid {
		_, _, _, err := parsePRURL(url)
		if err == nil {
			t.Errorf("parsePRURL(%q) expected error, got nil", url)
		}
	}
}

// ---- FormatReviewsPrompt tests ----

func TestFormatReviewsPrompt_Empty(t *testing.T) {
	got := FormatReviewsPrompt(nil)
	if got != "" {
		t.Errorf("FormatReviewsPrompt(nil) = %q, want empty string", got)
	}
	got = FormatReviewsPrompt([]ReviewComment{})
	if got != "" {
		t.Errorf("FormatReviewsPrompt([]) = %q, want empty string", got)
	}
}

func TestFormatReviewsPrompt_SingleGeneralReview(t *testing.T) {
	comments := []ReviewComment{
		{Author: "alice", Body: "Please add error handling.", State: "CHANGES_REQUESTED"},
	}
	got := FormatReviewsPrompt(comments)
	if !strings.Contains(got, "@alice") {
		t.Errorf("prompt missing author: %s", got)
	}
	if !strings.Contains(got, "Please add error handling.") {
		t.Errorf("prompt missing body: %s", got)
	}
	if !strings.Contains(got, "CHANGES_REQUESTED") {
		t.Errorf("prompt missing state: %s", got)
	}
	if !strings.Contains(got, "Please address all of these review comments.") {
		t.Errorf("prompt missing closing instruction: %s", got)
	}
	if !strings.Contains(got, "1 PR review comment(s)") {
		t.Errorf("prompt missing count: %s", got)
	}
}

func TestFormatReviewsPrompt_InlineComment(t *testing.T) {
	comments := []ReviewComment{
		{Author: "bob", Body: "This is too complex.", State: "COMMENTED", Path: "pkg/foo/bar.go", Line: 42},
	}
	got := FormatReviewsPrompt(comments)
	if !strings.Contains(got, "pkg/foo/bar.go:42") {
		t.Errorf("prompt missing file:line reference: %s", got)
	}
	if !strings.Contains(got, "@bob") {
		t.Errorf("prompt missing author: %s", got)
	}
	if strings.Contains(got, "COMMENTED") {
		t.Errorf("prompt should not show COMMENTED state, got: %s", got)
	}
}

func TestFormatReviewsPrompt_InlineComment_NoLine(t *testing.T) {
	comments := []ReviewComment{
		{Author: "carol", Body: "Rename this function.", State: "COMMENTED", Path: "main.go", Line: 0},
	}
	got := FormatReviewsPrompt(comments)
	if !strings.Contains(got, "main.go") {
		t.Errorf("prompt missing path: %s", got)
	}
	// Should show path but no ":0"
	if strings.Contains(got, "main.go:0") {
		t.Errorf("prompt should not show ':0' for zero line: %s", got)
	}
}

func TestFormatReviewsPrompt_Multiple(t *testing.T) {
	comments := []ReviewComment{
		{Author: "alice", Body: "Fix typo.", State: "CHANGES_REQUESTED"},
		{Author: "bob", Body: "Extract method.", State: "COMMENTED", Path: "pkg/x.go", Line: 10},
		{Author: "carol", Body: "Add tests.", State: "CHANGES_REQUESTED"},
	}
	got := FormatReviewsPrompt(comments)
	if !strings.Contains(got, "3 PR review comment(s)") {
		t.Errorf("prompt missing count: %s", got)
	}
	for _, wantBody := range []string{"Fix typo.", "Extract method.", "Add tests."} {
		if !strings.Contains(got, wantBody) {
			t.Errorf("prompt missing body %q: %s", wantBody, got)
		}
	}
}

// ---- FetchPRReviews when gh is not installed ----

func TestFetchPRReviews_GHNotInstalled(t *testing.T) {
	if isGHAvailable() {
		t.Skip("gh is installed; skipping test for missing gh")
	}
	pm := New()
	comments, err := pm.FetchPRReviews("some-branch")
	if err == nil {
		t.Fatal("FetchPRReviews() expected error when gh not installed, got nil")
	}
	if !strings.Contains(err.Error(), "gh CLI is not installed") {
		t.Errorf("FetchPRReviews() error = %q, want 'gh CLI is not installed'", err.Error())
	}
	if comments != nil {
		t.Errorf("FetchPRReviews() comments = %v, want nil on error", comments)
	}
}

func TestFetchPRReviews_NoBranchPR(t *testing.T) {
	if !isGHAvailable() {
		t.Skip("gh not available, skipping integration test")
	}
	skipIfGHUnauthenticated(t)
	pm := New()
	// A branch name that certainly has no PR.
	comments, err := pm.FetchPRReviews("this-branch-certainly-has-no-pr-xyz-99999")
	if err != nil {
		t.Fatalf("FetchPRReviews() unexpected error: %v", err)
	}
	// No PR → nil or empty slice, never an error.
	if len(comments) != 0 {
		t.Errorf("FetchPRReviews() expected 0 comments for non-existent branch PR, got %d", len(comments))
	}
}

// ---- Integration tests (require gh CLI) ----

func TestGetPRStatus_NoPRForBranch(t *testing.T) {
	if !isGHAvailable() {
		t.Skip("gh not available, skipping integration test")
	}
	skipIfGHUnauthenticated(t)
	pm := New()
	// A branch name unlikely to have a PR; "no PR" is a normal, non-error result.
	url, err := pm.GetPRStatus("this-branch-certainly-has-no-pr-xyz-12345")
	if err != nil {
		t.Fatalf("GetPRStatus() unexpected error: %v", err)
	}
	// No PR should yield an empty URL (gh returns non-zero exit for missing PRs).
	_ = url // empty or not depends on the repo; we just verify no panic/crash
}

func TestGetFullPRStatus_NoPRForBranch(t *testing.T) {
	if !isGHAvailable() {
		t.Skip("gh not available, skipping integration test")
	}
	skipIfGHUnauthenticated(t)
	pm := New()
	url, state, err := pm.GetFullPRStatus("this-branch-certainly-has-no-pr-xyz-12345")
	if err != nil {
		t.Fatalf("GetFullPRStatus() unexpected error: %v", err)
	}
	// Non-existent branch should yield empty url and state.
	if url != "" || state != "" {
		t.Logf("GetFullPRStatus() = (%q, %q) — may indicate an unexpected PR exists", url, state)
	}
}

// Regression: legacy commit statuses (StatusContext with only a `state`
// field), WAITING check runs, and ACTION_REQUIRED conclusions all used to
// decode to empty strings and fall through to a false "success".
func TestDeriveCIStatus(t *testing.T) {
	tests := []struct {
		name   string
		checks []rollupCheck
		want   string
	}{
		{"no checks", nil, ""},
		{"all check runs pass", []rollupCheck{{Status: "COMPLETED", Conclusion: "SUCCESS"}}, "success"},
		{"check run failed", []rollupCheck{{Status: "COMPLETED", Conclusion: "FAILURE"}}, "failure"},
		{"check run in progress", []rollupCheck{{Status: "IN_PROGRESS"}}, "pending"},
		{"check run waiting for approval", []rollupCheck{{Status: "WAITING"}}, "pending"},
		{"action required", []rollupCheck{{Status: "COMPLETED", Conclusion: "ACTION_REQUIRED"}}, "pending"},
		{"legacy status failing", []rollupCheck{{State: "FAILURE"}}, "failure"},
		{"legacy status error", []rollupCheck{{State: "ERROR"}}, "failure"},
		{"legacy status pending", []rollupCheck{{State: "PENDING"}}, "pending"},
		{"legacy status success", []rollupCheck{{State: "SUCCESS"}}, "success"},
		{"mixed: green run + failing legacy status", []rollupCheck{
			{Status: "COMPLETED", Conclusion: "SUCCESS"},
			{State: "FAILURE"},
		}, "failure"},
		{"mixed: green run + pending legacy status", []rollupCheck{
			{Status: "COMPLETED", Conclusion: "SUCCESS"},
			{State: "PENDING"},
		}, "pending"},
		{"pending then failure still reports failure", []rollupCheck{
			{Status: "IN_PROGRESS"},
			{Status: "COMPLETED", Conclusion: "TIMED_OUT"},
		}, "failure"},
		{"skipped and neutral count as success", []rollupCheck{
			{Status: "COMPLETED", Conclusion: "SKIPPED"},
			{Status: "COMPLETED", Conclusion: "NEUTRAL"},
		}, "success"},
		{"unknown shape is pending, never success", []rollupCheck{{}}, "pending"},
	}
	for _, tt := range tests {
		if got := deriveCIStatus(tt.checks); got != tt.want {
			t.Errorf("%s: deriveCIStatus() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// ---- decoders ----
//
// Every gh call in this package is a gate, a subprocess, and a fold. The folds
// are the part that can be wrong in an interesting way, so they are pure
// functions over bytes and tested from fixtures — no gh, no network, and
// nothing here that CI could skip its way past.

// commentsEqual compares two comment slices by value. A nil slice and an empty
// one are equal on purpose: a decoder that filters every comment out may
// return either, and callers only ever ask for the length.
func commentsEqual(a, b []ReviewComment) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Regression: an approval, a dismissal and a bare "LGTM" all used to be
// indistinguishable from real work once they reached the agent's prompt.
func TestDecodePRReviews(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		wantURL  string
		wantComs []ReviewComment
	}{
		{"no reviews", `{"url":"https://github.com/acme/tool/pull/7","reviews":[]}`,
			"https://github.com/acme/tool/pull/7", nil},
		{"null reviews", `{"url":"https://github.com/acme/tool/pull/7"}`,
			"https://github.com/acme/tool/pull/7", nil},
		{"changes requested with a body is kept", `{"url":"u","reviews":[
			{"author":{"login":"alice"},"body":"Handle the error.","state":"CHANGES_REQUESTED"}]}`,
			"u", []ReviewComment{{Author: "alice", Body: "Handle the error.", State: "CHANGES_REQUESTED"}}},
		{"approval is dropped", `{"url":"u","reviews":[
			{"author":{"login":"bob"},"body":"Ship it.","state":"APPROVED"}]}`, "u", nil},
		{"comment-only review is dropped", `{"url":"u","reviews":[
			{"author":{"login":"bob"},"body":"Interesting.","state":"COMMENTED"}]}`, "u", nil},
		{"dismissed review is dropped", `{"url":"u","reviews":[
			{"author":{"login":"bob"},"body":"Never mind.","state":"DISMISSED"}]}`, "u", nil},
		{"empty body is dropped", `{"url":"u","reviews":[
			{"author":{"login":"bob"},"body":"   \n","state":"CHANGES_REQUESTED"}]}`, "u", nil},
		{"body is trimmed", `{"url":"u","reviews":[
			{"author":{"login":"bob"},"body":"\n  Rename it.  \n","state":"CHANGES_REQUESTED"}]}`,
			"u", []ReviewComment{{Author: "bob", Body: "Rename it.", State: "CHANGES_REQUESTED"}}},
		{"order is preserved across a dropped review", `{"url":"u","reviews":[
			{"author":{"login":"alice"},"body":"First.","state":"CHANGES_REQUESTED"},
			{"author":{"login":"bob"},"body":"LGTM","state":"APPROVED"},
			{"author":{"login":"carol"},"body":"Second.","state":"CHANGES_REQUESTED"}]}`, "u",
			[]ReviewComment{
				{Author: "alice", Body: "First.", State: "CHANGES_REQUESTED"},
				{Author: "carol", Body: "Second.", State: "CHANGES_REQUESTED"},
			}},
		{"a PR with no URL still yields its reviews", `{"reviews":[
			{"author":{"login":"alice"},"body":"Fix.","state":"CHANGES_REQUESTED"}]}`, "",
			[]ReviewComment{{Author: "alice", Body: "Fix.", State: "CHANGES_REQUESTED"}}},
	}
	for _, tt := range tests {
		url, comments, err := decodePRReviews([]byte(tt.body))
		if err != nil {
			t.Errorf("%s: decodePRReviews() unexpected error: %v", tt.name, err)
			continue
		}
		if url != tt.wantURL {
			t.Errorf("%s: decodePRReviews() url = %q, want %q", tt.name, url, tt.wantURL)
		}
		if !commentsEqual(comments, tt.wantComs) {
			t.Errorf("%s: decodePRReviews() comments = %+v, want %+v", tt.name, comments, tt.wantComs)
		}
	}
}

func TestDecodePRReviews_MalformedJSON(t *testing.T) {
	_, _, err := decodePRReviews([]byte(`{"url":`))
	if err == nil {
		t.Fatal("decodePRReviews() on truncated JSON = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "parse pr reviews") {
		t.Errorf("decodePRReviews() error = %q, want it to name what failed to parse", err.Error())
	}
}

// Regression: a comment on a line that has since moved reports line: null,
// which decodes to zero and used to print the file with no line at all.
func TestDecodeUnresolvedThreads(t *testing.T) {
	// threadsJSON wraps thread nodes in the six levels of nesting the GraphQL
	// response actually arrives in.
	threadsJSON := func(nodes string) string {
		return `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[` + nodes + `]}}}}}`
	}
	comment := `{"author":{"login":"alice"},"body":"Extract this.","path":"pkg/x.go","line":12,"originalLine":9}`

	tests := []struct {
		name string
		body string
		want []ReviewComment
	}{
		{"no threads", threadsJSON(""), nil},
		{"unresolved thread is kept", threadsJSON(`{"isResolved":false,"comments":{"nodes":[` + comment + `]}}`),
			[]ReviewComment{{Author: "alice", Body: "Extract this.", State: "COMMENTED", Path: "pkg/x.go", Line: 12}}},
		{"resolved thread is dropped", threadsJSON(`{"isResolved":true,"comments":{"nodes":[` + comment + `]}}`), nil},
		{"thread with no comments is dropped", threadsJSON(`{"isResolved":false,"comments":{"nodes":[]}}`), nil},
		{"empty comment body is dropped", threadsJSON(
			`{"isResolved":false,"comments":{"nodes":[{"author":{"login":"bob"},"body":"  ","path":"a.go","line":1}]}}`), nil},
		{"moved comment falls back to originalLine", threadsJSON(
			`{"isResolved":false,"comments":{"nodes":[{"author":{"login":"bob"},"body":"Moved.","path":"a.go","line":null,"originalLine":44}]}}`),
			[]ReviewComment{{Author: "bob", Body: "Moved.", State: "COMMENTED", Path: "a.go", Line: 44}}},
		{"a comment on neither line survives with no line", threadsJSON(
			`{"isResolved":false,"comments":{"nodes":[{"author":{"login":"bob"},"body":"General.","path":"a.go"}]}}`),
			[]ReviewComment{{Author: "bob", Body: "General.", State: "COMMENTED", Path: "a.go", Line: 0}}},
		{"only the first comment of a thread is taken", threadsJSON(
			`{"isResolved":false,"comments":{"nodes":[` + comment +
				`,{"author":{"login":"bob"},"body":"Agreed.","path":"pkg/x.go","line":12}]}}`),
			[]ReviewComment{{Author: "alice", Body: "Extract this.", State: "COMMENTED", Path: "pkg/x.go", Line: 12}}},
		{"resolved threads do not disturb the ones after them", threadsJSON(
			`{"isResolved":true,"comments":{"nodes":[` + comment + `]}},` +
				`{"isResolved":false,"comments":{"nodes":[{"author":{"login":"carol"},"body":"Last.","path":"b.go","line":2}]}}`),
			[]ReviewComment{{Author: "carol", Body: "Last.", State: "COMMENTED", Path: "b.go", Line: 2}}},
	}
	for _, tt := range tests {
		got, err := decodeUnresolvedThreads([]byte(tt.body))
		if err != nil {
			t.Errorf("%s: decodeUnresolvedThreads() unexpected error: %v", tt.name, err)
			continue
		}
		if !commentsEqual(got, tt.want) {
			t.Errorf("%s: decodeUnresolvedThreads() = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestDecodeUnresolvedThreads_MalformedJSON(t *testing.T) {
	_, err := decodeUnresolvedThreads([]byte("not json at all"))
	if err == nil {
		t.Fatal("decodeUnresolvedThreads() on garbage = nil error, want an error")
	}
	if !strings.Contains(err.Error(), "graphql") {
		t.Errorf("decodeUnresolvedThreads() error = %q, want it to name the graphql response", err.Error())
	}
}

func TestDecodeIssue(t *testing.T) {
	issue, err := decodeIssue([]byte(
		`{"number":42,"title":"Add dark mode","body":"It is too bright.","labels":[{"name":"enhancement"},{"name":"ui"}]}`))
	if err != nil {
		t.Fatalf("decodeIssue() unexpected error: %v", err)
	}
	if issue.Number != 42 || issue.Title != "Add dark mode" || issue.Body != "It is too bright." {
		t.Errorf("decodeIssue() = %+v, want issue 42 with its title and body", issue)
	}
	want := []string{"enhancement", "ui"}
	if len(issue.Labels) != len(want) {
		t.Fatalf("decodeIssue() labels = %v, want %v", issue.Labels, want)
	}
	for i := range want {
		if issue.Labels[i] != want[i] {
			t.Errorf("decodeIssue() label %d = %q, want %q", i, issue.Labels[i], want[i])
		}
	}
}

// An unlabelled issue must decode to an empty label slice, not a nil one:
// callers range over it and the branch-naming path formats it.
func TestDecodeIssue_NoLabels(t *testing.T) {
	issue, err := decodeIssue([]byte(`{"number":1,"title":"T","body":"","labels":[]}`))
	if err != nil {
		t.Fatalf("decodeIssue() unexpected error: %v", err)
	}
	if issue.Labels == nil {
		t.Error("decodeIssue() labels = nil, want an empty slice")
	}
	if len(issue.Labels) != 0 {
		t.Errorf("decodeIssue() labels = %v, want empty", issue.Labels)
	}
}

func TestDecodeIssue_MalformedJSON(t *testing.T) {
	if _, err := decodeIssue([]byte(`{"number":"forty-two"}`)); err == nil {
		t.Fatal("decodeIssue() on a string where a number belongs = nil error, want an error")
	}
}

// Regression: a PR with no checks configured answers `"statusCheckRollup": null`,
// which must read as "no checks" rather than as a failure.
func TestDecodeCIStatus(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"null rollup", `{"statusCheckRollup":null}`, ""},
		{"absent rollup", `{}`, ""},
		{"empty rollup", `{"statusCheckRollup":[]}`, ""},
		{"one green check run", `{"statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"}]}`, "success"},
		{"one red check run", `{"statusCheckRollup":[{"status":"COMPLETED","conclusion":"FAILURE"}]}`, "failure"},
		{"legacy commit status", `{"statusCheckRollup":[{"state":"PENDING"}]}`, "pending"},
		{"mixed check run and commit status", `{"statusCheckRollup":[
			{"status":"COMPLETED","conclusion":"SUCCESS"},{"state":"FAILURE"}]}`, "failure"},
	}
	for _, tt := range tests {
		got, err := decodeCIStatus([]byte(tt.body))
		if err != nil {
			t.Errorf("%s: decodeCIStatus() unexpected error: %v", tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s: decodeCIStatus() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestDecodeCIStatus_MalformedJSON(t *testing.T) {
	if _, err := decodeCIStatus([]byte(`{"statusCheckRollup":{}}`)); err == nil {
		t.Fatal("decodeCIStatus() on an object where an array belongs = nil error, want an error")
	}
}

// Regression: mergeable is a tri-state, and GitHub answers UNKNOWN for the
// first seconds of a PR's life. Only CONFLICTING may raise the warning.
func TestDecodePRSummary(t *testing.T) {
	tests := []struct {
		name string
		body string
		want prSummary
	}{
		{"open PR, clean, green", `{"url":"https://github.com/acme/tool/pull/7","state":"OPEN",
			"mergeable":"MERGEABLE","statusCheckRollup":[{"status":"COMPLETED","conclusion":"SUCCESS"}]}`,
			prSummary{URL: "https://github.com/acme/tool/pull/7", State: "open", CIStatus: "success"}},
		{"conflicting", `{"url":"u","state":"OPEN","mergeable":"CONFLICTING"}`,
			prSummary{URL: "u", State: "open", MergeConflicts: true}},
		{"mergeability not computed yet", `{"url":"u","state":"OPEN","mergeable":"UNKNOWN"}`,
			prSummary{URL: "u", State: "open"}},
		{"merged", `{"url":"u","state":"MERGED","mergeable":"UNKNOWN"}`,
			prSummary{URL: "u", State: "merged"}},
		{"closed", `{"url":"u","state":"CLOSED"}`, prSummary{URL: "u", State: "closed"}},
		{"no PR at all", `{}`, prSummary{}},
	}
	for _, tt := range tests {
		got, err := decodePRSummary([]byte(tt.body))
		if err != nil {
			t.Errorf("%s: decodePRSummary() unexpected error: %v", tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("%s: decodePRSummary() = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestDecodePRSummary_MalformedJSON(t *testing.T) {
	if _, err := decodePRSummary([]byte(`{"state":`)); err == nil {
		t.Fatal("decodePRSummary() on truncated JSON = nil error, want an error")
	}
}

func TestSplitURLState(t *testing.T) {
	url, state, err := splitURLState([]byte("https://github.com/acme/tool/pull/7\tMERGED\n"))
	if err != nil {
		t.Fatalf("splitURLState() unexpected error: %v", err)
	}
	if url != "https://github.com/acme/tool/pull/7" {
		t.Errorf("splitURLState() url = %q", url)
	}
	if state != "merged" {
		t.Errorf("splitURLState() state = %q, want %q", state, "merged")
	}
	if _, _, err := splitURLState([]byte("https://github.com/acme/tool/pull/7")); err == nil {
		t.Error("splitURLState() on output with no tab = nil error, want an error")
	}
	if _, _, err := splitURLState(nil); err == nil {
		t.Error("splitURLState() on empty output = nil error, want an error")
	}
}

// Regression: gh reads --title and --body from a terminal when they are
// missing, and there is no terminal behind this call — an empty body used to
// fail the whole PR with "must provide --title and --body".
func TestCreatePRArgs(t *testing.T) {
	got := createPRArgs("feat/add-dark-mode", "main", "", "")
	want := []string{"pr", "create", "--base", "main", "--head", "feat/add-dark-mode",
		"--title", "Add dark mode", "--body", ""}
	if len(got) != len(want) {
		t.Fatalf("createPRArgs() = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("createPRArgs() = %q, want %q", got, want)
		}
	}

	got = createPRArgs("feat/x", "develop", "A real title", "A real body")
	want = []string{"pr", "create", "--base", "develop", "--head", "feat/x",
		"--title", "A real title", "--body", "A real body"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("createPRArgs() with an explicit title = %q, want %q", got, want)
		}
	}
}

func TestParsePRURL_EnterpriseHost(t *testing.T) {
	owner, repo, number, err := parsePRURL("https://github.mycorp.com/acme/tool/pull/7")
	if err != nil {
		t.Fatalf("parsePRURL() error: %v", err)
	}
	if owner != "acme" || repo != "tool" || number != 7 {
		t.Errorf("parsePRURL() = %s/%s#%d, want acme/tool#7", owner, repo, number)
	}
}
