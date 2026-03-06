package github

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
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
	if !pm.isGHInstalled() {
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
type PRManager struct{}

// New creates a new PR manager
func New() *PRManager {
	return &PRManager{}
}

// CreatePR creates a GitHub pull request using gh CLI
func (pm *PRManager) CreatePR(branch, baseBranch, title, body string) (string, error) {
	// Check if gh CLI is installed
	if !pm.isGHInstalled() {
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
	if !pm.isGHInstalled() {
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
	if !pm.isGHInstalled() {
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

// isGHInstalled checks if gh CLI is available
func (pm *PRManager) isGHInstalled() bool {
	cmd := exec.Command("gh", "--version")
	return cmd.Run() == nil
}
