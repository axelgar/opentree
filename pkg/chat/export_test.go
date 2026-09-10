package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/axelgar/opentree/pkg/acp"
)

func TestExport_WritesTheConversationAndSaysWhere(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "exports")
	m := newTestModel()
	m.opts.Exports = dir
	m.entries = append(m.entries, entry{kind: entryUser, text: "add a test"})
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "Adding one."))
	m, _ = applyUpdate(m, toolUpdate(acp.UpdateToolCall, outputCall(acp.StatusCompleted, "ok")))

	run, ok := m.clientCommandFor("/export")
	if !ok {
		t.Fatal("/export is not offered with a conversation to export")
	}
	next, cmd := run(m)
	if cmd == nil {
		t.Fatal("/export produced no write")
	}
	m, _ = applyUpdate(next.(Model), cmd())

	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("exports = %v, %v; want one file", files, err)
	}
	if !strings.HasPrefix(files[0].Name(), "fix-auth-") || !strings.HasSuffix(files[0].Name(), ".md") {
		t.Errorf("file = %q, want fix-auth-<time>.md", files[0].Name())
	}
	data, err := os.ReadFile(filepath.Join(dir, files[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# fix-auth — OpenCode", "## You\n\nadd a test", "Adding one.", "**✓ go test ./...**"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("the export is missing %q:\n%s", want, data)
		}
	}
	if !strings.Contains(m.renderLog(), "exported to ") {
		t.Errorf("the log does not say where the file went:\n%s", m.renderLog())
	}
}

func TestExport_IsNotOfferedWithNothingToExport(t *testing.T) {
	m := newTestModel()
	m.opts.Exports = t.TempDir()
	if _, ok := m.clientCommandFor("/export"); ok {
		t.Error("/export is offered for an empty conversation")
	}
	m.opts.Exports = ""
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "hi"))
	if _, ok := m.clientCommandFor("/export"); ok {
		t.Error("/export is offered with nowhere to write")
	}
}

func TestExportsDir_KeysTheRepositoryLikeTheSockets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got := ExportsDir("/src/myapp")
	want := filepath.Join(home, ".opentree", "exports", filepath.Base(filepath.Dir(SocketPath("/src/myapp", "x"))))
	if got != want {
		t.Errorf("ExportsDir = %q, want %q", got, want)
	}
}
