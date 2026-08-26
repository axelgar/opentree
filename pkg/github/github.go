package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/axelgar/opentree/pkg/gitutil"
)

// ghRun executes gh with stdout and stderr kept separate, so stderr chatter
// (e.g. a globally exported GH_DEBUG) can never corrupt parsed output or a
// returned PR URL. err is the raw process error; callers decide how to
// combine it with stderr.
func ghRun(dir string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.Command("gh", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.Bytes(), errBuf.Bytes(), err
}

// hasGitHubRemote reports whether the repository in dir has a remote gh could
// possibly serve. An empty dir means the process working directory, matching
// ghRun.
//
// Asked a `pr view` in a GitLab or Bitbucket checkout, gh contacts nobody and
// prints "none of the git remotes configured for this repository point to a
// known GitHub host" — but only after walking its own config and host
// resolution, and the dashboard's thirty-second status poll then pays that
// walk on every tick for the life of the session. `git remote -v` is local and
// costs microseconds, so the question is settled here, once, before gh is
// asked anything.
//
// The answer is deliberately not memoised. A repository grows an origin the
// moment its author runs `git remote add`, and a cached "not GitHub" would
// leave the PR column blank until the next restart with nothing on screen to
// explain why.
func hasGitHubRemote(dir string) bool {
	cmd := exec.Command("git", "remote", "-v")
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		// Not a git repository, or no git at all. That is not a question about
		// remotes, so it is not this gate's to answer — let gh speak.
		return true
	}
	return anyGitHubRemote(out)
}

// anyGitHubRemote reports whether a `git remote -v` listing names a host gh
// could serve. Every remote is considered, not just origin: gh resolves a PR
// through whichever remote points at GitHub, and a fork checkout routinely
// keeps origin on a mirror and upstream on github.com.
func anyGitHubRemote(listing []byte) bool {
	for _, line := range strings.Split(string(listing), "\n") {
		// "origin\tgit@github.com:acme/tool.git (fetch)"
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if hostIsGitHub(remoteHost(fields[1])) {
			return true
		}
	}
	return false
}

// nonGitHubHosts are forges gh can never speak to. This is a denylist rather
// than an allowlist of GitHub hosts because GitHub Enterprise lives at
// whatever hostname its owner chose — github.acme.io, git.acme.io, code.acme.io
// — and nothing in the URL tells it apart from a self-hosted GitLab at the
// same address. An unrecognised host is therefore treated as GitHub: being
// wrong that way costs one poll, while being wrong the other way would swallow
// the "gh auth login --hostname" error an Enterprise user has to see.
var nonGitHubHosts = []string{
	"gitlab.com",
	"bitbucket.org",
	"codeberg.org",
	"gitea.com",
	"gitee.com",
	"sr.ht",
	"dev.azure.com",
	"visualstudio.com",
	"sourceforge.net",
	"launchpad.net",
	"git.kernel.org",
	"savannah.gnu.org",
}

// hostIsGitHub reports whether gh could serve a remote on the given host.
func hostIsGitHub(host string) bool {
	if host == "" {
		// No remote, or a bare filesystem path. Either way there is nothing
		// for gh to resolve a PR against.
		return false
	}
	if host == "github.com" || strings.HasSuffix(host, ".github.com") {
		return true
	}
	for _, known := range nonGitHubHosts {
		if host == known || strings.HasSuffix(host, "."+known) {
			return false
		}
	}
	return true
}

// remoteHost extracts the lowercased hostname from a git remote URL, covering
// the three spellings git accepts: a scheme URL (https://host/o/r.git), an
// scp-like address (git@host:o/r.git), and a plain filesystem path, which has
// no host and yields "".
func remoteHost(remoteURL string) string {
	s := strings.TrimSpace(remoteURL)
	if s == "" {
		return ""
	}
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+len("://"):]
		if j := strings.Index(s, "/"); j >= 0 {
			s = s[:j]
		}
	} else {
		// scp-like syntax: everything up to the first colon is [user@]host.
		// Without a colon this is a local path, which no forge serves.
		i := strings.Index(s, ":")
		if i < 0 {
			return ""
		}
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:] // userinfo
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i] // port
	}
	return strings.ToLower(s)
}

// ReviewComment represents a single review comment on a PR.
// General reviews have an empty Path and zero Line.
// Inline (code) comments have Path and Line set.
type ReviewComment struct {
	Author string
	Body   string
	State  string // "CHANGES_REQUESTED", "APPROVED", "COMMENTED"
	Path   string // file path for inline comments; empty for general reviews
	Line   int    // line number for inline comments; 0 for general reviews
}

// prURLRe matches GitHub PR URLs (github.com or GitHub Enterprise hosts)
// and captures owner, repo, and number.
var prURLRe = regexp.MustCompile(`https?://[^/]+/([^/]+)/([^/]+)/pull/(\d+)`)

// parsePRURL extracts owner, repo, and PR number from a GitHub PR URL.
func parsePRURL(prURL string) (owner, repo string, number int, err error) {
	m := prURLRe.FindStringSubmatch(prURL)
	if m == nil {
		return "", "", 0, fmt.Errorf("invalid PR URL: %s", prURL)
	}
	number, err = strconv.Atoi(m[3])
	return m[1], m[2], number, err
}

// FetchPRReviews returns only actionable, unresolved review comments for the PR
// associated with the given branch. Returns an empty slice if no PR exists.
//
// General reviews: only CHANGES_REQUESTED reviews with a non-empty body are
// included. APPROVED, DISMISSED, and COMMENTED-only reviews are skipped.
//
// Inline code comments: only comments belonging to unresolved review threads
// are included, determined via the GitHub GraphQL API.
func (pm *PRManager) FetchPRReviews(branch string) ([]ReviewComment, error) {
	if !pm.IsInstalled() {
		return nil, fmt.Errorf("gh CLI is not installed. Install it from https://cli.github.com/")
	}
	if !hasGitHubRemote("") {
		// A repo with no GitHub remote has no PR to review, which is the same
		// normal, silent condition prViewError already recognises further down.
		return nil, nil
	}

	// Fetch top-level reviews and PR URL in one call.
	output, stderr, err := ghRun("", "pr", "view", branch, "--json", "url,reviews")
	if err != nil {
		if pErr := prViewError(stderr, err); pErr != nil {
			return nil, pErr
		}
		return nil, nil // branch has no PR
	}

	prURL, comments, err := decodePRReviews(output)
	if err != nil {
		return nil, err
	}

	// Fetch unresolved inline review threads via GraphQL.
	if prURL != "" {
		owner, repo, prNumber, parseErr := parsePRURL(prURL)
		if parseErr == nil {
			inlineComments, err := pm.fetchUnresolvedThreadComments(owner, repo, prNumber)
			if err != nil {
				return comments, fmt.Errorf("failed to fetch inline review threads: %w", err)
			}
			comments = append(comments, inlineComments...)
		}
	}

	return comments, nil
}

// decodePRReviews folds a `gh pr view --json url,reviews` payload into the PR's
// URL and the reviews an agent can actually act on. Only CHANGES_REQUESTED
// reviews carrying a body survive: APPROVED, DISMISSED, COMMENTED and PENDING
// ask for nothing, and an empty body asks for nothing either — handing either
// to an agent spends a turn to be told there was no work in it.
func decodePRReviews(data []byte) (prURL string, comments []ReviewComment, err error) {
	var raw struct {
		URL     string `json:"url"`
		Reviews []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			Body  string `json:"body"`
			State string `json:"state"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", nil, fmt.Errorf("failed to parse pr reviews: %w", err)
	}
	for _, r := range raw.Reviews {
		if r.State != "CHANGES_REQUESTED" {
			continue
		}
		body := strings.TrimSpace(r.Body)
		if body == "" {
			continue
		}
		comments = append(comments, ReviewComment{
			Author: r.Author.Login,
			Body:   body,
			State:  r.State,
		})
	}
	return raw.URL, comments, nil
}

// graphqlUnresolvedThreadsQuery queries for unresolved PR review threads and
// returns the first comment of each unresolved thread.
const graphqlUnresolvedThreadsQuery = `
query($owner: String!, $repo: String!, $number: Int!) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $number) {
      reviewThreads(first: 100) {
        nodes {
          isResolved
          comments(first: 1) {
            nodes {
              author { login }
              body
              path
              line
              originalLine
            }
          }
        }
      }
    }
  }
}`

// fetchUnresolvedThreadComments returns inline comments from unresolved review
// threads using the GitHub GraphQL API.
func (pm *PRManager) fetchUnresolvedThreadComments(owner, repo string, prNumber int) ([]ReviewComment, error) {
	out, stderr, err := ghRun("", "api", "graphql",
		"-f", fmt.Sprintf("query=%s", graphqlUnresolvedThreadsQuery),
		"-f", fmt.Sprintf("owner=%s", owner),
		"-f", fmt.Sprintf("repo=%s", repo),
		"-F", fmt.Sprintf("number=%d", prNumber),
	)
	if err != nil {
		return nil, fmt.Errorf("graphql query failed: %w\n%s", err, strings.TrimSpace(string(stderr)))
	}
	return decodeUnresolvedThreads(out)
}

// decodeUnresolvedThreads folds the GraphQL reviewThreads payload into inline
// comments. A resolved thread has already been dealt with, and a thread whose
// first comment is empty says nothing, so both are dropped rather than handed
// to an agent as work.
//
// A comment on a line that has since moved reports line: null, which decodes to
// zero; originalLine holds the position it was written against. Emitting zero
// would print "file.go" with no line at all in the prompt, so the original
// stands in.
func decodeUnresolvedThreads(data []byte) ([]ReviewComment, error) {
	var result struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							IsResolved bool `json:"isResolved"`
							Comments   struct {
								Nodes []struct {
									Author struct {
										Login string `json:"login"`
									} `json:"author"`
									Body         string `json:"body"`
									Path         string `json:"path"`
									Line         int    `json:"line"`
									OriginalLine int    `json:"originalLine"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("failed to parse graphql response: %w", err)
	}

	var comments []ReviewComment
	threads := result.Data.Repository.PullRequest.ReviewThreads.Nodes
	for _, thread := range threads {
		if thread.IsResolved {
			continue
		}
		if len(thread.Comments.Nodes) == 0 {
			continue
		}
		c := thread.Comments.Nodes[0]
		body := strings.TrimSpace(c.Body)
		if body == "" {
			continue
		}
		line := c.Line
		if line == 0 {
			line = c.OriginalLine
		}
		comments = append(comments, ReviewComment{
			Author: c.Author.Login,
			Body:   body,
			State:  "COMMENTED",
			Path:   c.Path,
			Line:   line,
		})
	}
	return comments, nil
}

// FormatReviewsPrompt formats a list of review comments into a prompt suitable
// for sending to an AI coding agent.
func FormatReviewsPrompt(comments []ReviewComment) string {
	if len(comments) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "I have %d PR review comment(s) to address:\n\n", len(comments))
	for i, c := range comments {
		fmt.Fprintf(&sb, "--- Review %d (by @%s", i+1, c.Author)
		if c.State != "" && c.State != "COMMENTED" {
			fmt.Fprintf(&sb, ", %s", c.State)
		}
		if c.Path != "" {
			if c.Line > 0 {
				fmt.Fprintf(&sb, ", %s:%d", c.Path, c.Line)
			} else {
				fmt.Fprintf(&sb, ", %s", c.Path)
			}
		}
		sb.WriteString(") ---\n")
		sb.WriteString(c.Body)
		sb.WriteString("\n\n")
	}
	sb.WriteString("Please address all of these review comments.")
	return sb.String()
}

// Issue represents a GitHub issue
type Issue struct {
	Number int
	Title  string
	Body   string
	Labels []string
}

// GetIssue fetches a GitHub issue by number using the gh CLI
func (pm *PRManager) GetIssue(number int) (*Issue, error) {
	if !pm.IsInstalled() {
		return nil, fmt.Errorf("gh CLI is not installed. Install it from https://cli.github.com/")
	}

	output, stderr, err := ghRun("", "issue", "view", strconv.Itoa(number), "--json", "number,title,body,labels")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch issue #%d: %w\nOutput: %s", number, err, stderr)
	}

	return decodeIssue(output)
}

// decodeIssue folds a `gh issue view --json number,title,body,labels` payload
// into an Issue, flattening the label objects gh nests to the names callers
// actually match on.
func decodeIssue(data []byte) (*Issue, error) {
	var raw struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse issue response: %w", err)
	}

	labels := make([]string, len(raw.Labels))
	for i, l := range raw.Labels {
		labels[i] = l.Name
	}

	return &Issue{
		Number: raw.Number,
		Title:  raw.Title,
		Body:   raw.Body,
		Labels: labels,
	}, nil
}

// issueBranchSlugRe matches any sequence of non-alphanumeric characters
var issueBranchSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// IssueBranchName generates a Git branch name from an issue number and title.
// e.g. issue #42 "Add dark mode" → "issue-42-add-dark-mode"
func IssueBranchName(number int, title string) string {
	slug := strings.ToLower(title)
	slug = issueBranchSlugRe.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 40 {
		slug = slug[:40]
		slug = strings.TrimRight(slug, "-")
	}
	return fmt.Sprintf("issue-%d-%s", number, slug)
}

// PRManager handles GitHub PR operations
type PRManager struct {
	ghOnce      sync.Once
	ghInstalled bool
}

// New creates a new PR manager
func New() *PRManager {
	return &PRManager{}
}

// CreatePR creates a GitHub pull request using gh CLI
func (pm *PRManager) CreatePR(branch, baseBranch, title, body string) (string, error) {
	// Check if gh CLI is installed
	if !pm.IsInstalled() {
		return "", fmt.Errorf("gh CLI is not installed. Install it from https://cli.github.com/")
	}

	// Check if user is authenticated
	cmd := exec.Command("gh", "auth", "status")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("not authenticated with GitHub. Run 'gh auth login'")
	}

	output, stderr, err := ghRun("", createPRArgs(branch, baseBranch, title, body)...)
	if err != nil {
		return "", fmt.Errorf("failed to create PR: %w\nOutput: %s", err, stderr)
	}

	// The PR URL is printed on stdout; stderr (progress, debug chatter) is
	// deliberately excluded so it can't pollute the stored URL.
	prURL := strings.TrimSpace(string(output))
	return prURL, nil
}

// createPRArgs assembles the `gh pr create` command line. An empty title falls
// back to the branch name read back as a sentence, since gh would otherwise
// prompt for one on a terminal it does not have.
//
// --body is always passed, empty or not: without it, non-interactive gh (stdin
// is not a terminal here) fails with "must provide --title and --body".
func createPRArgs(branch, baseBranch, title, body string) []string {
	if title == "" {
		title = gitutil.BranchToTitle(branch)
	}
	return []string{
		"pr", "create",
		"--base", baseBranch,
		"--head", branch,
		"--title", title,
		"--body", body,
	}
}

// prViewError interprets a failed `gh pr view` invocation. A branch with no
// PR, or a repo with no GitHub remote, is a normal condition and yields nil;
// anything else (auth expired, offline, ...) is a real error.
//
// The remote case is normally settled by hasGitHubRemote before gh is called
// at all; the excuse stays here for the repo whose remote could not be read,
// and because gh reaches this verdict by paths of its own.
func prViewError(output []byte, err error) error {
	out := string(output)
	if strings.Contains(out, "no pull requests found") || strings.Contains(out, "no git remotes") {
		return nil
	}
	return fmt.Errorf("gh pr view failed: %w\nOutput: %s", err, strings.TrimSpace(out))
}

// PRInfo is what publishing needs to know about a branch's existing PR: where
// it is, whether it is still open, which commit it currently serves, and the
// body — the last so a caller can tell an autopilot-written description from a
// human's before overwriting one.
type PRInfo struct {
	URL     string
	State   string // lowercased: "open", "merged", "closed"
	HeadSha string
	Body    string
}

// FindPR looks up the pull request for a branch, or reports that there is
// none. No PR is (nil, nil), not an error: it is the answer that makes
// creating one the right next move, and every caller branches on it.
func (pm *PRManager) FindPR(branch, repoDir string) (*PRInfo, error) {
	if !pm.IsInstalled() || !hasGitHubRemote(repoDir) {
		return nil, nil
	}
	output, stderr, err := ghRun(repoDir, "pr", "view", branch, "--json", "url,state,headRefOid,body")
	if err != nil {
		return nil, prViewError(stderr, err)
	}
	var raw struct {
		URL        string `json:"url"`
		State      string `json:"state"`
		HeadRefOid string `json:"headRefOid"`
		Body       string `json:"body"`
	}
	if err := json.Unmarshal(output, &raw); err != nil {
		return nil, fmt.Errorf("unexpected gh pr view output: %w", err)
	}
	return &PRInfo{
		URL:     raw.URL,
		State:   strings.ToLower(raw.State),
		HeadSha: raw.HeadRefOid,
		Body:    raw.Body,
	}, nil
}

// UpdatePR rewrites an existing PR's title and body. The caller decides
// whether that is its place — FindPR's Body is what makes that call possible.
func (pm *PRManager) UpdatePR(branch, title, body string) error {
	if !pm.IsInstalled() {
		return fmt.Errorf("gh CLI is not installed. Install it from https://cli.github.com/")
	}
	_, stderr, err := ghRun("", "pr", "edit", branch, "--title", title, "--body", body)
	if err != nil {
		return fmt.Errorf("failed to update PR: %w\nOutput: %s", err, stderr)
	}
	return nil
}

// GetPRStatus checks if a PR exists for the given branch
func (pm *PRManager) GetPRStatus(branch string) (string, error) {
	if !pm.IsInstalled() || !hasGitHubRemote("") {
		return "", nil // Silently fail if gh cannot answer for this repo
	}

	output, stderr, err := ghRun("", "pr", "view", branch, "--json", "url", "--jq", ".url")
	if err != nil {
		return "", prViewError(stderr, err)
	}

	return strings.TrimSpace(string(output)), nil
}

// GetFullPRStatus returns the URL and state of a PR for the given branch.
// State is lowercased: "open", "merged", or "closed".
func (pm *PRManager) GetFullPRStatus(branch string) (url, state string, err error) {
	if !pm.IsInstalled() || !hasGitHubRemote("") {
		return "", "", nil
	}

	output, stderr, err := ghRun("", "pr", "view", branch, "--json", "url,state", "--jq", `"\(.url)\t\(.state)"`)
	if err != nil {
		return "", "", prViewError(stderr, err)
	}

	return splitURLState(output)
}

// splitURLState reads the tab-joined pair the --jq filter in GetFullPRStatus
// produces. A PR URL cannot contain a tab, so the split is unambiguous; a line
// without one means gh answered something other than what was asked, which is
// worth an error rather than a half-populated status.
func splitURLState(output []byte) (url, state string, err error) {
	parts := strings.SplitN(strings.TrimSpace(string(output)), "\t", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected gh output: %s", output)
	}
	return parts[0], strings.ToLower(parts[1]), nil
}

// rollupCheck is one entry of statusCheckRollup. GitHub check runs carry
// status/conclusion; legacy commit statuses (Jenkins, CircleCI, any Status
// API integration) are StatusContext objects carrying only state.
type rollupCheck struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	State      string `json:"state"`
}

// deriveCIStatus folds a status rollup into "success", "failure", "pending",
// or "" (no checks). Anything unrecognized counts as pending, never success:
// a false green on CI is worse than a lingering yellow.
func deriveCIStatus(checks []rollupCheck) string {
	if len(checks) == 0 {
		return ""
	}
	status := "success"
	for _, check := range checks {
		switch strings.ToUpper(check.Conclusion) {
		case "FAILURE", "CANCELLED", "TIMED_OUT", "ERROR", "STARTUP_FAILURE":
			return "failure"
		case "SUCCESS", "NEUTRAL", "SKIPPED":
			continue
		}
		switch strings.ToUpper(check.State) {
		case "FAILURE", "ERROR":
			return "failure"
		case "SUCCESS":
			continue
		}
		// Not conclusively finished: in-progress/queued/waiting check runs,
		// pending commit statuses, ACTION_REQUIRED, and unknown values.
		status = "pending"
	}
	return status
}

// GetPRCIStatus returns the combined CI check status for the PR on the given branch.
// Returns "success", "failure", "pending", or "" if no checks exist.
func (pm *PRManager) GetPRCIStatus(branch string) (string, error) {
	if !pm.IsInstalled() || !hasGitHubRemote("") {
		return "", nil
	}
	output, stderr, err := ghRun("", "pr", "view", branch, "--json", "statusCheckRollup")
	if err != nil {
		return "", prViewError(stderr, err)
	}
	return decodeCIStatus(output)
}

// decodeCIStatus folds a `gh pr view --json statusCheckRollup` payload down to
// one word. A PR with no checks configured answers null, which decodes to no
// checks at all rather than to a failure.
func decodeCIStatus(data []byte) (string, error) {
	var result struct {
		StatusCheckRollup []rollupCheck `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("failed to parse CI status response: %w", err)
	}
	return deriveCIStatus(result.StatusCheckRollup), nil
}

// BranchStatus holds the combined branch push and PR status for a workspace.
type BranchStatus struct {
	Pushed            bool
	RemoteDeleted     bool // branch was previously pushed but no longer exists in remote
	RemoteCheckFailed bool // git ls-remote failed; Pushed/RemoteDeleted are unreliable
	PRURL             string
	PRState           string // "open", "merged", "closed", ""
	MergeConflicts    bool
	CIStatus          string // "success", "failure", "pending", ""
}

// GetBranchAndPRStatus returns the combined remote branch existence and PR status
// for the given branch. repoDir is used as the working directory for git commands.
// wasPushed should reflect the previously known BranchPushed state so RemoteDeleted
// can be set correctly when the branch disappears from remote.
func (pm *PRManager) GetBranchAndPRStatus(branch, repoDir string, wasPushed bool) (BranchStatus, error) {
	var status BranchStatus

	// Check remote branch existence via git ls-remote (fast, no API rate limit).
	lsCmd := exec.Command("git", "ls-remote", "--heads", "origin", branch)
	if repoDir != "" {
		lsCmd.Dir = repoDir
	}
	lsOut, lsErr := lsCmd.Output()
	if lsErr != nil {
		status.RemoteCheckFailed = true
	} else {
		remoteExists := strings.TrimSpace(string(lsOut)) != ""
		status.Pushed = remoteExists
		if wasPushed && !remoteExists {
			status.RemoteDeleted = true
		}
	}

	// Fetch PR info in a single gh call if available. This runs every thirty
	// seconds for every workspace, so both gates are cheap and local: neither
	// gh's absence nor a non-GitHub remote may cost a round trip here.
	if !pm.IsInstalled() || !hasGitHubRemote(repoDir) {
		return status, nil
	}
	output, stderr, err := ghRun(repoDir, "pr", "view", branch, "--json", "url,state,mergeable,statusCheckRollup")
	if err != nil {
		// Partial ls-remote status is still returned alongside any real error.
		return status, prViewError(stderr, err)
	}
	pr, err := decodePRSummary(output)
	if err != nil {
		return status, err
	}
	status.PRURL = pr.URL
	status.PRState = pr.State
	status.MergeConflicts = pr.MergeConflicts
	status.CIStatus = pr.CIStatus

	return status, nil
}

// prSummary is the half of a branch's status that only GitHub knows, as
// distinct from the half git ls-remote answers.
type prSummary struct {
	URL            string
	State          string // "open", "merged", "closed", ""
	MergeConflicts bool
	CIStatus       string
}

// decodePRSummary folds the combined `gh pr view --json
// url,state,mergeable,statusCheckRollup` payload used by the status poll.
// Mergeable is a tri-state — MERGEABLE, CONFLICTING, UNKNOWN while GitHub is
// still computing the merge — and only CONFLICTING is reported as a conflict,
// so a PR opened seconds ago does not flash a warning it may well retract.
func decodePRSummary(data []byte) (prSummary, error) {
	var raw struct {
		URL               string        `json:"url"`
		State             string        `json:"state"`
		Mergeable         string        `json:"mergeable"`
		StatusCheckRollup []rollupCheck `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return prSummary{}, fmt.Errorf("failed to parse PR status response: %w", err)
	}
	return prSummary{
		URL:            raw.URL,
		State:          strings.ToLower(raw.State),
		MergeConflicts: strings.EqualFold(raw.Mergeable, "CONFLICTING"),
		CIStatus:       deriveCIStatus(raw.StatusCheckRollup),
	}, nil
}

// IsInstalled reports whether the gh CLI is available on PATH.
// The result is cached after the first check.
func (pm *PRManager) IsInstalled() bool {
	pm.ghOnce.Do(func() {
		pm.ghInstalled = exec.Command("gh", "--version").Run() == nil
	})
	return pm.ghInstalled
}
