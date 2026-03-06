package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

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
		// Use branch name as title if not provided
		args = append(args, "--title", branch)
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

// IsInstalled reports whether the gh CLI is available on PATH.
// The result is cached after the first check.
func (pm *PRManager) IsInstalled() bool {
	pm.ghOnce.Do(func() {
		pm.ghInstalled = exec.Command("gh", "--version").Run() == nil
	})
	return pm.ghInstalled
}
