package chat

import (
	"encoding/base64"
	"os"
	"path/filepath"
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

// compose is composePrompt for the cases that only care about the blocks, with
// an agent that takes images.
func compose(text, cwd string, files map[string]bool) []acp.ContentBlock {
	blocks, _ := composer{cwd: cwd, known: files, images: true}.prompt(text, nil)
	return blocks
}

// onePixelPNG is the smallest thing http.DetectContentType will call an image.
var onePixelPNG = []byte{
	0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A,
	0x00, 0x00, 0x00, 0x0D, 'I', 'H', 'D', 'R',
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1F, 0x15, 0xC4, 0x89,
}

// writeFile drops a file in a temp dir and returns its path.
func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
	return path
}

// ---------------------------------------------------------------------------
// composePrompt
// ---------------------------------------------------------------------------

func TestComposePrompt_PlainText(t *testing.T) {
	blocks := compose("just words", "/repo", known())
	if len(blocks) != 1 || blocks[0].Type != "text" || blocks[0].Text != "just words" {
		t.Errorf("blocks = %+v, want a single text block", blocks)
	}
}

func TestComposePrompt_MentionBecomesResourceLink(t *testing.T) {
	blocks := compose("look at @main.go please", "/repo", known("main.go"))
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
	blocks := compose("email @someone about it", "/repo", known("main.go"))
	if len(blocks) != 1 || blocks[0].Type != "text" {
		t.Fatalf("blocks = %+v, want one text block", blocks)
	}
	if blocks[0].Text != "email @someone about it" {
		t.Errorf("text = %q, want it unchanged", blocks[0].Text)
	}
}

func TestComposePrompt_MidWordAtIsNotAMention(t *testing.T) {
	blocks := compose("mail me@main.go now", "/repo", known("main.go"))
	if len(blocks) != 1 {
		t.Fatalf("blocks = %+v, want one text block; an email is not a mention", blocks)
	}
}

func TestComposePrompt_MultipleMentions(t *testing.T) {
	blocks := compose("@main.go and @README.md", "/repo", known("main.go", "README.md"))
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
	blocks := compose("@main.go", "/repo", known("main.go"))
	if len(blocks) != 1 || blocks[0].Type != "resource_link" {
		t.Errorf("blocks = %+v, want a single resource link", blocks)
	}
}

// ---------------------------------------------------------------------------
// Attachments
// ---------------------------------------------------------------------------

func TestComposePrompt_ImagePathBecomesAnImageBlock(t *testing.T) {
	path := writeFile(t, "shot.png", onePixelPNG)

	blocks := compose("what is wrong with "+path, "/repo", known())
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v, want text then image", blocks)
	}
	img := blocks[1]
	if img.Type != acp.BlockImage {
		t.Fatalf("second block = %+v, want an image", img)
	}
	if img.MimeType != "image/png" {
		t.Errorf("MimeType = %q, want image/png", img.MimeType)
	}
	// The bytes have to survive the trip, not merely be present: an agent given
	// a truncated base64 payload reports an image it cannot open.
	data, err := base64.StdEncoding.DecodeString(img.Data)
	if err != nil {
		t.Fatalf("Data is not base64: %v", err)
	}
	if string(data) != string(onePixelPNG) {
		t.Errorf("decoded %d bytes, want the %d written", len(data), len(onePixelPNG))
	}
}

func TestComposePrompt_ImageFallsBackToALinkWhenTheAgentCannotTakeOne(t *testing.T) {
	path := writeFile(t, "shot.png", onePixelPNG)

	blocks, notices := composer{cwd: "/repo", known: known(), images: false}.prompt(path, nil)
	if len(blocks) != 1 || blocks[0].Type != acp.BlockResourceLink {
		t.Fatalf("blocks = %+v, want a single resource link", blocks)
	}
	// Silence would be the worst outcome: an image that quietly went as a link
	// looks exactly like one that did not.
	if len(notices) != 1 || !strings.Contains(notices[0], "does not take images") {
		t.Errorf("notices = %q, want one saying why", notices)
	}
}

func TestComposePrompt_OversizeImageFallsBackToALink(t *testing.T) {
	big := append(append([]byte{}, onePixelPNG...), make([]byte, maxImageBytes)...)
	path := writeFile(t, "huge.png", big)

	blocks, notices := composer{cwd: "/repo", known: known(), images: true}.prompt(path, nil)
	if len(blocks) != 1 || blocks[0].Type != acp.BlockResourceLink {
		t.Fatalf("blocks = %+v, want a single resource link", blocks)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "too large") {
		t.Errorf("notices = %q, want one saying it was too large", notices)
	}
}

func TestComposePrompt_NonImageFileBecomesALinkWithNoNotice(t *testing.T) {
	path := writeFile(t, "notes.txt", []byte("plain words"))

	blocks, notices := composer{cwd: "/repo", known: known(), images: true}.prompt(path, nil)
	if len(blocks) != 1 || blocks[0].Type != acp.BlockResourceLink {
		t.Fatalf("blocks = %+v, want a single resource link", blocks)
	}
	// A link is what a text file was always going to be, so there is nothing
	// to report.
	if len(notices) != 0 {
		t.Errorf("notices = %q, want none", notices)
	}
}

func TestComposePrompt_EscapedSpacesInADraggedPathSurvive(t *testing.T) {
	path := writeFile(t, "Screenshot 2026-08-10.png", onePixelPNG)

	// This is what a terminal inserts when a file is dragged onto it.
	blocks := compose(strings.ReplaceAll(path, " ", `\ `), "/repo", known())
	if len(blocks) != 1 || blocks[0].Type != acp.BlockImage {
		t.Fatalf("blocks = %+v, want a single image; the escape split the word", blocks)
	}
}

func TestComposePrompt_BareWordIsNotAPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A word with no separator stays prose even when a file by that name is
	// sitting right there.
	blocks := compose("check the README first", dir, known())
	if len(blocks) != 1 || blocks[0].Type != acp.BlockText {
		t.Errorf("blocks = %+v, want one text block", blocks)
	}
}

func TestComposePrompt_MissingPathStaysText(t *testing.T) {
	blocks := compose("see ./nope/missing.png", t.TempDir(), known())
	if len(blocks) != 1 || blocks[0].Type != acp.BlockText {
		t.Errorf("blocks = %+v, want it left as typed", blocks)
	}
}

func TestComposePrompt_TrackedFileNeedsNoDisk(t *testing.T) {
	// The palette offers whatever git listed, and a mention of one has to travel
	// whether or not the file survived to the moment of sending.
	blocks := compose("@pkg/auth/session.go", "/nowhere", known("pkg/auth/session.go"))
	if len(blocks) != 1 || blocks[0].Type != acp.BlockResourceLink {
		t.Fatalf("blocks = %+v, want a single resource link", blocks)
	}
	if blocks[0].Name != "pkg/auth/session.go" {
		t.Errorf("Name = %q, want the path as typed", blocks[0].Name)
	}
}

// ---------------------------------------------------------------------------
// Blocks back to text
// ---------------------------------------------------------------------------

func TestEcho_ReadsAsTheMessageWasTyped(t *testing.T) {
	blocks := []acp.ContentBlock{
		acp.TextBlock("look at "),
		acp.ResourceLink("file:///repo/main.go", "main.go"),
		acp.TextBlock(" please"),
	}
	if got := echo(blocks); got != "look at @main.go please" {
		t.Errorf("echo = %q, want the sigil put back and no invented breaks", got)
	}
}

func TestEcho_SeparatesAPastedImageFromTheMessage(t *testing.T) {
	blocks := []acp.ContentBlock{
		acp.ImageBlock(base64.StdEncoding.EncodeToString(onePixelPNG), "image/png"),
		acp.TextBlock("what is this?"),
	}
	got := echo(blocks)
	if !strings.HasPrefix(got, "[image") {
		t.Fatalf("echo = %q, want it to open with the image", got)
	}
	if !strings.Contains(got, "\nwhat is this?") {
		t.Errorf("echo = %q, want the message on its own line", got)
	}
}

func TestBlockText_NamesTheFileAnImageCameFrom(t *testing.T) {
	path := writeFile(t, "shot.png", onePixelPNG)
	blocks := compose(path, "/repo", known())

	got := blockText(blocks[0])
	if !strings.Contains(got, "shot.png") {
		t.Errorf("blockText = %q, want it to name the file", got)
	}
}

func TestBlockText_SaysNothingForABlockItCannotRender(t *testing.T) {
	// Empty is the signal to draw no entry at all. An audio block rendered as a
	// blank leaves a coloured bullet on a line of its own.
	if got := blockText(acp.ContentBlock{Type: "audio", Data: "…"}); got != "" {
		t.Errorf("blockText = %q, want empty", got)
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

func TestMatchFiles_KeepsEveryMatch(t *testing.T) {
	var many []string
	for i := 0; i < 50; i++ {
		many = append(many, "file")
	}
	// The palette scrolls, so matching stops filtering and keeps the lot.
	if items := matchFiles("f", many); len(items) != len(many) {
		t.Errorf("items = %d, want all %d matches", len(items), len(many))
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

// An agent sends its commands after the session opens, and the first "/" is
// often typed before they land — a palette that keeps its opening snapshot
// shows opentree's own commands and nothing else until the whole token is
// retyped.
func TestPalette_TakesCommandsThatArriveWhileOpen(t *testing.T) {
	m := newTestModel()
	m = typeInto(m, "/")
	m, _ = applyUpdate(m, acpUpdateMsg{
		Type:     acp.UpdateCommands,
		Commands: []acp.Command{{Name: "compact"}, {Name: "init"}},
	})

	if got := len(m.completion.items); got != 2 {
		t.Errorf("items = %d, want the 2 commands that just arrived", got)
	}
}

// Until they land, a palette holding only opentree's own commands looks like
// the whole list, so it says what it is still waiting for.
func TestPalette_SaysItIsStillWaitingForCommands(t *testing.T) {
	m := typeInto(newTestModel(), "/")
	if !strings.Contains(m.completionView(), "asking") {
		t.Errorf("completionView() = %q, want it to say the commands are still coming", m.completionView())
	}
	if got, want := m.completionHeight(), len(m.completion.items)+1; got != want {
		t.Errorf("completionHeight = %d, want %d — the footer has to fit the waiting line", got, want)
	}

	m, _ = applyUpdate(m, acpUpdateMsg{
		Type:     acp.UpdateCommands,
		Commands: []acp.Command{{Name: "compact"}},
	})
	if strings.Contains(m.completionView(), "asking") {
		t.Error("the waiting line must go once the commands are in")
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

	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	log := m.renderLog()
	if strings.Contains(log, "reasoning aloud") {
		t.Error("ctrl+o should hide reasoning")
	}
	if !strings.Contains(log, "the answer") {
		t.Error("hiding reasoning must not hide the reply")
	}

	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlO})
	if !strings.Contains(m.renderLog(), "reasoning aloud") {
		t.Error("ctrl+o should bring reasoning back")
	}
}
