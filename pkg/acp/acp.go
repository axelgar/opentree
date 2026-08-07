// Package acp implements the client half of the Agent Client Protocol
// (https://agentclientprotocol.com): newline-delimited JSON-RPC 2.0 spoken to
// an agent subprocess over its stdio.
//
// The wire types were derived from recorded `opencode acp` traffic rather than
// from the published schema — see testdata/ for the captures. Where the two
// disagreed, the wire won.
package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
)

// CodeAuthRequired is the JSON-RPC error code an agent returns when a request
// needs credentials it does not have.
const CodeAuthRequired = -32000

// Handlers are invoked by the client's read loop.
type Handlers struct {
	// Update receives session/update notifications in wire order, on the read
	// loop itself. It must not block for long.
	Update func(SessionUpdate)

	// Permission chooses an option id from the request, or returns "" to
	// cancel. It runs on its own goroutine, so it may block on a human for as
	// long as it likes without stalling updates.
	Permission func(PermissionRequest) string
}

// Client is a live connection to an ACP agent.
type Client struct {
	handlers Handlers

	w       io.Writer
	writeMu sync.Mutex

	mu       sync.Mutex
	nextID   int
	pending  map[int]chan rpcResult
	closed   bool
	closeErr error

	cmd    *exec.Cmd
	stderr *ring
	done   chan struct{}
}

type rpcResult struct {
	result json.RawMessage
	err    error
}

// Error is a JSON-RPC error returned by the agent.
type Error struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if len(e.Data) > 0 {
		return fmt.Sprintf("acp error %d: %s (%s)", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("acp error %d: %s", e.Code, e.Message)
}

// IsAuthRequired reports whether err is the agent asking to be authenticated.
// The caller is expected to surface the agent's own login instructions rather
// than attempt a login itself.
func IsAuthRequired(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == CodeAuthRequired
}

// envelope is any inbound JSON-RPC message. Direction is decided by shape:
// method+id is a request from the agent, method alone is a notification, and
// anything else is a response to one of ours. Inbound request ids live in a
// separate id space that starts at 0 and overlaps ours, so they are never
// matched against pending calls.
type envelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

type outRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type outNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type outResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

// Spawn starts an agent process in dir and begins serving its stdio.
func Spawn(ctx context.Context, name string, args []string, dir string, h Handlers) (*Client, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("agent stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("agent stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("agent stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", name, err)
	}

	c := newClient(stdout, stdin, h)
	c.cmd = cmd
	go c.stderr.drain(stderr)
	return c, nil
}

// newClient wires a client to an arbitrary stream pair and starts reading.
func newClient(r io.Reader, w io.Writer, h Handlers) *Client {
	c := &Client{
		handlers: h,
		w:        w,
		pending:  make(map[int]chan rpcResult),
		stderr:   &ring{max: 50},
		done:     make(chan struct{}),
	}
	go c.run(r)
	return c
}

// Close terminates the agent and releases the connection. It is safe to call
// more than once.
func (c *Client) Close() error {
	c.fail(errors.New("client closed"))
	if c.cmd == nil || c.cmd.Process == nil {
		return nil
	}
	_ = c.cmd.Process.Kill()
	_ = c.cmd.Wait()
	return nil
}

// ---------------------------------------------------------------------------
// Protocol methods
// ---------------------------------------------------------------------------

// Initialize performs the handshake. opentree declares no filesystem or
// terminal capabilities: those exist for editors holding unsaved buffers, and
// an agent told the client has none simply does its own IO.
func (c *Client) Initialize(ctx context.Context, clientName, version string) (*InitializeResponse, error) {
	req := InitializeRequest{
		ProtocolVersion: ProtocolVersion,
		ClientCapabilities: ClientCapabilities{
			FS:       FileSystemCapabilities{ReadTextFile: false, WriteTextFile: false},
			Terminal: false,
		},
		ClientInfo: &Implementation{Name: clientName, Version: version},
	}
	var resp InitializeResponse
	if err := c.call(ctx, methodInitialize, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// NewSession opens a fresh conversation rooted at cwd.
func (c *Client) NewSession(ctx context.Context, cwd string) (*NewSessionResponse, error) {
	var resp NewSessionResponse
	req := NewSessionRequest{Cwd: cwd, MCPServers: []json.RawMessage{}}
	if err := c.call(ctx, methodSessionNew, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// LoadSession reopens an existing conversation. The agent replays the entire
// history as session/update notifications before this returns, so Handlers.Update
// fires many times during the call.
func (c *Client) LoadSession(ctx context.Context, sessionID, cwd string) (*LoadSessionResponse, error) {
	var resp LoadSessionResponse
	req := LoadSessionRequest{SessionID: sessionID, Cwd: cwd, MCPServers: []json.RawMessage{}}
	if err := c.call(ctx, methodSessionLoad, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// SetConfigOption changes an agent-declared control — model, reasoning effort,
// session mode — and returns the resulting set.
func (c *Client) SetConfigOption(ctx context.Context, sessionID, configID, value string) ([]ConfigOption, error) {
	var resp SetConfigOptionResponse
	req := SetConfigOptionRequest{SessionID: sessionID, ConfigID: configID, Value: value}
	if err := c.call(ctx, methodSetConfigOption, req, &resp); err != nil {
		return nil, err
	}
	return resp.ConfigOptions, nil
}

// Cancel asks the agent to stop the current turn. It is a notification, so it
// returns as soon as it is written; the in-flight Prompt then completes with
// StopCancelled rather than an error.
func (c *Client) Cancel(sessionID string) error {
	return c.write(outNotification{
		JSONRPC: "2.0",
		Method:  methodSessionCancel,
		Params:  CancelNotification{SessionID: sessionID},
	})
}

// Done is closed when the connection ends, whether through Close or because
// the agent exited on its own.
func (c *Client) Done() <-chan struct{} { return c.done }

// Prompt sends a turn and blocks until the agent stops. Everything the agent
// produces along the way arrives through Handlers.Update.
func (c *Client) Prompt(ctx context.Context, sessionID string, blocks []ContentBlock) (*PromptResponse, error) {
	var resp PromptResponse
	req := PromptRequest{SessionID: sessionID, Prompt: blocks}
	if err := c.call(ctx, methodSessionPrompt, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ---------------------------------------------------------------------------
// Transport
// ---------------------------------------------------------------------------

func (c *Client) run(r io.Reader) {
	dec := json.NewDecoder(r)
	for {
		var env envelope
		if err := dec.Decode(&env); err != nil {
			if errors.Is(err, io.EOF) {
				err = errors.New("agent closed the connection")
			}
			c.fail(err)
			return
		}
		switch {
		case env.Method != "" && len(env.ID) > 0:
			go c.serveRequest(env)
		case env.Method != "":
			c.serveNotification(env)
		default:
			c.deliver(env)
		}
	}
}

func (c *Client) serveNotification(env envelope) {
	if env.Method != methodSessionUpdate || c.handlers.Update == nil {
		return
	}
	var note sessionNotification
	if err := json.Unmarshal(env.Params, &note); err != nil {
		return
	}
	c.handlers.Update(note.Update)
}

func (c *Client) serveRequest(env envelope) {
	if env.Method != methodRequestPermission {
		// Any other agent->client request is something we declared we cannot
		// do. Answering with an empty result keeps the turn moving rather than
		// leaving the agent blocked forever.
		_ = c.write(outResponse{JSONRPC: "2.0", ID: env.ID, Result: struct{}{}})
		return
	}

	var req PermissionRequest
	if err := json.Unmarshal(env.Params, &req); err != nil {
		_ = c.write(outResponse{JSONRPC: "2.0", ID: env.ID,
			Result: permissionResponse{Outcome: permissionOutcome{Outcome: "cancelled"}}})
		return
	}

	var optionID string
	if c.handlers.Permission != nil {
		optionID = c.handlers.Permission(req)
	}

	out := permissionResponse{Outcome: permissionOutcome{Outcome: "cancelled"}}
	if optionID != "" {
		out = permissionResponse{Outcome: permissionOutcome{Outcome: "selected", OptionID: optionID}}
	}
	_ = c.write(outResponse{JSONRPC: "2.0", ID: env.ID, Result: out})
}

func (c *Client) deliver(env envelope) {
	var id int
	if err := json.Unmarshal(env.ID, &id); err != nil {
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	c.mu.Unlock()
	if !ok {
		return
	}
	if env.Error != nil {
		ch <- rpcResult{err: env.Error}
		return
	}
	ch <- rpcResult{result: env.Result}
}

func (c *Client) call(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResult, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.write(outRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case res := <-ch:
		if res.err != nil {
			return res.err
		}
		if result == nil || len(res.result) == 0 {
			return nil
		}
		return json.Unmarshal(res.result, result)
	}
}

func (c *Client) write(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.w.Write(append(data, '\n'))
	return err
}

// fail ends the connection, handing err to every in-flight call. The first
// failure wins, so a later Close can't overwrite the real cause of death.
func (c *Client) fail(err error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	if tail := c.stderr.String(); tail != "" {
		err = fmt.Errorf("%w\n%s", err, tail)
	}
	c.closeErr = err
	waiters := make([]chan rpcResult, 0, len(c.pending))
	for _, ch := range c.pending {
		waiters = append(waiters, ch)
	}
	c.mu.Unlock()

	for _, ch := range waiters {
		select {
		case ch <- rpcResult{err: err}:
		default:
		}
	}
	close(c.done)
}

// ring keeps the last max lines written to it.
type ring struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func (r *ring) drain(rd io.Reader) {
	sc := bufio.NewScanner(rd)
	for sc.Scan() {
		r.add(sc.Text())
	}
}

func (r *ring) add(line string) {
	if line == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, line)
	if len(r.lines) > r.max {
		r.lines = r.lines[len(r.lines)-r.max:]
	}
}

func (r *ring) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}
