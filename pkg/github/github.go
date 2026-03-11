package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/axelgar/opentree/pkg/gitutil"
)

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

// prURLRe matches GitHub PR URLs and captures owner, repo, and number.
var prURLRe = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/pull/(\d+)`)

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
	cmd := exec.Command("gh", "pr", "view", branch, "--json", "url,reviews")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, nil // no PR for this branch
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
			if err == nil {
				comments = append(comments, inlineComments...)
			}
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
	cmd := exec.Command("gh", "api", "graphql",
		"-f", fmt.Sprintf("query=%s", graphqlUnresolvedThreadsQuery),
		"-f", fmt.Sprintf("owner=%s", owner),
		"-f", fmt.Sprintf("repo=%s", repo),
		"-F", fmt.Sprintf("number=%d", prNumber),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("graphql query failed: %w", err)
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
	sb.WriteString(fmt.Sprintf("I have %d PR review comment(s) to address:\n\n", len(comments)))
	for i, c := range comments {
		sb.WriteString(fmt.Sprintf("--- Review %d (by @%s", i+1, c.Author))
		if c.State != "" && c.State != "COMMENTED" {
			sb.WriteString(fmt.Sprintf(", %s", c.State))
		}
		if c.Path != "" {
			if c.Line > 0 {
				sb.WriteString(fmt.Sprintf(", %s:%d", c.Path, c.Line))
			} else {
				sb.WriteString(fmt.Sprintf(", %s", c.Path))
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

	cmd := exec.Command("gh", "issue", "view", strconv.Itoa(number), "--json", "number,title,body,labels")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch issue #%d: %w\nOutput: %s", number, err, output)
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
	sb.WriteString(fmt.Sprintf("# Issue #%d: %s\n\n", issue.Number, issue.Title))
	if len(issue.Labels) > 0 {
		sb.WriteString(fmt.Sprintf("**Labels:** %s\n\n", strings.Join(issue.Labels, ", ")))
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
	
	if body != "" {
		args = append(args, "--body", body)
	}

	cmd = exec.Command("gh", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to create PR: %w\nOutput: %s", err, output)
	}

	// Extract PR URL from output
	prURL := strings.TrimSpace(string(output))
	return prURL, nil
}

// GetPRStatus checks if a PR exists for the given branch
func (pm *PRManager) GetPRStatus(branch string) (string, error) {
	if !pm.IsInstalled() {
		return "", nil // Silently fail if gh not installed
	}

	cmd := exec.Command("gh", "pr", "view", branch, "--json", "url", "--jq", ".url")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil // No PR exists
	}

	return strings.TrimSpace(string(output)), nil
}

// GetFullPRStatus returns the URL and state of a PR for the given branch.
// State is lowercased: "open", "merged", or "closed".
func (pm *PRManager) GetFullPRStatus(branch string) (url, state string, err error) {
	if !pm.IsInstalled() {
		return "", "", nil
	}

	cmd := exec.Command("gh", "pr", "view", branch, "--json", "url,state", "--jq", `"\(.url)\t\(.state)"`)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", nil // No PR exists
	}

	parts := strings.SplitN(strings.TrimSpace(string(output)), "\t", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected gh output: %s", output)
	}

	return parts[0], strings.ToLower(parts[1]), nil
}

// GetPRCIStatus returns the combined CI check status for the PR on the given branch.
// Returns "success", "failure", "pending", or "" if no checks exist.
func (pm *PRManager) GetPRCIStatus(branch string) (string, error) {
	if !pm.IsInstalled() {
		return "", nil
	}
	cmd := exec.Command("gh", "pr", "view", branch, "--json", "statusCheckRollup")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil
	}
	var result struct {
		StatusCheckRollup []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return "", nil
	}
	if len(result.StatusCheckRollup) == 0 {
		return "", nil
	}
	for _, check := range result.StatusCheckRollup {
		c := strings.ToUpper(check.Conclusion)
		if c == "FAILURE" || c == "CANCELLED" || c == "TIMED_OUT" {
			return "failure", nil
		}
	}
	for _, check := range result.StatusCheckRollup {
		s := strings.ToUpper(check.Status)
		c := strings.ToUpper(check.Conclusion)
		if s == "IN_PROGRESS" || s == "QUEUED" || s == "PENDING" || (c == "" && s == "IN_PROGRESS") {
			return "pending", nil
		}
	}
	return "success", nil
}

// BranchStatus holds the combined branch push and PR status for a workspace.
type BranchStatus struct {
	Pushed            bool
	RemoteDeleted     bool   // branch was previously pushed but no longer exists in remote
	RemoteCheckFailed bool   // git ls-remote failed; Pushed/RemoteDeleted are unreliable
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
	cmd := exec.Command("gh", "pr", "view", branch, "--json", "url,state,mergeable,statusCheckRollup")
	output, err := cmd.CombinedOutput()
	if err != nil {
		// No PR found for this branch — not an error.
		return status, nil
	}
	var raw struct {
		URL      string `json:"url"`
		State    string `json:"state"`
		Mergeable string `json:"mergeable"`
		StatusCheckRollup []struct {
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(output, &raw); err != nil {
		return status, nil
	}
	status.PRURL = raw.URL
	status.PRState = strings.ToLower(raw.State)
	status.MergeConflicts = strings.ToUpper(raw.Mergeable) == "CONFLICTING"

	// Derive CI status (same logic as GetPRCIStatus).
	for _, check := range raw.StatusCheckRollup {
		c := strings.ToUpper(check.Conclusion)
		if c == "FAILURE" || c == "CANCELLED" || c == "TIMED_OUT" {
			status.CIStatus = "failure"
			return status, nil
		}
	}
	for _, check := range raw.StatusCheckRollup {
		s := strings.ToUpper(check.Status)
		if s == "IN_PROGRESS" || s == "QUEUED" || s == "PENDING" {
			status.CIStatus = "pending"
			return status, nil
		}
	}
	if len(raw.StatusCheckRollup) > 0 {
		status.CIStatus = "success"
	}

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
