package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/axelgar/opentree/pkg/workspace"
)

// DaemonUnavailableError indicates the daemon is not running or unreachable.
type DaemonUnavailableError struct {
	Err error
}

func (e *DaemonUnavailableError) Error() string {
	return fmt.Sprintf("daemon unavailable: %v", e.Err)
}

func (e *DaemonUnavailableError) Unwrap() error { return e.Err }

// Compile-time check that Client implements TerminalProcessManager.
var _ workspace.TerminalProcessManager = (*Client)(nil)

// Client communicates with the daemon over a Unix socket.
// Each method call opens a short-lived connection.
type Client struct {
	sockPath string
	nextID   atomic.Int64
}

// NewClient creates a new daemon client for the given repo root.
func NewClient(repoRoot string) *Client {
	return &Client{
		sockPath: filepath.Join(repoRoot, ".opentree", "daemon.sock"),
	}
}

func (c *Client) call(method string, params interface{}) (json.RawMessage, error) {
	conn, err := net.DialTimeout("unix", c.sockPath, 5*time.Second)
	if err != nil {
		return nil, &DaemonUnavailableError{Err: err}
	}
	defer conn.Close()

	// Prevent the call from blocking forever if the daemon stops responding.
	if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, &DaemonUnavailableError{Err: err}
	}

	id := int(c.nextID.Add(1))

	var rawParams json.RawMessage
	if params != nil {
		rawParams, err = json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
	}

	req := Request{
		Version: ProtocolVersion,
		Method:  method,
		ID:      id,
		Params:  rawParams,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	data = append(data, '\n')

	if _, err := conn.Write(data); err != nil {
		return nil, &DaemonUnavailableError{Err: err}
	}

	// Match the server's 1 MB buffer to handle large screen renders with ANSI.
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	if !scanner.Scan() {
		return nil, &DaemonUnavailableError{Err: fmt.Errorf("no response from daemon")}
	}

	var resp Response
	if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}

	return resp.Result, nil
}

func (c *Client) CreateWindow(name, workdir, command string, args ...string) error {
	_, err := c.call(MethodCreateWindow, CreateWindowParams{
		Name: name, Workdir: workdir, Command: command, Args: args,
	})
	return err
}

func (c *Client) CreateWindowSized(name, workdir, command string, cols, rows int, args ...string) error {
	_, err := c.call(MethodCreateWindowSized, CreateWindowSizedParams{
		Name: name, Workdir: workdir, Command: command, Cols: cols, Rows: rows, Args: args,
	})
	return err
}

func (c *Client) ListWindows() ([]workspace.Window, error) {
	raw, err := c.call(MethodListWindows, nil)
	if err != nil {
		return nil, err
	}
	var result ListWindowsResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}
	return result.Windows, nil
}

func (c *Client) SelectWindow(_ string) error {
	return nil // no-op, same as NativeProcessManager
}

func (c *Client) AttachWindow(_ string) error {
	return fmt.Errorf("attach is not supported with daemon — use the TUI terminal view")
}

func (c *Client) AttachCmd(_ string) (*exec.Cmd, error) {
	return nil, fmt.Errorf("attach is not supported with daemon — use the TUI terminal view")
}

func (c *Client) KillWindow(name string) error {
	_, err := c.call(MethodKillWindow, NameParams{Name: name})
	return err
}

func (c *Client) KillSession() error {
	_, err := c.call(MethodKillSession, nil)
	return err
}

func (c *Client) CapturePane(name string, lines int) (string, error) {
	raw, err := c.call(MethodCapturePane, CapturePaneParams{Name: name, Lines: lines})
	if err != nil {
		return "", err
	}
	var result CapturePaneResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("unmarshal result: %w", err)
	}
	return result.Output, nil
}

func (c *Client) GetWindowActivity(name string) (time.Time, error) {
	raw, err := c.call(MethodGetWindowActivity, NameParams{Name: name})
	if err != nil {
		return time.Time{}, err
	}
	var result GetWindowActivityResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return time.Time{}, fmt.Errorf("unmarshal result: %w", err)
	}
	return result.LastActivity, nil
}

func (c *Client) SendMessage(name, text string) error {
	_, err := c.call(MethodSendMessage, SendMessageParams{Name: name, Text: text})
	return err
}

func (c *Client) RenderScreen(name string) (string, error) {
	raw, err := c.call(MethodRenderScreen, NameParams{Name: name})
	if err != nil {
		return "", err
	}
	var result RenderScreenResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("unmarshal result: %w", err)
	}
	return result.Screen, nil
}

func (c *Client) ResizeWindow(name string, cols, rows int) error {
	_, err := c.call(MethodResizeWindow, ResizeWindowParams{Name: name, Cols: cols, Rows: rows})
	return err
}

func (c *Client) WriteInput(name string, data []byte) error {
	_, err := c.call(MethodWriteInput, WriteInputParams{Name: name, Data: data})
	return err
}

func (c *Client) IsRunning(name string) bool {
	raw, err := c.call(MethodIsRunning, NameParams{Name: name})
	if err != nil {
		return false
	}
	var result IsRunningResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return false
	}
	return result.Running
}

func (c *Client) ScrollbackLines(name string, offset, count int) ([]string, error) {
	raw, err := c.call(MethodScrollbackLines, ScrollbackLinesParams{Name: name, Offset: offset, Count: count})
	if err != nil {
		return nil, err
	}
	var result ScrollbackLinesResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal result: %w", err)
	}
	return result.Lines, nil
}
