package github

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// The rollup the badge reads collapses CI to one word; forwarding a failure to
// an agent needs the opposite — which check failed, and what it printed. This
// file is that fetch. Nothing here decides when to forward: watermarks and
// cadence belong to the caller, and this only answers "what is red right now,
// on which commit".

// Bounds on what a forwarded failure carries. The tail of each log, because
// that is where a test runner's summary lives; bounded per check and in total,
// because a build log can be megabytes and the agent's context is the scarcest
// thing in the loop.
const (
	ciLogTailLines = 150
	ciLogTailBytes = 16 * 1024
	ciTotalBytes   = 48 * 1024
)

// CheckFailure is one red check, with as much of its log as could be had. A
// Status API context (Jenkins, and every other non-Actions integration) has no
// log gh can reach, so Log is empty and Link is what the agent gets.
type CheckFailure struct {
	Name string
	Link string
	Log  string
}

// actionsRunRe pulls the run id out of a check run's details URL. Only GitHub
// Actions runs have logs `gh run view` can fetch.
var actionsRunRe = regexp.MustCompile(`/actions/runs/(\d+)`)

// FetchFailingChecks reports the PR's failing checks and the head commit they
// failed on. The sha is the caller's watermark: one forward per commit, and a
// new push re-arms it by changing the answer.
//
// (sha, nil, nil) is a PR whose checks are green or still running — the
// ordinary answer, not an error.
func (pm *PRManager) FetchFailingChecks(branch, repoDir string) (string, []CheckFailure, error) {
	if !pm.IsInstalled() || !hasGitHubRemote(repoDir) {
		return "", nil, nil
	}
	output, stderr, err := ghRun(repoDir, "pr", "view", branch, "--json", "headRefOid,statusCheckRollup")
	if err != nil {
		return "", nil, prViewError(stderr, err)
	}

	var raw struct {
		HeadRefOid        string `json:"headRefOid"`
		StatusCheckRollup []struct {
			Name       string `json:"name"`    // check runs
			Context    string `json:"context"` // status contexts
			Conclusion string `json:"conclusion"`
			State      string `json:"state"`
			DetailsURL string `json:"detailsUrl"`
			TargetURL  string `json:"targetUrl"`
		} `json:"statusCheckRollup"`
	}
	if err := json.Unmarshal(output, &raw); err != nil {
		return "", nil, fmt.Errorf("unexpected gh pr view output: %w", err)
	}

	var failures []CheckFailure
	budget := ciTotalBytes
	// One Actions run carries several jobs, each its own check; its log is
	// fetched once and attached to the first check that needed it.
	fetched := map[string]bool{}
	for _, c := range raw.StatusCheckRollup {
		if !checkFailed(c.Conclusion, c.State) {
			continue
		}
		f := CheckFailure{Name: c.Name, Link: c.DetailsURL}
		if f.Name == "" {
			f.Name = c.Context
		}
		if f.Link == "" {
			f.Link = c.TargetURL
		}
		if m := actionsRunRe.FindStringSubmatch(f.Link); len(m) == 2 && !fetched[m[1]] && budget > 0 {
			fetched[m[1]] = true
			if log := pm.fetchRunLog(repoDir, m[1], budget); log != "" {
				f.Log = log
				budget -= len(log)
			}
		}
		failures = append(failures, f)
	}
	return raw.HeadRefOid, failures, nil
}

// fetchRunLog is the tail of an Actions run's failed-step output, bounded by
// its own cap and whatever remains of the caller's total. Best-effort: a log
// that cannot be fetched — expired, still uploading, no permission — costs the
// failure its detail, not the caller its answer.
func (pm *PRManager) fetchRunLog(repoDir, runID string, budget int) string {
	output, _, err := ghRun(repoDir, "run", "view", runID, "--log-failed")
	if err != nil {
		return ""
	}
	return tailBounded(string(output), ciLogTailLines, min(ciLogTailBytes, budget))
}

// tailBounded keeps the last maxLines lines and maxBytes bytes of s, cutting
// on a line boundary so the first kept line is a whole one.
func tailBounded(s string, maxLines, maxBytes int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" || maxBytes <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	s = strings.Join(lines, "\n")
	if len(s) > maxBytes {
		s = s[len(s)-maxBytes:]
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return s
}

// FormatCIPrompt renders failing checks as the prompt an agent receives, the
// way FormatReviewsPrompt does for reviews. Empty input formats to "".
func FormatCIPrompt(failures []CheckFailure) string {
	if len(failures) == 0 {
		return ""
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "CI is failing on this PR — %d check(s):\n\n", len(failures))
	for _, f := range failures {
		fmt.Fprintf(&sb, "--- Check: %s ---\n", f.Name)
		if f.Link != "" {
			sb.WriteString(f.Link + "\n")
		}
		if f.Log != "" {
			sb.WriteString("```\n" + f.Log + "\n```\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("Fix what makes these checks fail.")
	return sb.String()
}
