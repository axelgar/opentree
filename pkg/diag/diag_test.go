package diag

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Off is the state every user is in, and it has to cost nothing and record
// nothing. A diagnostic that wrote a file nobody asked for would be a
// different kind of bug.
func TestLog_SilentUntilAskedFor(t *testing.T) {
	reset(t)
	t.Setenv(EnvVar, "")

	Init("test")
	Log("test", "should not be recorded anywhere")

	if Enabled() {
		t.Error("Enabled() = true with no " + EnvVar + " set")
	}
	if got := Path(); got != "" {
		t.Errorf("Path() = %q, want empty", got)
	}
}

func TestLog_WritesWhatItWasGiven(t *testing.T) {
	reset(t)
	path := filepath.Join(t.TempDir(), "opentree.log")
	t.Setenv(EnvVar, path)

	Init("chat")
	Log("chat", "launching agent", "workspace", "fix-auth", "binary", "/usr/local/bin/claude-agent-acp")

	if !Enabled() || Path() != path {
		t.Fatalf("Path() = %q, want %q", Path(), path)
	}
	body := read(t, path)
	for _, want := range []string{"[chat]", "launching agent", "workspace=fix-auth", "binary=/usr/local/bin/claude-agent-acp"} {
		if !strings.Contains(body, want) {
			t.Errorf("log is missing %q:\n%s", want, body)
		}
	}
}

// A value with a space in it must not read as the start of the next key —
// which is the whole reason for the key=value shape rather than a sentence.
func TestLog_ValuesWithSpacesStayOneValue(t *testing.T) {
	reset(t)
	path := filepath.Join(t.TempDir(), "opentree.log")
	t.Setenv(EnvVar, path)

	Init("chat")
	Log("chat", "handshake failed", "err", "context deadline exceeded", "workspace", "fix-auth")

	body := read(t, path)
	if !strings.Contains(body, `err="context deadline exceeded"`) {
		t.Errorf("a multi-word value was not quoted:\n%s", body)
	}
	if !strings.Contains(body, "workspace=fix-auth") {
		t.Errorf("the key after a quoted value was lost:\n%s", body)
	}
}

// It is opened for append by every opentree that shares the path — a dashboard
// and one chat per workspace, at once — so a second process must not truncate
// the first one's log.
func TestInit_AppendsRatherThanTruncates(t *testing.T) {
	reset(t)
	path := filepath.Join(t.TempDir(), "opentree.log")
	t.Setenv(EnvVar, path)

	Init("first")
	Log("first", "before")
	reset(t)
	Init("second")
	Log("second", "after")

	body := read(t, path)
	if !strings.Contains(body, "before") || !strings.Contains(body, "after") {
		t.Errorf("a second process truncated the log:\n%s", body)
	}
}

// The log holds session ids, branch names and paths, so it is the user's own
// like everything else opentree writes.
func TestInit_LogIsPrivate(t *testing.T) {
	reset(t)
	path := filepath.Join(t.TempDir(), "opentree.log")
	t.Setenv(EnvVar, path)

	Init("test")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(): %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("log mode = %v, want 0600", perm)
	}
}

// A path that cannot be opened must not stop the program: this is the code
// that runs when something is already going wrong.
func TestInit_AnUnopenableLogIsNotFatal(t *testing.T) {
	reset(t)
	t.Setenv(EnvVar, filepath.Join(t.TempDir(), "no", "such", "directory", "x.log"))

	Init("test")
	Log("test", "still fine")

	if Enabled() {
		t.Error("Enabled() = true for a path that could not be opened")
	}
}

// reset closes whatever a previous test opened, since the log is package state
// by design — every process has one.
func reset(t *testing.T) {
	t.Helper()
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		_ = file.Close()
	}
	file, path = nil, ""
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	return string(b)
}
