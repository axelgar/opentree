package github

import (
	"fmt"
	"strings"
	"testing"
)

// stubGHCI answers `gh pr view` with the given rollup payload and `gh run
// view` with the given log, recording nothing — the assertions are on what
// comes back.
func stubGHCI(t *testing.T, rollupJSON, runLog string) {
	t.Helper()
	stubGH(t, `case "$1" in`+"\n"+
		`pr) cat <<'ROLLUP'`+"\n"+rollupJSON+"\n"+`ROLLUP`+"\n;;\n"+
		`run) cat <<'RUNLOG'`+"\n"+runLog+"\n"+`RUNLOG`+"\n;;\n"+
		`esac`)
}

func TestFetchFailingChecks_SelectsFailuresAndFetchesLogs(t *testing.T) {
	stubGHCI(t, `{
		"headRefOid": "abc123",
		"statusCheckRollup": [
			{"name": "test", "conclusion": "FAILURE", "detailsUrl": "https://github.com/acme/repo/actions/runs/42/job/7"},
			{"name": "lint", "conclusion": "SUCCESS", "detailsUrl": "https://github.com/acme/repo/actions/runs/42/job/8"},
			{"context": "jenkins/build", "state": "FAILURE", "targetUrl": "https://ci.acme.dev/build/9"},
			{"name": "deploy", "status": "IN_PROGRESS"}
		]
	}`, "FAIL: TestThing\nexit status 1")

	sha, failures, err := New().FetchFailingChecks("feat/x", "")
	if err != nil {
		t.Fatalf("FetchFailingChecks: %v", err)
	}
	if sha != "abc123" {
		t.Errorf("sha = %q, want the head commit the caller watermarks on", sha)
	}
	if len(failures) != 2 {
		t.Fatalf("failures = %+v, want the red check and the red context, nothing running or green", failures)
	}
	if failures[0].Name != "test" || !strings.Contains(failures[0].Log, "FAIL: TestThing") {
		t.Errorf("failures[0] = %+v, want the Actions check with its log tail", failures[0])
	}
	if failures[1].Name != "jenkins/build" || failures[1].Log != "" || failures[1].Link != "https://ci.acme.dev/build/9" {
		t.Errorf("failures[1] = %+v, want the Status API context with link only — gh has no log for it", failures[1])
	}
}

func TestFetchFailingChecks_GreenIsNotAnError(t *testing.T) {
	stubGHCI(t, `{"headRefOid": "abc123", "statusCheckRollup": [{"name": "test", "conclusion": "SUCCESS"}]}`, "")

	sha, failures, err := New().FetchFailingChecks("feat/x", "")
	if err != nil || len(failures) != 0 || sha != "abc123" {
		t.Errorf("(%q, %v, %v), want the sha, no failures, no error", sha, failures, err)
	}
}

func TestFetchFailingChecks_NoPRIsQuiet(t *testing.T) {
	stubGH(t, `if [ "$1" = "pr" ]; then echo 'no pull requests found' >&2; exit 1; fi`)

	sha, failures, err := New().FetchFailingChecks("feat/x", "")
	if err != nil || failures != nil || sha != "" {
		t.Errorf("(%q, %v, %v), want the empty answer — no PR is a state, not a fault", sha, failures, err)
	}
}

func TestTailBounded_KeepsTheEndOnLineBoundaries(t *testing.T) {
	var lines []string
	for i := range 300 {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	got := tailBounded(strings.Join(lines, "\n"), 150, 1<<20)
	if strings.Count(got, "\n") != 149 || !strings.HasPrefix(got, "line 150") || !strings.HasSuffix(got, "line 299") {
		t.Errorf("tail = %q…%q, want the last 150 whole lines", got[:20], got[len(got)-10:])
	}

	got = tailBounded("aaaa\nbbbb\ncccc", 150, 7)
	if got != "cccc" {
		t.Errorf("byte-capped tail = %q, want the whole last line only", got)
	}
}

func TestFormatCIPrompt(t *testing.T) {
	prompt := FormatCIPrompt([]CheckFailure{
		{Name: "test", Link: "https://x/runs/1", Log: "FAIL: TestThing"},
		{Name: "jenkins/build", Link: "https://ci/9"},
	})
	for _, want := range []string{
		"2 check(s)",
		"--- Check: test ---",
		"```\nFAIL: TestThing\n```",
		"--- Check: jenkins/build ---",
		"https://ci/9",
		"Fix what makes these checks fail.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt lacks %q:\n%s", want, prompt)
		}
	}
	if FormatCIPrompt(nil) != "" {
		t.Error("no failures must format to nothing")
	}
}

func TestReviewsFingerprint(t *testing.T) {
	a := ReviewComment{Author: "sam", Body: "rename this", Path: "a.go", Line: 3}
	b := ReviewComment{Author: "kai", Body: "add a test", Path: "b.go", Line: 9}

	if ReviewsFingerprint(nil) != "" {
		t.Error("no comments must fingerprint to nothing — \"\" is the never-forwarded watermark")
	}
	if ReviewsFingerprint([]ReviewComment{a, b}) != ReviewsFingerprint([]ReviewComment{b, a}) {
		t.Error("a reshuffled identical set is not new feedback")
	}
	if ReviewsFingerprint([]ReviewComment{a}) == ReviewsFingerprint([]ReviewComment{a, b}) {
		t.Error("a different set must fingerprint differently")
	}
	edited := a
	edited.Body = "rename this, please"
	if ReviewsFingerprint([]ReviewComment{a}) == ReviewsFingerprint([]ReviewComment{edited}) {
		t.Error("an edited body is new feedback")
	}
}
