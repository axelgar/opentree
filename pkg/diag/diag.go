// Package diag is opentree's log, for the times somebody has to say what
// happened on a machine that is not yours.
//
// It exists because there was no answer to "can you send me the log". opentree
// drives tmux, git, gh, an agent subprocess, MCP servers under that, and npm —
// and the entire record of any of it going wrong was a twenty-entry list in the
// dashboard's memory, gone when the dashboard quit, plus fifty lines of the
// agent's stderr shown only once the agent had already died. "The chat window
// closes instantly" had nothing to attach to a bug report.
//
// Off unless asked for, and asked for by an environment variable rather than a
// flag: the interesting failures happen in `opentree chat`, which is started by
// a tmux window rather than by a person, so a flag would have to be threaded
// through the launch line to reach the process that is failing. An environment
// variable is inherited by everything opentree starts, which is exactly the set
// of processes worth recording.
//
//	OPENTREE_LOG=/tmp/opentree.log opentree
//
// Nothing here is on a hot path, and when logging is off every call is a nil
// check and a return.
package diag

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// EnvVar is where the log goes. Empty, unset, or a path that cannot be opened
// means no logging at all — a diagnostic aid that stopped the program it was
// meant to diagnose would be a poor trade.
const EnvVar = "OPENTREE_LOG"

var (
	mu   sync.Mutex
	file *os.File
	path string
)

// Init opens the log if the environment asks for one. It is safe to call more
// than once and from more than one process: the file is opened for append, and
// every line is written whole, so several opentrees sharing a path interleave
// by line rather than corrupting each other.
//
// component names the process in every line it writes. There are usually
// several at once — a dashboard and one chat per workspace — and a log that
// could not tell them apart would be worse than none.
func Init(component string) {
	p := strings.TrimSpace(os.Getenv(EnvVar))
	if p == "" {
		return
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		// Said once, on stderr, and then never again. A chat window whose
		// stderr goes nowhere is exactly the case this is for, so it cannot
		// depend on being read.
		fmt.Fprintf(os.Stderr, "opentree: %s=%s could not be opened (%v); continuing without a log\n", EnvVar, p, err)
		return
	}

	mu.Lock()
	defer mu.Unlock()
	file, path = f, p
	writeLine(component, "log opened", "pid", os.Getpid())
}

// Path is where the log is being written, or "" when there is none. It is what
// `opentree doctor` prints, so somebody can be told which file to send.
func Path() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}

// Enabled reports whether anything is being recorded.
func Enabled() bool { return Path() != "" }

// Log records one event. kv is alternating keys and values, so a line stays
// greppable and a reader can find every launch without knowing how the message
// was worded:
//
//	diag.Log("chat", "agent launch failed", "agent", name, "err", err)
//
// An odd number of kv entries is written as-is rather than refused. This is the
// code that runs when something is already going wrong; it does not get to be
// the thing that fails.
func Log(component, msg string, kv ...any) {
	mu.Lock()
	defer mu.Unlock()
	if file == nil {
		return
	}
	writeLine(component, msg, kv...)
}

// writeLine assumes mu is held.
func writeLine(component, msg string, kv ...any) {
	if file == nil {
		return
	}
	var b strings.Builder
	b.WriteString(time.Now().Format("2006-01-02T15:04:05.000Z07:00"))
	b.WriteString(" [")
	b.WriteString(component)
	b.WriteString("] ")
	b.WriteString(msg)
	for i := 0; i < len(kv); i += 2 {
		b.WriteString(" ")
		fmt.Fprint(&b, kv[i])
		if i+1 < len(kv) {
			b.WriteString("=")
			b.WriteString(quoteIfNeeded(fmt.Sprint(kv[i+1])))
		}
	}
	b.WriteString("\n")
	_, _ = file.WriteString(b.String())
}

// quoteIfNeeded keeps a value that contains spaces from looking like the start
// of the next key.
func quoteIfNeeded(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\"\n") {
		return strconv.Quote(s)
	}
	return s
}
