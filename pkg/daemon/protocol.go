package daemon

import (
	"encoding/json"
	"time"

	"github.com/axelgar/opentree/pkg/workspace"
)

// ProtocolVersion is the current wire-protocol version. Both client and server
// embed this in every Request. The server rejects requests whose version field
// is non-empty and does not match this constant, so incompatible upgrades are
// detected early rather than causing silent data corruption.
const ProtocolVersion = "1"

// Request is the JSON envelope sent from client to server (newline-delimited).
type Request struct {
	Version string          `json:"v,omitempty"`
	Method  string          `json:"method"`
	ID      int             `json:"id"`
	Params  json.RawMessage `json:"params"`
}

// Response is the JSON envelope sent from server to client (newline-delimited).
type Response struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Method constants — 1:1 with TerminalProcessManager + ProcessManager methods.
const (
	MethodPing              = "ping"
	MethodCreateWindow      = "create_window"
	MethodCreateWindowSized = "create_window_sized"
	MethodListWindows       = "list_windows"
	MethodKillWindow        = "kill_window"
	MethodKillSession       = "kill_session"
	MethodCapturePane       = "capture_pane"
	MethodGetWindowActivity = "get_window_activity"
	MethodSendMessage       = "send_message"
	MethodRenderScreen      = "render_screen"
	MethodResizeWindow      = "resize_window"
	MethodWriteInput        = "write_input"
	MethodIsRunning         = "is_running"
	MethodScrollbackLines   = "scrollback_lines"
)

// Param types.

type CreateWindowParams struct {
	Name    string   `json:"name"`
	Workdir string   `json:"workdir"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

type CreateWindowSizedParams struct {
	Name    string   `json:"name"`
	Workdir string   `json:"workdir"`
	Command string   `json:"command"`
	Cols    int      `json:"cols"`
	Rows    int      `json:"rows"`
	Args    []string `json:"args,omitempty"`
}

type NameParams struct {
	Name string `json:"name"`
}

type CapturePaneParams struct {
	Name  string `json:"name"`
	Lines int    `json:"lines"`
}

type SendMessageParams struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

type ResizeWindowParams struct {
	Name string `json:"name"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

type WriteInputParams struct {
	Name string `json:"name"`
	Data []byte `json:"data"` // base64-encoded in JSON automatically by encoding/json
}

type ScrollbackLinesParams struct {
	Name   string `json:"name"`
	Offset int    `json:"offset"`
	Count  int    `json:"count"`
}

// Result types.

type ListWindowsResult struct {
	Windows []workspace.Window `json:"windows"`
}

type CapturePaneResult struct {
	Output string `json:"output"`
}

type GetWindowActivityResult struct {
	LastActivity time.Time `json:"last_activity"`
}

type RenderScreenResult struct {
	Screen string `json:"screen"`
}

type IsRunningResult struct {
	Running bool `json:"running"`
}

type ScrollbackLinesResult struct {
	Lines []string `json:"lines"`
}
