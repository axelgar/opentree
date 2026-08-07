package chat

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
)

var trackedFiles = []string{"main.go", "pkg/auth/session.go", "pkg/auth/token.go", "README.md"}

func known(paths ...string) map[string]bool {
	m := make(map[string]bool, len(paths))
	for _, p := range paths {
		m[p] = true
	}
	return m
}

// ---------------------------------------------------------------------------
// composePrompt
// ---------------------------------------------------------------------------

func TestComposePrompt_PlainText(t *testing.T) {
	blocks := composePrompt("just words", "/repo", known())
	if len(blocks) != 1 || blocks[0].Type != "text" || blocks[0].Text != "just words" {
		t.Errorf("blocks = %+v, want a single text block", blocks)
	}
}

func TestComposePrompt_MentionBecomesResourceLink(t *testing.T) {
	blocks := composePrompt("look at @main.go please", "/repo", known("main.go"))
	if len(blocks) != 3 {
		t.Fatalf("blocks = %+v, want text, link, text", blocks)
	}
	if blocks[0].Text != "look at " {
		t.Errorf("leading text = %q, want %q", blocks[0].Text, "look at ")
	}
	if blocks[1].Type != "resource_link" {
		t.Fatalf("middle block = %+v, want a resource_link", blocks[1])
	}
	if blocks[1].Name != "main.go" {
		t.Errorf("Name = %q, want main.go", blocks[1].Name)
	}
	if blocks[1].URI != "file:///repo/main.go" {
		t.Errorf("URI = %q, want file:///repo/main.go", blocks[1].URI)
	}
	if blocks[2].Text != " please" {
		t.Errorf("trailing text = %q, want %q", blocks[2].Text, " please")
	}
}

func TestComposePrompt_UnknownMentionStaysText(t *testing.T) {
	// A stray @ in prose must not turn into a broken link.
	blocks := composePrompt("email @someone about it", "/repo", known("main.go"))
	if len(blocks) != 1 || blocks[0].Type != "text" {
		t.Fatalf("blocks = %+v, want one text block", blocks)
	}
	if blocks[0].Text != "email @someone about it" {
		t.Errorf("text = %q, want it unchanged", blocks[0].Text)
	}
}

func TestComposePrompt_MidWordAtIsNotAMention(t *testing.T) {
	blocks := composePrompt("mail me@main.go now", "/repo", known("main.go"))
	if len(blocks) != 1 {
		t.Fatalf("blocks = %+v, want one text block; an email is not a mention", blocks)
	}
}

func TestComposePrompt_MultipleMentions(t *testing.T) {
	blocks := composePrompt("@main.go and @README.md", "/repo", known("main.go", "README.md"))
	links := 0
	for _, b := range blocks {
		if b.Type == "resource_link" {
			links++
		}
	}
	if links != 2 {
		t.Errorf("resource links = %d, want 2", links)
	}
}

func TestComposePrompt_OnlyAMention(t *testing.T) {
	blocks := composePrompt("@main.go", "/repo", known("main.go"))
	if len(blocks) != 1 || blocks[0].Type != "resource_link" {
		t.Errorf("blocks = %+v, want a single resource link", blocks)
	}
}

// ---------------------------------------------------------------------------
// Completion
// ---------------------------------------------------------------------------

var testCommands = []acp.Command{
	{Name: "compact", Description: "Compact the conversation"},
	{Name: "commit", Description: "Commit the changes\nsecond line"},
	{Name: "undo", Description: "Undo the last edit"},
}

func TestCompletionFor(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantKind  completionKind
		wantFirst string
	}{
		{"empty", "", completionNone, ""},
		{"plain word", "hello", completionNone, ""},
		{"trailing space closes it", "/com ", completionNone, ""},
		{"slash opens commands", "/com", completionCommand, "/compact"},
		{"bare slash lists all", "/", completionCommand, "/compact"},
		{"slash mid-message is not a command", "run /com", completionNone, ""},
		{"at opens files", "@main", completionFile, "@main.go"},
		{"at mid-message still completes", "look at @main", completionFile, "@main.go"},
		{"file substring match", "@session", completionFile, "@pkg/auth/session.go"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := completionFor(tt.input, testCommands, trackedFiles)
			if got.kind != tt.wantKind {
				t.Fatalf("kind = %v, want %v", got.kind, tt.wantKind)
			}
			if tt.wantFirst == "" {
				return
			}
			if len(got.items) == 0 {
				t.Fatalf("no items for %q", tt.input)
			}
			if got.items[0].value != tt.wantFirst {
				t.Errorf("first item = %q, want %q", got.items[0].value, tt.wantFirst)
			}
		})
	}
}

func TestCompletionFor_UnmatchedIsInactive(t *testing.T) {
	got := completionFor("/zzz", testCommands, trackedFiles)
	if got.active() {
		t.Errorf("expected no active completion, got %+v", got.items)
	}
}

func TestMatchCommands_DescriptionIsOneLine(t *testing.T) {
	items := matchCommands("commit", testCommands)
	if len(items) != 1 {
		t.Fatalf("items = %+v, want 1", items)
	}
	if strings.Contains(items[0].desc, "\n") {
		t.Errorf("desc = %q, want a single line", items[0].desc)
	}
}

func TestMatchFiles_PrefixBeatsSubstring(t *testing.T) {
	items := matchFiles("pkg/auth", trackedFiles)
	if len(items) < 2 {
		t.Fatalf("items = %+v, want both auth files", items)
	}
	if items[0].value != "@pkg/auth/session.go" {
		t.Errorf("first = %q, want the prefix match first", items[0].value)
	}
}

func TestMatchFiles_Capped(t *testing.T) {
	var many []string
	for i := 0; i < 50; i++ {
		many = append(many, "file")
	}
	if got := len(matchFiles("f", many)); got > maxCompletionItems {
		t.Errorf("items = %d, want at most %d", got, maxCompletionItems)
	}
}

func TestApplyCompletion(t *testing.T) {
	tests := []struct {
		input, value, want string
	}{
		{"/com", "/compact", "/compact "},
		{"look at @mai", "@main.go", "look at @main.go "},
		{"@s", "@pkg/auth/session.go", "@pkg/auth/session.go "},
	}
	for _, tt := range tests {
		if got := applyCompletion(tt.input, completionItem{value: tt.value}); got != tt.want {
			t.Errorf("applyCompletion(%q, %q) = %q, want %q", tt.input, tt.value, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Palette in the model
// ---------------------------------------------------------------------------

func newPaletteModel(t *testing.T) Model {
	t.Helper()
	m := newTestModel()
	m.commands = testCommands
	m.files = trackedFiles
	return m
}

func typeInto(m Model, s string) Model {
	for _, r := range s {
		m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	return m
}

func TestPalette_OpensWhileTyping(t *testing.T) {
	m := typeInto(newPaletteModel(t), "/com")
	if !m.completion.active() {
		t.Fatal("expected the command palette to open")
	}
	if m.completion.kind != completionCommand {
		t.Errorf("kind = %v, want a command palette", m.completion.kind)
	}
}

func TestPalette_TabAccepts(t *testing.T) {
	m := typeInto(newPaletteModel(t), "/comp")
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyTab})

	if m.input.Value() != "/compact " {
		t.Errorf("input = %q, want the command completed", m.input.Value())
	}
	if m.completion.active() {
		t.Error("palette should close once accepted")
	}
}

func TestPalette_EnterAcceptsInsteadOfSending(t *testing.T) {
	// Enter is the send key, so the palette has to claim it first or picking a
	// command would fire a half-typed prompt.
	m := typeInto(newPaletteModel(t), "/comp")
	m, _ = applyUpdate(m, keyMsg("enter"))

	if m.turn {
		t.Error("enter must not send while the palette is open")
	}
	if m.input.Value() != "/compact " {
		t.Errorf("input = %q, want the command completed", m.input.Value())
	}
}

func TestPalette_ArrowsMoveSelection(t *testing.T) {
	m := typeInto(newPaletteModel(t), "/com")
	if len(m.completion.items) < 2 {
		t.Fatal("need at least two matches for this test")
	}
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.completion.cursor != 1 {
		t.Errorf("cursor = %d, want 1", m.completion.cursor)
	}
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.completion.cursor != 0 {
		t.Errorf("cursor = %d, want back to 0", m.completion.cursor)
	}
}

func TestPalette_EscDismisses(t *testing.T) {
	m := typeInto(newPaletteModel(t), "/com")
	m, _ = applyUpdate(m, keyMsg("esc"))
	if m.completion.active() {
		t.Error("esc should dismiss the palette")
	}
	if m.input.Value() != "/com" {
		t.Errorf("input = %q, want it left alone", m.input.Value())
	}
}

func TestPalette_NarrowingResetsSelection(t *testing.T) {
	m := typeInto(newPaletteModel(t), "/com")
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyDown})
	m = typeInto(m, "p")
	if m.completion.cursor != 0 {
		t.Errorf("cursor = %d, want the best match reselected as the token narrows", m.completion.cursor)
	}
}

func TestPalette_FileMentionRoundTrip(t *testing.T) {
	m := typeInto(newPaletteModel(t), "look at @sess")
	if !m.completion.active() || m.completion.kind != completionFile {
		t.Fatal("expected the file palette")
	}
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.input.Value() != "look at @pkg/auth/session.go " {
		t.Errorf("input = %q", m.input.Value())
	}
}

func TestPalette_View(t *testing.T) {
	m := typeInto(newPaletteModel(t), "/com")
	view := m.completionView()
	for _, want := range []string{"/compact", "Compact the conversation", "›"} {
		if !strings.Contains(view, want) {
			t.Errorf("completionView() missing %q\ngot:\n%s", want, view)
		}
	}
}

func TestPalette_FooterGrows(t *testing.T) {
	m := newPaletteModel(t)
	base := m.footerHeight()
	m = typeInto(m, "/com")
	if got := m.footerHeight(); got <= base {
		t.Errorf("footerHeight with a palette = %d, want more than %d", got, base)
	}
}

func TestAvailableCommands_AreRecorded(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, acpUpdateMsg(acp.SessionUpdate{
		Type: acp.UpdateCommands, Commands: testCommands,
	}))
	if len(m.commands) != len(testCommands) {
		t.Errorf("commands = %d, want %d", len(m.commands), len(testCommands))
	}
}

func TestFilesLoaded_FeedTheePalette(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, filesLoadedMsg{files: trackedFiles})
	m = typeInto(m, "@main")
	if !m.completion.active() {
		t.Error("expected loaded files to drive @ completion")
	}
}

// ---------------------------------------------------------------------------
// Thoughts
// ---------------------------------------------------------------------------

func TestThoughts_ToggleHidesThem(t *testing.T) {
	m := newTestModel()
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentThought, "reasoning aloud"))
	m, _ = applyUpdate(m, textUpdate(acp.UpdateAgentMessage, "the answer"))

	if !strings.Contains(m.renderLog(), "reasoning aloud") {
		t.Fatal("reasoning should be shown by default")
	}

	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	log := m.renderLog()
	if strings.Contains(log, "reasoning aloud") {
		t.Error("ctrl+t should hide reasoning")
	}
	if !strings.Contains(log, "the answer") {
		t.Error("hiding reasoning must not hide the reply")
	}

	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlT})
	if !strings.Contains(m.renderLog(), "reasoning aloud") {
		t.Error("ctrl+t should bring reasoning back")
	}
}
