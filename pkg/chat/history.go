package chat

// history is what this chat has already sent, and where the reader is while
// walking back through it. It is the message box's own memory: pressing up
// puts the last message back where it was typed, so a prompt worth repeating —
// or worth repeating with one word changed — does not have to be typed twice.
//
// ponytail: this run of the chat, not the conversation. A resumed session
// replays what was said before as log entries rather than as typed text, and
// the two are not the same string — a replayed message carries the label an
// image left behind, and re-sending that label would send the words rather
// than the picture.
type history struct {
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
	}
	h.at = len(h.sent)
	h.draft = ""
	return h
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
