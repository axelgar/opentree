package chat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// history is what this workspace's chats have sent, and where the reader is
// while walking back through it. It is the message box's own memory: pressing
// up puts the last message back where it was typed, so a prompt worth
// repeating — or worth repeating with one word changed — does not have to be
// typed twice.
//
// It is kept on disk, per workspace, so a window closed and reopened — or a
// chat restarted after its agent died — still has what was typed into it.
// The file is the messages as typed, not the conversation: a resumed session
// replays what was said before as log entries, and a replayed message carries
// the label an image left behind, and re-sending that label would send the
// words rather than the picture.
type history struct {
	// path is the file the messages live in, or "" for a history that lives
	// and dies with the process — a test's, or a chat with no home directory.
	path string

	// sent is the messages, oldest first.
	sent []string

	// at is where the walk has reached. len(sent) means "not walking": the box
	// holds a message being written rather than one being remembered.
	at int

	// draft is what was in the box when the walk began, kept so coming back
	// down past the newest message returns the half-typed message rather than
	// clearing it. Losing it would make up an irreversible key.
	draft string
}

// walking reports whether the box is showing a remembered message.
func (h history) walking() bool { return h.at < len(h.sent) }

// record files a message that has just gone and ends any walk, so the next up
// starts from the newest message rather than from wherever the last walk
// stopped.
//
// A message identical to the one before it is not filed twice: sending the same
// thing twice is usually a retry, and it should cost one press to reach, not two.
func (h history) record(text string) history {
	if text != "" && (len(h.sent) == 0 || h.sent[len(h.sent)-1] != text) {
		h.sent = append(h.sent, text)
		if len(h.sent) > historyMax {
			h.sent = h.sent[len(h.sent)-historyMax:]
		}
		h.save()
	}
	h.at = len(h.sent)
	h.draft = ""
	return h
}

// historyMax is how many messages a workspace keeps. Two hundred is more
// than anyone walks back through, and small enough that rewriting the file
// on every send is not worth noticing.
const historyMax = 200

// HistoryPath is where a workspace's sent messages are kept between chats:
// ~/.opentree/history/<repository>/<workspace>, the repository identified the
// way the sockets identify it. Empty with no home directory to keep it in.
func HistoryPath(repoRoot, workspace string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".opentree", "history", repoKey(repoRoot), workspaceFile(workspace))
}

// loadHistory is what earlier chats of this workspace sent, read from path
// and ready to walk. A file that cannot be read is an empty history rather
// than a chat that will not start, and a line that will not parse costs only
// itself.
func loadHistory(path string) history {
	h := history{path: path}
	if path == "" {
		return h
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return h
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var text string
		if err := json.Unmarshal([]byte(line), &text); err != nil || text == "" {
			continue
		}
		h.sent = append(h.sent, text)
	}
	h.at = len(h.sent)
	return h
}

// save writes the messages back, one JSON string per line — a message can
// span lines, and JSON is the encoding that keeps that and needs no parser
// of its own. Best-effort: a history that could not be written is the one
// this process has, which is what it always was. 0600, because what people
// type at an agent is not always meant for the other accounts on a machine.
func (h history) save() {
	if h.path == "" {
		return
	}
	var b strings.Builder
	for _, text := range h.sent {
		line, err := json.Marshal(text)
		if err != nil {
			continue
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(h.path, []byte(b.String()), 0o600)
}

// walk moves delta messages through the history and returns what the box
// should now hold. ok=false means there is nowhere to go — the oldest message
// is already showing, or nothing has been sent yet — and the box is left alone.
//
// current is what the box holds now, which is only read on the step that leaves
// a message being written behind.
func (h history) walk(delta int, current string) (history, string, bool) {
	at := h.at + delta
	if at < 0 || at > len(h.sent) {
		return h, "", false
	}
	if !h.walking() {
		h.draft = current
	}
	h.at = at
	if at == len(h.sent) {
		return h, h.draft, true
	}
	return h, h.sent[at], true
}
