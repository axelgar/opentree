package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// twoFileDiff is what DiffCombined hands over for a rename with one hunk and
// a new file, the committed half alone so it is unlabelled.
const twoFileDiff = `diff --git a/pkg/old.go b/pkg/new.go
similarity index 90%
rename from pkg/old.go
rename to pkg/new.go
index 1111111..2222222 100644
--- a/pkg/old.go
+++ b/pkg/new.go
@@ -10,3 +12,4 @@ func f() {
 	a := 1
-	b := 2
+	b := 3
+	c := 4
 	return
diff --git a/README.md b/README.md
new file mode 100644
index 0000000..3333333
--- /dev/null
+++ b/README.md
@@ -0,0 +1,2 @@
+# Title
+text`

func TestParseDiff_FilesHunksAndLineNumbers(t *testing.T) {
	rows, files, gutter := parseDiff(twoFileDiff)

	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}
	f := files[0]
	if f.path != "pkg/new.go" || f.oldPath != "pkg/old.go" {
		t.Errorf("rename parsed as %q ← %q", f.path, f.oldPath)
	}
	if f.added != 2 || f.removed != 1 {
		t.Errorf("counts = +%d -%d, want +2 -1", f.added, f.removed)
	}
	if f.first != 0 || f.last != 12 {
		t.Errorf("row range = %d..%d, want 0..12", f.first, f.last)
	}
	if g := files[1]; g.path != "README.md" || g.oldPath != "" || g.added != 2 || g.first != 13 || g.last != len(rows)-1 {
		t.Errorf("second file = %+v", g)
	}

	// The hunk seeds the counters; each side counts only its own lines.
	want := []struct {
		kind       byte
		old, new   int
		text, file string
	}{
		{rowContext, 10, 12, "\ta := 1", "pkg/new.go"},
		{rowDel, 11, 0, "\tb := 2", "pkg/new.go"},
		{rowAdd, 0, 13, "\tb := 3", "pkg/new.go"},
		{rowAdd, 0, 14, "\tc := 4", "pkg/new.go"},
		{rowContext, 12, 15, "\treturn", "pkg/new.go"},
	}
	for i, w := range want {
		r := rows[8+i]
		if r.kind != w.kind || r.oldNo != w.old || r.newNo != w.new || r.text != w.text || files[r.file].path != w.file {
			t.Errorf("row %d = %+v, want %+v", 8+i, r, w)
		}
	}
	if rows[7].kind != rowHunk || rows[0].kind != rowFile || rows[5].kind != rowMeta {
		t.Errorf("structure rows mistyped: %c %c %c", rows[0].kind, rows[5].kind, rows[7].kind)
	}
	if gutter != 2 {
		t.Errorf("gutter = %d, want 2 (widest number is 15)", gutter)
	}
}

func TestParseDiff_SectionsFromCombinedHeaders(t *testing.T) {
	content := "══════ Committed Changes ══════\n\n" + twoFileDiff +
		"\n\n══════ Uncommitted Changes ══════\n\ndiff --git a/x b/x\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b"
	rows, files, _ := parseDiff(content)
	if rows[0].kind != rowSection || rows[1].kind != rowText {
		t.Errorf("header and its air = %c %c", rows[0].kind, rows[1].kind)
	}
	if len(files) != 3 {
		t.Fatalf("files = %d, want 3", len(files))
	}
	if files[0].section != "Committed Changes" || files[2].section != "Uncommitted Changes" {
		t.Errorf("sections = %q %q", files[0].section, files[2].section)
	}

	// A fan-out header is a section too.
	_, files, _ = parseDiff(buildGroupDiff([]groupDiffSection{
		{name: "feat/x-claude", agent: "claude", content: twoFileDiff},
		{name: "feat/x-gemini", agent: "gemini", content: "No changes."},
	}))
	if len(files) != 2 || files[0].section != "feat/x-claude (claude)" {
		t.Errorf("group sections = %+v", files)
	}
}

func TestParseDiff_BinaryAndDeletedFile(t *testing.T) {
	rows, files, _ := parseDiff("diff --git a/img.png b/img.png\nBinary files a/img.png and b/img.png differ\n" +
		"diff --git a/gone.txt b/gone.txt\ndeleted file mode 100644\n--- a/gone.txt\n+++ /dev/null\n@@ -1,2 +0,0 @@\n-one\n-two")
	if !files[0].binary || files[0].path != "img.png" {
		t.Errorf("binary file = %+v", files[0])
	}
	if files[1].removed != 2 || files[1].added != 0 || rows[len(rows)-1].oldNo != 2 {
		t.Errorf("deleted file = %+v, last row %+v", files[1], rows[len(rows)-1])
	}
}

func TestParseDiff_NoChangesIsAPlainRow(t *testing.T) {
	rows, files, gutter := parseDiff("No changes.")
	if len(rows) != 1 || rows[0].kind != rowText || rows[0].file != -1 || len(files) != 0 || gutter != 1 {
		t.Errorf("rows = %+v files = %+v gutter = %d", rows, files, gutter)
	}
}

// The painted rows keep their text, sign included, and a header its whole line.
func TestPaintRow_KeepsTheText(t *testing.T) {
	m := newTestModel()
	m.diff = newDiffView("══════ Committed Changes ══════\n"+twoFileDiff, "a")
	view := m.View()
	for _, want := range []string{"══════ Committed Changes ══════", "diff --git a/pkg/old.go b/pkg/new.go",
		"@@ -10,3 +12,4 @@ func f() {", "-    b := 2", "+    b := 3", "     a := 1", "▎"} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q\n%s", want, view)
		}
	}
}

func TestDiffView_CursorDragsTheWindow(t *testing.T) {
	m := newTestModel()
	m.diff = newDiffView(fiftyLines(), "a")
	page := m.diffPage()

	// Down to the last visible row moves nothing; one more pulls the window.
	for range page - 1 {
		m, _ = applyUpdate(m, keyMsg("j"))
	}
	if m.diff.offset != 0 {
		t.Fatalf("offset = %d before the cursor left the screen", m.diff.offset)
	}
	m, _ = applyUpdate(m, keyMsg("j"))
	if m.diff.cursor != page || m.diff.offset != 1 {
		t.Errorf("cursor/offset = %d/%d, want %d/1", m.diff.cursor, m.diff.offset, page)
	}

	// G puts the cursor on the last row and the window at its limit.
	m, _ = applyUpdate(m, keyMsg("G"))
	if m.diff.cursor != 49 || m.diff.offset != m.maxDiffScroll() {
		t.Errorf("G: cursor/offset = %d/%d", m.diff.cursor, m.diff.offset)
	}

	// The wheel moves the window and drags the cursor into it.
	m, _ = applyUpdate(m, tea.MouseMsg{Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp})
	if m.diff.offset != m.maxDiffScroll()-wheelLines {
		t.Errorf("wheel: offset = %d", m.diff.offset)
	}
	if last := m.diff.offset + page - 1; m.diff.cursor != last {
		t.Errorf("wheel: cursor = %d, want it pulled to the last visible row %d", m.diff.cursor, last)
	}
	if !strings.Contains(m.View(), "line 47/50") {
		t.Errorf("footer does not show the cursor line:\n%s", m.View())
	}
}
