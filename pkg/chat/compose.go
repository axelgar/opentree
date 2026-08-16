package chat

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/axelgar/opentree/pkg/acp"
)

// completionWindow is how many palette rows are on screen at once, so the
// palette cannot swallow the conversation. Every match is kept — the rest
// scroll past under the cursor.
const completionWindow = 6

// maxImageBytes is the largest image opentree will inline.
//
// ponytail: 4 MiB, because base64 inflates by 4/3 and the per-image ceiling on
// the wire is 5 MB. Downscaling instead of refusing means carrying an image
// decoder; add one if anyone actually hits this.
const maxImageBytes = 4 << 20

// composer is what turning a typed message into blocks needs to know about
// the session: where relative paths root, which files git tracks, and whether
// the agent takes images. The three travel through every step of composing
// together, which is what makes them one thing rather than three parameters.
type composer struct {
	cwd    string
	known  map[string]bool
	images bool
}

// prompt turns typed input into ACP content blocks, promoting the file
// paths in it into attachments. A file becomes a resource link, which points
// the agent at it rather than pasting its contents, so the agent reads what it
// actually needs. An image becomes an inline image block instead: fs is
// text-only, so a link to a PNG leaves nothing to read.
//
// A word that does not name a file stays literal text: a stray @ in prose
// should not become a broken link.
//
// The second return is what the reader should be told about a file that could
// not travel the way it looked like it would. Silence there would be the worst
// outcome: an image that quietly went as a link looks identical to one that did
// not, right up until the agent says it cannot see your screenshot.
func (c composer) prompt(text string, pending []acp.ContentBlock) ([]acp.ContentBlock, []string) {
	var blocks []acp.ContentBlock
	var notices []string

	for _, s := range splitPending(text, pending) {
		if s.block != nil {
			blocks = append(blocks, *s.block)
			continue
		}
		b, n := c.text(s.text)
		blocks = append(blocks, b...)
		notices = append(notices, n...)
	}

	if len(blocks) == 0 {
		blocks = append(blocks, acp.TextBlock(text))
	}
	return blocks, notices
}

// span is one stretch of a message: either literal text still to be composed,
// or an attachment already resolved.
type span struct {
	text  string
	block *acp.ContentBlock
}

// splitPending cuts the message at each pasted attachment's label.
//
// The label sits in the input as ordinary text, which is what makes it behave
// the way a chip should: it is where the cursor left it, it moves with editing,
// and backspacing it takes the image with it — the only undo a marker in a text
// box can have. It is also exactly what the log will show, so the message on
// screen is the message being sent.
//
// Labels are consumed left to right, so two pastes of the same image resolve to
// the two blocks in order rather than both to the first.
func splitPending(text string, pending []acp.ContentBlock) []span {
	if len(pending) == 0 {
		return []span{{text: text}}
	}
	left := make([]acp.ContentBlock, len(pending))
	copy(left, pending)

	var out []span
	for len(left) > 0 {
		at, which := -1, -1
		for i, c := range left {
			if j := strings.Index(text, imageLabel(c)); j >= 0 && (at < 0 || j < at) {
				at, which = j, i
			}
		}
		if at < 0 {
			// Whatever is left was deleted out of the message before it was
			// sent, which is how an attachment is taken back.
			break
		}
		block := left[which]
		out = append(out, span{text: text[:at]}, span{block: &block})
		text = text[at+len(imageLabel(block)):]
		left = append(left[:which], left[which+1:]...)
	}
	return append(out, span{text: text})
}

// text turns one stretch of literal text into blocks.
func (c composer) text(text string) ([]acp.ContentBlock, []string) {
	var blocks []acp.ContentBlock
	var notices []string
	var buf strings.Builder

	flush := func() {
		if buf.Len() > 0 {
			blocks = append(blocks, acp.TextBlock(buf.String()))
			buf.Reset()
		}
	}

	runes := []rune(text)
	for i := 0; i < len(runes); {
		if isBoundary(runes[i]) {
			buf.WriteRune(runes[i])
			i++
			continue
		}
		j := wordEnd(runes, i)
		word := string(runes[i:j])
		if block, notice, ok := c.attach(word); ok {
			flush()
			blocks = append(blocks, block)
			if notice != "" {
				notices = append(notices, notice)
			}
		} else {
			buf.WriteString(word)
		}
		i = j
	}
	flush()
	return blocks, notices
}

func isBoundary(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

// wordEnd finds where the word starting at i ends. A backslash-escaped space
// does not end it: dragging a file onto a terminal inserts its path that way,
// and "~/Desktop/Screenshot 2026.png" arrives as one word wearing two escapes.
func wordEnd(runes []rune, i int) int {
	for i < len(runes) {
		if runes[i] == '\\' && i+1 < len(runes) && isBoundary(runes[i+1]) {
			i += 2
			continue
		}
		if isBoundary(runes[i]) {
			return i
		}
		i++
	}
	return i
}

// attach decides what one word of the message becomes. ok=false leaves it as
// typed, which is the answer for everything that is not a file.
func (c composer) attach(word string) (acp.ContentBlock, string, bool) {
	abs, name, ok := c.file(word)
	if !ok {
		return acp.ContentBlock{}, "", false
	}
	block, notice := c.classify(abs, name)
	return block, notice, true
}

// file is which file one word of the message names, and the name to show for
// it. It is the half of attach that costs nothing to ask, which is what the
// composer paints mentions with: deciding how a file travels means reading and
// base64-encoding it, and that is not a price to pay for a colour on a frame
// that will be drawn again in a tenth of a second.
func (c composer) file(word string) (abs, name string, ok bool) {
	path := strings.TrimPrefix(word, "@")
	if path == word && !looksLikePath(path) {
		// A bare word only counts as a path when it is shaped like one. Without
		// that, "README" typed in a sentence would silently become an
		// attachment the moment a file by that name existed.
		return "", "", false
	}
	return c.resolve(path)
}

// looksLikePath is the test a word has to pass to be considered without an @.
// Requiring a separator is what keeps prose out: paths people drag in or type
// have one, ordinary words do not.
func looksLikePath(s string) bool {
	return strings.ContainsRune(s, '/') || strings.HasPrefix(s, "~")
}

// resolve finds the file a word names, and the name to show for it.
//
// A tracked file resolves without touching the disk, exactly as it always has:
// the completion palette offers what git listed, and a mention of one should
// travel whether or not it survives to the moment of sending.
func (c composer) resolve(path string) (abs, name string, ok bool) {
	path = unescape(path)
	if path == "" {
		return "", "", false
	}
	if c.known[path] {
		return filepath.Join(c.cwd, path), path, true
	}

	abs = path
	if strings.HasPrefix(abs, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", false
		}
		abs = filepath.Join(home, strings.TrimPrefix(abs, "~"))
	}
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(c.cwd, abs)
	}
	// An untracked path has to exist to count. A typo would otherwise be sent
	// as a link to nothing, which reads to the agent as a file it failed to open.
	if fi, err := os.Stat(abs); err != nil || !fi.Mode().IsRegular() {
		return "", "", false
	}
	// A relative path keeps the shape it was typed in; an absolute one is
	// shortened, because the log has better uses for eighty columns than
	// /var/folders/9k/…/T/.
	if filepath.IsAbs(path) || strings.HasPrefix(path, "~") {
		return abs, filepath.Base(abs), true
	}
	return abs, path, true
}

// classify turns a resolved file into the block that carries it best. Every
// failure falls back to a link, which is what the file would have been anyway.
func (c composer) classify(abs, name string) (acp.ContentBlock, string) {
	link := acp.ResourceLink(fileURI(abs), name)

	fi, err := os.Stat(abs)
	if err != nil || !fi.Mode().IsRegular() {
		return link, ""
	}
	// Magic bytes rather than the extension: stdlib already knows png, jpeg,
	// gif and webp, and a screenshot saved under the wrong name is still a
	// screenshot.
	mime := sniff(abs)
	if !strings.HasPrefix(mime, "image/") {
		return link, ""
	}
	if !c.images {
		return link, name + " went as a link — this agent does not take images"
	}
	if fi.Size() > maxImageBytes {
		return link, fmt.Sprintf("%s is %s — too large to inline, sent as a link", name, humanBytes(fi.Size()))
	}
	data, err := os.ReadFile(abs) // #nosec G304 -- a path the user named in their own message
	if err != nil {
		return link, ""
	}
	img := acp.ImageBlock(base64.StdEncoding.EncodeToString(data), mime)
	// The uri is optional on an image block and agents ignore it once data is
	// present, which makes it free somewhere to keep the file's name — so the
	// log can say which picture went, not just that one did.
	img.URI = link.URI
	return img, ""
}

// ---------------------------------------------------------------------------
// Blocks back to text
// ---------------------------------------------------------------------------

// echo is a sent message as the log should show it. It runs the same renderer a
// replayed message does, so reopening a conversation reads the way sending it
// did rather than subtly differently.
func echo(blocks []acp.ContentBlock) string {
	var b strings.Builder
	for _, c := range blocks {
		text := blockText(c)
		if text == "" {
			continue
		}
		b.WriteString(separator(b.String(), text))
		b.WriteString(text)
	}
	return b.String()
}

// blockText is what one content block reads as in the log. Empty means it has
// nothing to show and should not become an entry at all — an entry with no text
// draws a full-width band with nothing in it.
func blockText(c acp.ContentBlock) string {
	switch c.Type {
	case acp.BlockResourceLink:
		// An @mention leaves as a link and its sigil leaves with it, so putting
		// the sigil back is what makes the line read as it was typed.
		return "@" + c.Name
	case acp.BlockImage:
		return imageLabel(c)
	}
	return c.Text
}

// imageLabel stands in for a picture the terminal will not be drawing.
//
// ponytail: a line, not the image. The chat runs inside a tmux window, and tmux
// only forwards the terminal graphics protocols on 3.4+ with allow-passthrough
// set — an image that appeared for some readers and silently vanished for the
// rest is worse than a line that is honest for everyone.
func imageLabel(c acp.ContentBlock) string {
	parts := []string{"image"}
	// The file's name when there is one, otherwise the kind of image it is.
	//
	// A name has to have an extension to count. Replaying a pasted image —
	// which was sent with no uri at all — opencode invents one ending in
	// "/image", and "[image · image · 303 bytes]" reads as a rendering fault
	// rather than as a picture.
	if name := uriName(c.URI); filepath.Ext(name) != "" {
		parts = append(parts, name)
	} else if kind := strings.TrimPrefix(c.MimeType, "image/"); kind != "" && kind != c.MimeType {
		parts = append(parts, kind)
	}
	if n := base64.StdEncoding.DecodedLen(len(c.Data)); n > 0 {
		parts = append(parts, humanBytes(int64(n)))
	}
	return "[" + strings.Join(parts, " · ") + "]"
}

func uriName(uri string) string {
	if uri == "" {
		return ""
	}
	if u, err := url.Parse(uri); err == nil && u.Path != "" {
		return filepath.Base(u.Path)
	}
	return filepath.Base(uri)
}

// separator is what goes between two content blocks joined into one line.
// Blocks carry no spacing of their own, so two that would otherwise run
// together get a line break. Between "look at " and a picture there is already
// a space, and inventing a second break would read as a paragraph nobody typed.
func separator(prev, next string) string {
	if prev == "" || next == "" || endsSpace(prev) || startsSpace(next) {
		return ""
	}
	return "\n"
}

func endsSpace(s string) bool   { return s != "" && isBoundary(rune(s[len(s)-1])) }
func startsSpace(s string) bool { return s != "" && isBoundary(rune(s[0])) }

func sniff(path string) string {
	f, err := os.Open(path) // #nosec G304 -- a path the user named in their own message
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	var head [512]byte
	n, _ := f.Read(head[:])
	return sniffBytes(head[:n])
}

// sniffBytes names a mime type the way the wire wants it. DetectContentType
// appends a charset to some types and the protocol carries the bare one.
func sniffBytes(b []byte) string {
	return strings.TrimSpace(strings.SplitN(http.DetectContentType(b), ";", 2)[0])
}

// unescape undoes the quoting a terminal applies to a dragged path.
func unescape(s string) string {
	s = strings.Trim(s, `'"`)
	return strings.ReplaceAll(s, `\ `, " ")
}

func fileURI(abs string) string {
	return (&url.URL{Scheme: "file", Path: abs}).String()
}

func humanBytes(n int64) string {
	switch {
	case n < 1<<10:
		// Not "0 KB": a size that rounds to nothing reads as a failed read
		// rather than as a small file.
		return fmt.Sprintf("%d bytes", n)
	case n < 1<<20:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
}

// ---------------------------------------------------------------------------
// Completion
// ---------------------------------------------------------------------------

type completionKind int

const (
	completionNone completionKind = iota
	completionCommand
	completionFile
)

type completionItem struct {
	value string
	desc  string
}

type completionState struct {
	kind   completionKind
	token  string // the word being completed, sigil included
	items  []completionItem
	cursor int
}

func (c completionState) active() bool { return c.kind != completionNone && len(c.items) > 0 }

// completionFor decides what the trailing word of the input is asking to
// complete.
//
// ponytail: the trailing word, not the word under the cursor. Completing
// mid-line would mean tracking the textarea's cursor across wrapping, and
// people type these at the end. Revisit if that stops being true.
func completionFor(input string, commands []acp.Command, files []string) completionState {
	if input == "" || isBoundary(rune(input[len(input)-1])) {
		return completionState{}
	}
	token := input[strings.LastIndexAny(input, " \t\n")+1:]

	switch {
	case strings.HasPrefix(token, "/") && token == input:
		// A slash command is only a command when it opens the message.
		return completionState{kind: completionCommand, token: token,
			items: matchCommands(strings.TrimPrefix(token, "/"), commands)}

	case strings.HasPrefix(token, "@"):
		return completionState{kind: completionFile, token: token,
			items: matchFiles(strings.TrimPrefix(token, "@"), files)}
	}
	return completionState{}
}

// commandToken is the slash command a message opens with, empty when it opens
// with something else. It is what the composer paints: the sigil and the name,
// without whatever arguments follow them.
//
// Only a name that exists counts. The colour is the message saying the command
// resolves, so painting "/reusme" the same orange as "/resume" would be saying
// the opposite of what it means — and while a name is still half typed, the
// palette above the input is already showing what it matches.
func commandToken(input string, commands []acp.Command) string {
	if !strings.HasPrefix(input, "/") {
		// The same rule the palette applies: a slash command is only a command
		// when it opens the message.
		return ""
	}
	runes := []rune(input)
	token := string(runes[:wordEnd(runes, 0)])
	for _, c := range commands {
		if c.Name == strings.TrimPrefix(token, "/") {
			return token
		}
	}
	return ""
}

func matchCommands(prefix string, commands []acp.Command) []completionItem {
	var items []completionItem
	for _, c := range commands {
		if strings.HasPrefix(c.Name, prefix) {
			items = append(items, completionItem{value: "/" + c.Name, desc: firstLine(c.Description)})
		}
	}
	return items
}

// matchFiles prefers a path prefix match but falls back to a substring, so
// "@session" finds pkg/auth/session.go without typing the directories.
func matchFiles(prefix string, files []string) []completionItem {
	var prefixed, contained []completionItem
	for _, f := range files {
		switch {
		case strings.HasPrefix(f, prefix):
			prefixed = append(prefixed, completionItem{value: "@" + f})
		case prefix != "" && strings.Contains(f, prefix):
			contained = append(contained, completionItem{value: "@" + f})
		}
	}
	return append(prefixed, contained...)
}

// applyCompletion replaces the trailing word with the chosen value.
func applyCompletion(input string, item completionItem) string {
	cut := strings.LastIndexAny(input, " \t\n") + 1
	return input[:cut] + item.value + " "
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
