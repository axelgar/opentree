package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
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

// --- tree and navigation ---------------------------------------------------

func TestDiffTree_ListsFilesWithCounts(t *testing.T) {
	m := newTestModel()
	m.diff = newDiffView(twoFileDiff, "a")
	view := m.View()
	for _, want := range []string{"pkg/new.go", "+2 -1", "README.md", "+2 -0", "│"} {
		if !strings.Contains(view, want) {
			t.Errorf("tree lacks %q\n%s", want, view)
		}
	}
	// t hides it, and a narrow terminal has no room for it.
	m, _ = applyUpdate(m, keyMsg("t"))
	if strings.Contains(m.View(), "+2 -1") {
		t.Error("t did not hide the tree")
	}
	m, _ = applyUpdate(m, keyMsg("t"))
	m.width = diffTreeMinWidth - 1
	if strings.Contains(m.View(), "+2 -1") {
		t.Error("the tree is drawn on a terminal too narrow for it")
	}
}

func TestDiffTree_HiddenForOneFile(t *testing.T) {
	m := newTestModel()
	one, _, _ := strings.Cut(twoFileDiff, "diff --git a/README.md")
	m.diff = newDiffView(one, "a")
	if strings.Contains(m.View(), "+2 -1") {
		t.Error("a single file has a tree; the code should have the width")
	}
}

func TestDiffKeys_HunkAndFileJumps(t *testing.T) {
	m := newTestModel()
	m.diff = newDiffView(fiftyLines()+"\n"+twoFileDiff, "a") // the diff starts at row 50
	m, _ = applyUpdate(m, keyMsg("]"))
	if m.diff.cursor != 57 || m.diff.offset != m.maxDiffScroll() {
		t.Errorf("] landed on %d/%d, want the first hunk row 57 at the window's limit", m.diff.cursor, m.diff.offset)
	}
	m, _ = applyUpdate(m, keyMsg("]"))
	if m.diff.cursor != 68 {
		t.Errorf("second ] landed on %d, want 68", m.diff.cursor)
	}
	m, _ = applyUpdate(m, keyMsg("]"))
	if m.diff.cursor != 68 {
		t.Errorf("] past the last hunk moved to %d", m.diff.cursor)
	}
	m, _ = applyUpdate(m, keyMsg("["))
	if m.diff.cursor != 57 {
		t.Errorf("[ landed on %d, want 57", m.diff.cursor)
	}
	m, _ = applyUpdate(m, keyMsg("g"))
	m, _ = applyUpdate(m, keyMsg("n"))
	if m.diff.cursor != 50 {
		t.Errorf("n landed on %d, want the first file header 50", m.diff.cursor)
	}
	m, _ = applyUpdate(m, keyMsg("n"))
	if m.diff.cursor != 63 {
		t.Errorf("second n landed on %d, want 63", m.diff.cursor)
	}
	m, _ = applyUpdate(m, keyMsg("p"))
	if m.diff.cursor != 50 {
		t.Errorf("p landed on %d, want 50", m.diff.cursor)
	}
}

func TestDiffKeys_SpaceMarksReviewed(t *testing.T) {
	m := newTestModel()
	m.diff = newDiffView(twoFileDiff, "a")
	if strings.Contains(m.View(), "✓") {
		t.Fatal("a fresh diff has a reviewed mark")
	}
	m, _ = applyUpdate(m, keyMsg("n")) // onto README.md, so the tick is not the cursor's file
	m, _ = applyUpdate(m, keyMsg("p"))
	m, _ = applyUpdate(m, keyMsg("j"))
	m, _ = applyUpdate(m, keyMsg("n"))
	m, _ = applyUpdate(m, keyMsg(" "))
	m, _ = applyUpdate(m, keyMsg("p"))
	if !strings.Contains(m.View(), "✓ README.md") {
		t.Errorf("space did not tick the file:\n%s", m.View())
	}
	if m.diff.offset != 0 {
		t.Error("space paged; it should only mark")
	}
	m, _ = applyUpdate(m, keyMsg("n"))
	m, _ = applyUpdate(m, keyMsg(" "))
	if strings.Contains(m.View(), "✓") {
		t.Error("a second space did not untick")
	}
}

func TestDiffClick_TreeJumpsBodySelects(t *testing.T) {
	m := newTestModel()
	m.diff = newDiffView(twoFileDiff, "a")
	top := appStyle.GetPaddingTop() + 2

	// The tree's second line is README.md (no section heading, so no offset).
	m, _ = applyUpdate(m, tea.MouseMsg{X: 4, Y: top + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.diff.cursor != 13 {
		t.Errorf("a click on the second file put the cursor on %d, want 13", m.diff.cursor)
	}
	m, _ = applyUpdate(m, keyMsg("g"))
	m, _ = applyUpdate(m, tea.MouseMsg{X: 60, Y: top + 5, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if m.diff.cursor != 5 {
		t.Errorf("a click on the sixth body line put the cursor on %d, want 5", m.diff.cursor)
	}
	if m.cursor != 0 {
		t.Error("a click in the diff moved the list behind it")
	}
}

func TestGroupDiff_TreeGroupsFilesBySibling(t *testing.T) {
	m := newTestModel()
	m.diff = newDiffView(buildGroupDiff([]groupDiffSection{
		{name: "feat/x-claude", agent: "claude", content: twoFileDiff},
		{name: "feat/x-gemini", agent: "gemini", content: twoFileDiff},
	}), "feat/x · 2 siblings")
	lines := m.treeLines()
	files := make([]int, len(lines))
	for i, l := range lines {
		files[i] = l.file
	}
	if want := []int{-1, 0, 1, -1, 2, 3}; fmt.Sprint(files) != fmt.Sprint(want) {
		t.Errorf("tree lines = %v, want a heading before each sibling's files %v", files, want)
	}
	if !strings.Contains(lines[3].text, "feat/x-gemini (gemini)") {
		t.Errorf("second heading = %q", lines[3].text)
	}
}

func TestDiffView_HelpCardNamesEveryKey(t *testing.T) {
	m := newTestModel()
	m.diff = newDiffView(twoFileDiff, "a")
	m, _ = applyUpdate(m, keyMsg("?"))
	view := m.View()
	for _, k := range diffKeys {
		if !strings.Contains(view, k[0]) || !strings.Contains(view, k[1]) {
			t.Errorf("card lacks %v", k)
		}
	}
	m, _ = applyUpdate(m, keyMsg("j"))
	if m.diff.help || m.diff.cursor != 0 {
		t.Error("the first key after the card should close it and do nothing else")
	}
}

// --- width -------------------------------------------------------------------

func TestDiffView_TruncatesToWidth(t *testing.T) {
	m := newTestModel()
	long := strings.Repeat("x", 300)
	m.diff = newDiffView(twoFileDiff+"\n+"+long, "a")
	for i, line := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("line %d is %d wide in a %d-wide terminal", i, w, m.width)
		}
	}
	if !strings.Contains(m.View(), "…") {
		t.Error("the cut line has no ellipsis")
	}
}

func TestDiffKeys_WrapShowsTheWholeLine(t *testing.T) {
	m := newTestModel()
	long := strings.Repeat("y", 200) + "END"
	m.diff = newDiffView("diff --git a/f b/f\n@@ -1 +1 @@\n+"+long, "a")
	if strings.Contains(m.View(), "END") {
		t.Fatal("the tail of a long line is on screen before w")
	}
	m, _ = applyUpdate(m, keyMsg("w"))
	view := m.View()
	if !strings.Contains(view, "END") {
		t.Errorf("w did not wrap the line:\n%s", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("wrapped line %d is %d wide", i, w)
		}
	}
	// The cursor stays visible when the rows above it wrap.
	m.diff = newDiffView(strings.Repeat("diff --git a/f b/f\n@@ -1 +1 @@\n+"+long+"\n", 40), "a")
	m.diff.wrap = true
	m, _ = applyUpdate(m, keyMsg("G"))
	if last := m.lastVisibleRow(); last != m.diff.cursor {
		t.Errorf("after G under wrap the last visible row is %d, cursor %d", last, m.diff.cursor)
	}
}

func TestDiffKeys_LineNumbersToggle(t *testing.T) {
	m := newTestModel()
	m.diff = newDiffView(twoFileDiff, "a")
	m.diff.tree = false
	if strings.Contains(m.View(), "10 12") {
		t.Fatal("line numbers are on before L")
	}
	m, _ = applyUpdate(m, keyMsg("L"))
	view := m.View()
	// Context has both numbers, a removal only the old, an addition only the new.
	for _, want := range []string{"10 12      a := 1", "11    -    b := 2", "   13 +    b := 3"} {
		if !strings.Contains(view, want) {
			t.Errorf("gutter lacks %q\n%s", want, view)
		}
	}
	m, _ = applyUpdate(m, keyMsg("L"))
	if strings.Contains(m.View(), "10 12") {
		t.Error("a second L did not hide the numbers")
	}
}

// --- search --------------------------------------------------------------------

func TestDiffSearch_NStepsMatchesWhileQueryLives(t *testing.T) {
	m := newTestModel()
	m.diff = newDiffView(twoFileDiff, "a")
	m, _ = applyUpdate(m, keyMsg("/"))
	for _, r := range "b :=" {
		m, _ = applyUpdate(m, keyMsg(string(r)))
	}
	if len(m.diff.matches) != 2 || m.diff.cursor != 9 {
		t.Fatalf("matches = %+v cursor = %d, want two matches and the cursor on the first, row 9", m.diff.matches, m.diff.cursor)
	}
	if !strings.Contains(m.View(), "1 of 2") {
		t.Errorf("the box does not count the matches:\n%s", m.View())
	}
	m, _ = applyUpdate(m, keyMsg("enter"))
	if m.diff.searching || m.diff.query != "b :=" {
		t.Fatal("enter should close the box and keep the query")
	}
	m, _ = applyUpdate(m, keyMsg("n"))
	if m.diff.cursor != 10 {
		t.Errorf("n moved to %d, want the second match, 10", m.diff.cursor)
	}
	m, _ = applyUpdate(m, keyMsg("n"))
	if m.diff.cursor != 9 {
		t.Errorf("n at the last match moved to %d, want to wrap to 9", m.diff.cursor)
	}
	m, _ = applyUpdate(m, keyMsg("N"))
	if m.diff.cursor != 10 {
		t.Errorf("N moved to %d, want 10", m.diff.cursor)
	}
	if !strings.Contains(m.View(), "≋ b := · 2 of 2") {
		t.Errorf("the footer does not show the live query:\n%s", m.View())
	}

	// esc clears the query and n goes back to stepping files; a second esc closes.
	m, _ = applyUpdate(m, keyMsg("esc"))
	if !m.diff.open || m.diff.query != "" {
		t.Fatal("esc with a query should clear it, not close")
	}
	m, _ = applyUpdate(m, keyMsg("n"))
	if m.diff.cursor != 13 {
		t.Errorf("n without a query moved to %d, want the next file, 13", m.diff.cursor)
	}
	m, _ = applyUpdate(m, keyMsg("esc"))
	if m.diff.open {
		t.Error("esc without a query should close")
	}
}

func TestDiffSearch_PaintsTheMatchOnTheRow(t *testing.T) {
	before := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(before) })

	m := newTestModel()
	m.diff = newDiffView(twoFileDiff, "a")
	m.diff.tree = false
	m, _ = applyUpdate(m, keyMsg("/"))
	m, _ = applyUpdate(m, keyMsg("2"))
	// The first 2 is in the index line, row 4: that one is current, in
	// inverse; the one in "b := 2" on row 9 is underlined.
	if m.diff.cursor != 4 {
		t.Fatalf("cursor = %d, want the first match's row 4", m.diff.cursor)
	}
	if line := m.paintRow(4); !strings.Contains(line, "\x1b[7m2") {
		t.Errorf("the current match is not painted in inverse: %q", line)
	}
	line := m.paintRow(9) // "-\tb := 2"
	if !strings.Contains(line, "\x1b[4;") || !strings.Contains(ansi.Strip(line), "2") {
		t.Errorf("the other match is not underlined: %q", line)
	}
	if got := ansi.Strip(line); !strings.HasSuffix(got, "-    b := 2") {
		t.Errorf("painting changed the text: %q", got)
	}
}
