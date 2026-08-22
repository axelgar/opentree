package chat

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

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
	if !bytes.Equal(data, onePixelPNG) {
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
		{"slash mid-message completes too", "run /com", completionCommand, "/compact"},
		{"a bare slash mid-message does not", "run /", completionNone, ""},
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
	for range 50 {
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
	return m.withFiles(trackedFiles)
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

// A command is completed anywhere in the message now, so a path opens the
// palette for one too. Somebody typing where a file is has not asked opentree
// to go and ask the agent anything.
func TestPalette_DoesNotWaitOnAPath(t *testing.T) {
	m := typeInto(newTestModel(), "/usr/local")
	if m.awaitingCommands() {
		t.Error("a path must not be reported as waiting on the agent's commands")
	}
	if m = typeInto(newTestModel(), "/u"); !m.awaitingCommands() {
		t.Error("a name that could still become a command should keep the waiting line")
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
// What the composer colours
// ---------------------------------------------------------------------------

func TestNamesCommand(t *testing.T) {
	tests := []struct {
		name, word string
		want       bool
	}{
		{"a command", "/compact", true},
		{"prose", "compact", false},
		{"half typed names nothing yet", "/comp", false},
		{"a name that does not exist", "/zzz", false},
		{"a bare slash", "/", false},
		{"a path that opens with one", "/usr/local/bin", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := namesCommand(tt.word, testCommands); got != tt.want {
				t.Errorf("namesCommand(%q) = %v, want %v", tt.word, got, tt.want)
			}
		})
	}
}

// withColour renders the test's styles for a terminal that has some.
//
// The composer is drawn from its own characters and coloured by stretch. A test
// binary's own profile is Ascii, where lipgloss emits nothing at all — so
// without this every stretch comes out the same and any mistake about which
// runes a stretch covers is invisible.
func withColour(t *testing.T) {
	t.Helper()
	before := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(before) })
}

// opener is the escape sequence a style starts a stretch with.
func opener(s lipgloss.Style) string {
	open, _, _ := strings.Cut(s.Inline(true).Render("x"), "x")
	return open
}

// colouring replays the escapes ahead of each of a composer's first n columns
// and marks what a terminal would actually draw there: a command, a mention,
// the suggestion, the block cursor, or the plain text of the message.
//
// Comparing strings cannot stand in for this. A stretch cut out of a rendered
// row carries the escapes ahead of the cut so it keeps the colours it was drawn
// in, which is how a stretch can hold the accent's opening sequence and still
// be drawn plain.
func colouring(row string, n int) string {
	marks := map[string]byte{
		opener(commandStyle):                      '/',
		opener(mentionStyle):                      '@',
		opener(ghostStyle):                        '~',
		opener(lipgloss.NewStyle().Reverse(true)): 'C',
	}
	var b strings.Builder
	for i := range n {
		mark, cell := byte('.'), ansi.Cut(row, i, i+1)
		for {
			seq := sgrOpen.FindString(cell)
			if seq == "" {
				break
			}
			if m, ok := marks[seq]; ok {
				mark = m
			} else if seq == "\x1b[0m" {
				mark = '.'
			}
			cell = cell[len(seq):]
		}
		b.WriteByte(mark)
	}
	return b.String()
}

var sgrOpen = regexp.MustCompile("^\x1b\\[[0-9;]*m")

// composerRow is one row of a rendered composer, escapes and all.
func composerRow(view string, n int) string {
	rows := strings.Split(view, "\n")
	if n >= len(rows) {
		return ""
	}
	return rows[n]
}

// composed is the composer after typing s, and after moving the cursor back to
// rune cursor when that is not -1.
func composed(t *testing.T, s string, cursor int) Model {
	t.Helper()
	m := typeInto(newPaletteModel(t), s)
	if cursor >= 0 {
		m.input.SetCursor(cursor)
	}
	return m
}

func TestInputView_Colouring(t *testing.T) {
	withColour(t)

	tests := []struct {
		name   string
		typed  string
		cursor int    // -1 leaves the cursor where typing left it
		want   string // one mark per column, from the start of the composer
	}{
		{"a command it opens with", "/compact", -1, "////////C"},
		{"only the command, not its arguments", "/compact everything", -1, "////////...........C"},
		{"a cursor back inside the name", "/compact", 4, "////C///"},
		{"a cursor back on the sigil", "/compact", 0, "C///////"},
		{"a name that does not exist", "/zzz", -1, "....C"},
		{"a command anywhere in the message", "run /compact", -1, "....////////C"},
		{"a command and a mention in one line", "@main.go and /compact", -1, "@@@@@@@@.....////////C"},
		{"a mention that names a file", "read @main.go now", -1, ".....@@@@@@@@....C"},
		{"a mention that names nothing", "read @nope.go now", -1, ".................C"},
		{"a mention mid-word is an address", "mail me@main.go now", -1, "...................C"},
		{"two mentions", "@main.go @README.md", -1, "@@@@@@@@.@@@@@@@@@@C"},
		{"a path with no sigil still travels", "look at pkg/auth/token.go", -1, "........@@@@@@@@@@@@@@@@@C"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := composed(t, tt.typed, tt.cursor)
			if got := colouring(composerRow(m.inputView(), 0), len(tt.want)); got != tt.want {
				t.Errorf("colouring  = %q\n     want  = %q\n     text  = %q",
					got, tt.want, tt.typed)
			}
		})
	}
}

// The paint is colour and nothing else. A stretch that moved a character, or
// left the row a column short, would draw the box around the composer to the
// wrong width.
func TestInputView_KeepsTheComposerItWasGiven(t *testing.T) {
	withColour(t)

	for _, typed := range []string{
		"/compact", "/compact everything", "read @main.go now", "plain prose",
		"@main.go and @README.md, plus a long tail to push this onto a second row",
	} {
		t.Run(typed, func(t *testing.T) {
			m := composed(t, typed, -1)
			if got, want := ansi.Strip(m.inputView()), ansi.Strip(m.input.View()); got != want {
				t.Errorf("painted text = %q, want %q", got, want)
			}
		})
	}
}

// A word can be both: the sigil that summons a command is also the root of an
// absolute path, and known is checked before the disk is, so a file at the name
// of a command is reachable without a fixture.
func TestWordPaints_ACommandBeatsAPath(t *testing.T) {
	m := newPaletteModel(t).withFiles([]string{"/compact"})
	m.commands = testCommands

	marks := m.wordPaints([]rune("/compact"))
	if len(marks) != 1 {
		t.Fatalf("marks = %+v, want one", marks)
	}
	if marks[0].style.Render("x") != commandStyle.Render("x") {
		t.Error("a word that is both a command and a file should be painted as the command")
	}
}

// The whole reason the composer is painted by stretch rather than by column:
// a message long enough to wrap puts its mentions on whichever row it likes,
// and the cursor is drawn into one of them.
func TestInputView_PaintsAMentionThatWrappedOntoTheSecondRow(t *testing.T) {
	withColour(t)

	m := newPaletteModel(t)
	m.input.SetWidth(46)
	m = typeInto(m, "a long enough opening sentence to push the mention over onto @main.go here")

	row := composerRow(m.inputView(), 1)
	if got, want := ansi.Strip(row)[:31], "mention over onto @main.go here"; got != want {
		t.Fatalf("second row = %q, want %q", got, want)
	}
	if got, want := colouring(row, 31), "..................@@@@@@@@.....C"[:31]; got != want {
		t.Errorf("colouring = %q, want %q", got, want)
	}
}

func TestInputView_PaintsACommandThatWrappedOntoTheSecondRow(t *testing.T) {
	withColour(t)

	m := newPaletteModel(t)
	m.input.SetWidth(46)
	m = typeInto(m, "a long enough opening sentence to push the command over onto /compact here")

	row := composerRow(m.inputView(), 1)
	if got, want := ansi.Strip(row)[:31], "command over onto /compact here"; got != want {
		t.Fatalf("second row = %q, want %q", got, want)
	}
	if got, want := colouring(row, 31), "..................////////....."; got != want {
		t.Errorf("colouring = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// The completion ahead of the cursor
// ---------------------------------------------------------------------------

func TestGhost_ShowsTheRestOfTheSelectedMatch(t *testing.T) {
	withColour(t)

	m := composed(t, "/comp", -1)
	if got, want := m.ghost(), "act"; got != want {
		t.Errorf("ghost() = %q, want %q", got, want)
	}
	// The cursor keeps its column and lands on the suggestion's first rune,
	// which is what makes it read as the next thing that would be typed.
	// "/comp" is not a command yet, so it is not coloured as one — the accent
	// arrives with the name, which is the confirmation accepting it gives.
	if got, want := colouring(composerRow(m.inputView(), 0), 8), ".....C~~"; got != want {
		t.Errorf("colouring = %q, want %q", got, want)
	}
	if got, want := ansi.Strip(m.inputView())[:8], "/compact"; got != want {
		t.Errorf("composer text = %q, want %q", got, want)
	}
}

func TestGhost_FollowsThePaletteCursor(t *testing.T) {
	m := composed(t, "/com", -1)
	if got, want := m.ghost(), "pact"; got != want {
		t.Fatalf("ghost() = %q, want the first match's tail %q", got, want)
	}
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyDown})
	if got, want := m.ghost(), "mit"; got != want {
		t.Errorf("ghost() = %q, want the second match's tail %q", got, want)
	}
}

func TestGhost_CompletesACommandMidSentence(t *testing.T) {
	if got, want := composed(t, "please run /com", -1).ghost(), "pact"; got != want {
		t.Errorf("ghost() = %q, want %q", got, want)
	}
}

func TestGhost_CompletesAMention(t *testing.T) {
	m := composed(t, "look at @main", -1)
	if got, want := m.ghost(), ".go"; got != want {
		t.Errorf("ghost() = %q, want %q", got, want)
	}
}

func TestGhost_StaysQuiet(t *testing.T) {
	tests := []struct {
		name   string
		typed  string
		cursor int
	}{
		{"nothing typed", "", -1},
		{"no palette open", "compact the log", -1},
		{"the name is already whole", "/compact", -1},
		{"the cursor is not at the end", "/comp", 2},
		// "@session" finds pkg/auth/session.go by substring, so what it matched
		// is not at the end and there is no tail to offer.
		{"a match that is not a prefix", "@session", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := composed(t, tt.typed, tt.cursor).ghost(); got != "" {
				t.Errorf("ghost() = %q, want nothing offered", got)
			}
		})
	}
}

// A suggestion is drawn over the row's own padding, so it cannot make the
// composer wider than the box it was drawn to fit.
func TestGhost_NotShownWithoutRoomForIt(t *testing.T) {
	withColour(t)

	m := newPaletteModel(t)
	m.input.SetWidth(7)
	m = typeInto(m, "/com")
	if got, want := ansi.Strip(m.inputView()), ansi.Strip(m.input.View()); got != want {
		t.Errorf("composer text = %q, want %q — a suggestion took room it did not have", got, want)
	}
}

// Accepting is what the palette already did: the suggestion is the same match
// tab completes, so the two cannot disagree about what pressing it gives.
func TestGhost_TabAcceptsWhatItOffered(t *testing.T) {
	m := composed(t, "/com", -1)
	want := "/com" + m.ghost() + " "
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyTab})
	if got := m.input.Value(); got != want {
		t.Errorf("value = %q, want %q", got, want)
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
