package chat

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/axelgar/opentree/pkg/acp"
)

// maxCompletionItems caps the palette so it cannot swallow the conversation.
const maxCompletionItems = 6

// composePrompt turns typed input into ACP content blocks, promoting @mentions
// of known files into resource links. A link points the agent at the file
// rather than pasting its contents, so the agent reads what it actually needs.
//
// A mention that does not match a tracked file stays literal text: a stray @ in
// prose should not become a broken link.
func composePrompt(text, cwd string, known map[string]bool) []acp.ContentBlock {
	var blocks []acp.ContentBlock
	var buf strings.Builder

	flush := func() {
		if buf.Len() > 0 {
			blocks = append(blocks, acp.TextBlock(buf.String()))
			buf.Reset()
		}
	}

	runes := []rune(text)
	for i := 0; i < len(runes); {
		if runes[i] != '@' || (i > 0 && !isBoundary(runes[i-1])) {
			buf.WriteRune(runes[i])
			i++
			continue
		}

		j := i + 1
		for j < len(runes) && !isBoundary(runes[j]) {
			j++
		}
		path := string(runes[i+1 : j])
		if path != "" && known[path] {
			flush()
			blocks = append(blocks, acp.ResourceLink(fileURI(cwd, path), path))
		} else {
			buf.WriteString(string(runes[i:j]))
		}
		i = j
	}
	flush()

	if len(blocks) == 0 {
		blocks = append(blocks, acp.TextBlock(text))
	}
	return blocks
}

func isBoundary(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

func fileURI(cwd, path string) string {
	abs := filepath.Join(cwd, path)
	return (&url.URL{Scheme: "file", Path: abs}).String()
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

	// total is how many matched before the cap. opencode advertises 35
	// commands and the palette shows six of them, which read as "that is all
	// there is" — so the palette says what it is holding back.
	total int
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
		items, total := matchCommands(strings.TrimPrefix(token, "/"), commands)
		return completionState{kind: completionCommand, token: token, items: items, total: total}

	case strings.HasPrefix(token, "@"):
		items, total := matchFiles(strings.TrimPrefix(token, "@"), files)
		return completionState{kind: completionFile, token: token, items: items, total: total}
	}
	return completionState{}
}

func matchCommands(prefix string, commands []acp.Command) ([]completionItem, int) {
	var items []completionItem
	var total int
	for _, c := range commands {
		if !strings.HasPrefix(c.Name, prefix) {
			continue
		}
		total++
		if len(items) < maxCompletionItems {
			items = append(items, completionItem{value: "/" + c.Name, desc: firstLine(c.Description)})
		}
	}
	return items, total
}

// matchFiles prefers a path prefix match but falls back to a substring, so
// "@session" finds pkg/auth/session.go without typing the directories.
func matchFiles(prefix string, files []string) ([]completionItem, int) {
	var prefixed, contained []completionItem
	var total int
	for _, f := range files {
		switch {
		case strings.HasPrefix(f, prefix):
			total++
			if len(prefixed) < maxCompletionItems {
				prefixed = append(prefixed, completionItem{value: "@" + f})
			}
		case prefix != "" && strings.Contains(f, prefix):
			total++
			if len(contained) < maxCompletionItems {
				contained = append(contained, completionItem{value: "@" + f})
			}
		}
	}
	items := append(prefixed, contained...)
	if len(items) > maxCompletionItems {
		items = items[:maxCompletionItems]
	}
	return items, total
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
