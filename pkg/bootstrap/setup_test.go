package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// collect gathers emitted lines for assertions.
func collect(lines *[]string) func(string) {
	return func(line string) { *lines = append(*lines, line) }
}

func TestRunSetup_RunsInOrderInTheWorktree(t *testing.T) {
	dir := t.TempDir()
	var lines []string

	err := RunSetup(context.Background(), dir, []string{
		"echo first > order.txt",
		"echo second >> order.txt",
		"pwd",
	}, collect(&lines))
	if err != nil {
		t.Fatalf("RunSetup: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "order.txt"))
	if err != nil {
		t.Fatalf("the commands did not run in the worktree: %v", err)
	}
	if string(got) != "first\nsecond\n" {
		t.Errorf("order.txt = %q, want the commands run in order", got)
	}

	// Each command is announced the way a shell echoes it, so the output reads
	// as a transcript rather than as text from nowhere.
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "$ pwd") {
		t.Errorf("output does not announce its commands:\n%s", joined)
	}
	if !strings.Contains(joined, dir) {
		t.Errorf("pwd printed %q, want the worktree %s", joined, dir)
	}
}

// A build whose install did not finish is not worth starting.
func TestRunSetup_StopsAtTheFirstFailure(t *testing.T) {
	dir := t.TempDir()
	var lines []string

	err := RunSetup(context.Background(), dir, []string{
		"exit 3",
		"touch should-not-exist",
	}, collect(&lines))
	if err == nil {
		t.Fatal("RunSetup reported success for a command that failed")
	}
	// The failing command is named: "exit status 3" on its own says nothing
	// about which of five commands produced it.
	if !strings.Contains(err.Error(), "exit 3") {
		t.Errorf("error = %q, want it to name the command", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "should-not-exist")); err == nil {
		t.Error("the later command ran anyway")
	}
}

func TestRunSetup_StreamsBothStreams(t *testing.T) {
	var lines []string

	err := RunSetup(context.Background(), t.TempDir(), []string{
		"echo to-stdout; echo to-stderr >&2",
	}, collect(&lines))
	if err != nil {
		t.Fatalf("RunSetup: %v", err)
	}

	joined := strings.Join(lines, "\n")
	for _, want := range []string{"to-stdout", "to-stderr"} {
		if !strings.Contains(joined, want) {
			t.Errorf("output is missing %q:\n%s", want, joined)
		}
	}
}

// A progress bar redraws itself in place, and rendered whole it is a hundred
// copies of one line with the interesting one at the end.
func TestRunSetup_KeepsOnlyTheLastRedraw(t *testing.T) {
	var lines []string

	if err := RunSetup(context.Background(), t.TempDir(), []string{
		`printf '10%%\r50%%\rdone\n'`,
	}, collect(&lines)); err != nil {
		t.Fatalf("RunSetup: %v", err)
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "$ ") {
			continue // the echoed command, which quotes the redraws deliberately
		}
		if strings.Contains(line, "10%") || strings.Contains(line, "50%") {
			t.Errorf("line %q kept the redraws it replaced", line)
		}
	}
	if !strings.Contains(strings.Join(lines, "\n"), "done") {
		t.Errorf("output lost the final state of the line: %v", lines)
	}
}

// No timeout — any number is wrong for somebody — so cancelling is how a hung
// setup ends. It has to reach the children too: killing only the shell leaves
// the install running against a worktree nobody is watching.
func TestRunSetup_CancelStopsTheProcessGroup(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "child-survived")
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	// The child outlives its parent shell unless the whole group is signalled.
	err := RunSetup(ctx, dir, []string{
		"sh -c 'sleep 5; touch " + marker + "' & wait",
	}, func(string) {})
	if err == nil {
		t.Fatal("a cancelled setup reported success")
	}
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Errorf("cancel took %s — it waited for the command instead of stopping it", elapsed)
	}

	// Past when the child would have finished, had it survived.
	time.Sleep(time.Second)
	if _, err := os.Stat(marker); err == nil {
		t.Error("a child of the cancelled command was still running")
	}
}

func TestRunSetup_NothingConfigured(t *testing.T) {
	var lines []string
	if err := RunSetup(context.Background(), t.TempDir(), nil, collect(&lines)); err != nil {
		t.Fatalf("RunSetup(nil): %v", err)
	}
	if len(lines) != 0 {
		t.Errorf("emitted %v with nothing to run", lines)
	}
}
