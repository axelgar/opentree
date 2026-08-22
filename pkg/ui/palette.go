package ui

import "github.com/charmbracelet/lipgloss"

// The palette both of opentree's terminal programs draw from.
//
// It is here rather than in each of them because a colour is a value, not a
// style: pkg/tui and pkg/chat still keep their own style vars, for the reason
// pkg/chat/styles.go gives, and they now agree on what "the accent" or "a
// stopped thing" looks like instead of agreeing by coincidence.
//
// Every entry is a lipgloss.AdaptiveColor, and that is the point of the file.
// There were ninety-seven hardcoded colours across the two programs and not one
// AdaptiveColor among them, all of them chosen against a dark terminal — the
// agent's own replies were #EEE on whatever the terminal's background happened
// to be, which is 1.16:1 on white. On Solarized Light, on a default macOS
// Terminal, on most editor-embedded terminals, the answer you were waiting for
// was invisible. AdaptiveColor is lipgloss asking the terminal which it is and
// picking; it costs one query at startup.
//
// The Dark values are exactly what the two programs used before this file
// existed, so nothing changes for anyone already reading them. The Light values
// are new, and every one of them clears WCAG AA — 4.5:1 against white — with
// the hue kept so the palette still means the same things.
//
// Three kinds of colour are deliberately absent. ANSI 196, opentree's red, is
// the terminal's own palette entry and is readable in whatever theme its owner
// chose. A badge that sets both its foreground and its background carries its
// own contrast with it. And a colour the agent registry supplies is that
// vendor's, not opentree's, to adapt.
var (
	// Body is ordinary text: the agent's reply, what you typed. The one that
	// was invisible.
	Body = lipgloss.AdaptiveColor{Dark: "#EEE", Light: "#1F1F1F"}

	// BodyStrong is text a shade brighter than Body on dark, used where a
	// label leads a line.
	BodyStrong = lipgloss.AdaptiveColor{Dark: "#DDD", Light: "#2E2E2E"}

	// Muted is secondary text that should still be read: a tool's title, a
	// file name, a count.
	Muted = lipgloss.AdaptiveColor{Dark: "#AAA", Light: "#6B6B6B"}

	// Meta is text that is there when you look for it: help lines, hints, the
	// working directory.
	Meta = lipgloss.AdaptiveColor{Dark: "#888", Light: "#595959"}

	// Dim is quieter still — a row's second line, a label under a value.
	Dim = lipgloss.AdaptiveColor{Dark: "#626262", Light: "#5C5C5C"}

	// Faint is the quietest thing that is still text: a stopped workspace, a
	// scroll hint, a skill that is switched off.
	Faint = lipgloss.AdaptiveColor{Dark: "#666", Light: "#6A6A6A"}

	// Ghost is placeholder text and the agent's own thinking — present, and
	// meant to be skipped over.
	Ghost = lipgloss.AdaptiveColor{Dark: "#5F5F5F", Light: "#6E6E6E"}

	// Whisper is barely there: a diffstat behind a row, a pending item.
	Whisper = lipgloss.AdaptiveColor{Dark: "#555", Light: "#767676"}

	// Accent is opentree's amber: the cursor, the selected row, a key worth
	// pressing.
	Accent = lipgloss.AdaptiveColor{Dark: "#F4A261", Light: "#A2521B"}

	// Success is a thing that worked, and a line that was added.
	Success = lipgloss.AdaptiveColor{Dark: "#2A9D8F", Light: "#17685F"}

	// Warn is a thing in progress or worth a second look: uncommitted work, a
	// tool still running.
	Warn = lipgloss.AdaptiveColor{Dark: "#E9C46A", Light: "#8A6100"}

	// WarnFile is Warn's near neighbour, kept apart because the file list uses
	// both at once and they have to stay distinguishable.
	WarnFile = lipgloss.AdaptiveColor{Dark: "#E5C07B", Light: "#B36D00"}

	// Info is a hunk header, a shared tag — structure rather than state.
	Info = lipgloss.AdaptiveColor{Dark: "#88C0D0", Light: "#2A6E80"}

	// Toast is an error said briefly, in the corner, rather than in a panel.
	Toast = lipgloss.AdaptiveColor{Dark: "#E76F51", Light: "#A83E22"}

	// Border is a box's edge. It carries no text, so it is judged by whether
	// the box reads as a box.
	Border = lipgloss.AdaptiveColor{Dark: "#444", Light: "#B0B0B0"}

	// Divider is the horizontal rule under a header.
	Divider = lipgloss.AdaptiveColor{Dark: "#333", Light: "#C8C8C8"}

	// Band is the tint behind your own messages: a shade off the background,
	// on whichever side of it the background is.
	Band = lipgloss.AdaptiveColor{Dark: "#26262B", Light: "#F0F0F2"}

	// ToolOutput is a command's output quoted back inside a tool call.
	ToolOutput = lipgloss.AdaptiveColor{Dark: "#7A7A7A", Light: "#6A6A6A"}
)

// Danger is the terminal's own red, and stays an ANSI index on purpose: 196 is
// whatever red the user chose, in whatever theme they chose it for, and a hex
// pair would be opentree overruling that in both directions at once.
const Danger = lipgloss.Color("196")
