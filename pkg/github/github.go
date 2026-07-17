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

	// Fetch top-level reviews and PR URL in one call.
	output, stderr, err := ghRun("", "pr", "view", branch, "--json", "url,reviews")
	if err != nil {
		if pErr := prViewError(stderr, err); pErr != nil {
			return nil, pErr
		}
		return nil, nil // branch has no PR
	}

	var prData struct {
		URL     string `json:"url"`
		Reviews []struct {
			Author struct {
				Login string `json:"login"`
			} `json:"author"`
			Body  string `json:"body"`
			State string `json:"state"`
		} `json:"reviews"`
	}
	if err := json.Unmarshal(output, &prData); err != nil {
		return nil, fmt.Errorf("failed to parse pr reviews: %w", err)
	}

	var comments []ReviewComment

	// Only include reviews that actively requested changes and have a body.
	// APPROVED, DISMISSED, COMMENTED, and PENDING are all skipped.
	for _, r := range prData.Reviews {
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

	// Fetch unresolved inline review threads via GraphQL.
	if prData.URL != "" {
		owner, repo, prNumber, parseErr := parsePRURL(prData.URL)
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
	if err := json.Unmarshal(out, &result); err != nil {
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

	var raw struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		Body   string `json:"body"`
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(output, &raw); err != nil {
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

// IssueTaskContent formats a GitHub issue as a TASK.md file for the AI agent.
func IssueTaskContent(issue *Issue) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Issue #%d: %s\n\n", issue.Number, issue.Title)
	if len(issue.Labels) > 0 {
		fmt.Fprintf(&sb, "**Labels:** %s\n\n", strings.Join(issue.Labels, ", "))
	}
	sb.WriteString("## Description\n\n")
	if issue.Body != "" {
		sb.WriteString(issue.Body)
		sb.WriteString("\n")
	} else {
		sb.WriteString("_No description provided._\n")
	}
	return sb.String()
}

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

	// Create PR
	args := []string{"pr", "create", "--base", baseBranch, "--head", branch}

	if title != "" {
		args = append(args, "--title", title)
	} else {
		args = append(args, "--title", gitutil.BranchToTitle(branch))
	}

	// Always pass --body: without it, non-interactive gh (stdin is not a
	// terminal here) fails with "must provide --title and --body".
	args = append(args, "--body", body)

	output, stderr, err := ghRun("", args...)
	if err != nil {
		return "", fmt.Errorf("failed to create PR: %w\nOutput: %s", err, stderr)
	}

	// The PR URL is printed on stdout; stderr (progress, debug chatter) is
	// deliberately excluded so it can't pollute the stored URL.
	prURL := strings.TrimSpace(string(output))
	return prURL, nil
}

// prViewError interprets a failed `gh pr view` invocation. A branch with no
// PR, or a repo with no GitHub remote, is a normal condition and yields nil;
// anything else (auth expired, offline, ...) is a real error.
func prViewError(output []byte, err error) error {
	out := string(output)
	if strings.Contains(out, "no pull requests found") || strings.Contains(out, "no git remotes") {
		return nil
	}
	return fmt.Errorf("gh pr view failed: %w\nOutput: %s", err, strings.TrimSpace(out))
}

// GetPRStatus checks if a PR exists for the given branch
func (pm *PRManager) GetPRStatus(branch string) (string, error) {
	if !pm.IsInstalled() {
		return "", nil // Silently fail if gh not installed
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
	if !pm.IsInstalled() {
		return "", "", nil
	}

	output, stderr, err := ghRun("", "pr", "view", branch, "--json", "url,state", "--jq", `"\(.url)\t\(.state)"`)
	if err != nil {
		return "", "", prViewError(stderr, err)
	}

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
	if !pm.IsInstalled() {
		return "", nil
	}
	output, stderr, err := ghRun("", "pr", "view", branch, "--json", "statusCheckRollup")
	if err != nil {
		return "", prViewError(stderr, err)
	}
	var result struct {
		StatusCheckRollup []rollupCheck `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
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

	// Fetch PR info in a single gh call if available.
	if !pm.IsInstalled() {
		return status, nil
	}
	output, stderr, err := ghRun(repoDir, "pr", "view", branch, "--json", "url,state,mergeable,statusCheckRollup")
	if err != nil {
		// Partial ls-remote status is still returned alongside any real error.
		return status, prViewError(stderr, err)
	}
	var raw struct {
		URL               string        `json:"url"`
		State             string        `json:"state"`
		Mergeable         string        `json:"mergeable"`
		StatusCheckRollup []rollupCheck `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(output, &raw); err != nil {
		return status, fmt.Errorf("failed to parse PR status response: %w", err)
	}
	status.PRURL = raw.URL
	status.PRState = strings.ToLower(raw.State)
	status.MergeConflicts = strings.ToUpper(raw.Mergeable) == "CONFLICTING"
	status.CIStatus = deriveCIStatus(raw.StatusCheckRollup)

	return status, nil
}

// IsInstalled reports whether the gh CLI is available on PATH.
// The result is cached after the first check.
func (pm *PRManager) IsInstalled() bool {
	pm.ghOnce.Do(func() {
		pm.ghInstalled = exec.Command("gh", "--version").Run() == nil
	})
	return pm.ghInstalled
}
