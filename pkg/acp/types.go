package acp

import (
	"encoding/json"
	"slices"
	"time"
)

// ProtocolVersion is the ACP major version this client speaks.
const ProtocolVersion = 1

// Method names. The segment after the slash is snake_case even though every
// JSON key on the wire is camelCase.
const (
	methodInitialize        = "initialize"
	methodAuthenticate      = "authenticate"
	methodSessionNew        = "session/new"
	methodSessionLoad       = "session/load"
	methodSessionResume     = "session/resume"
	methodSessionList       = "session/list"
	methodSessionClose      = "session/close"
	methodSessionPrompt     = "session/prompt"
	methodSessionCancel     = "session/cancel"
	methodSetConfigOption   = "session/set_config_option"
	methodRequestPermission = "session/request_permission"
	methodSessionUpdate     = "session/update"
)

// ---------------------------------------------------------------------------
// initialize
// ---------------------------------------------------------------------------

type InitializeRequest struct {
	ProtocolVersion    int                `json:"protocolVersion"`
	ClientCapabilities ClientCapabilities `json:"clientCapabilities"`
	ClientInfo         *Implementation    `json:"clientInfo,omitempty"`
}

// ClientCapabilities declares which client-side methods the agent may call.
// Capabilities omitted from initialize must be treated as unsupported, so none
// of these carry omitempty — sending an explicit false is the entire point.
type ClientCapabilities struct {
	FS       FileSystemCapabilities `json:"fs"`
	Terminal bool                   `json:"terminal"`
}

type FileSystemCapabilities struct {
	ReadTextFile  bool `json:"readTextFile"`
	WriteTextFile bool `json:"writeTextFile"`
}

// Implementation identifies a client or agent build.
type Implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Title   string `json:"title,omitempty"`
}

type InitializeResponse struct {
	ProtocolVersion   int               `json:"protocolVersion"`
	AgentCapabilities AgentCapabilities `json:"agentCapabilities"`
	AuthMethods       []AuthMethod      `json:"authMethods"`
	AgentInfo         *Implementation   `json:"agentInfo,omitempty"`
}

type AgentCapabilities struct {
	LoadSession         bool                `json:"loadSession"`
	PromptCapabilities  PromptCapabilities  `json:"promptCapabilities"`
	SessionCapabilities SessionCapabilities `json:"sessionCapabilities"`
}

type PromptCapabilities struct {
	EmbeddedContext bool `json:"embeddedContext"`
	Image           bool `json:"image"`
}

// SessionCapabilities are the optional session methods an agent serves. ACP
// spells each as presence rather than a boolean — `{}` means supported, null or
// omitted means not — which is why they are pointers rather than bools.
//
// session/load is the exception: it stays on the top-level loadSession flag,
// and the schema says the two will be unified in a later version of the
// protocol. CanReopen hides the split, so that day costs one function.
//
// Only the ones opentree acts on are decoded. session/fork is deliberately
// absent: the schema marks it UNSTABLE, "may be removed or changed at any
// point", which is not something to build a command on.
type SessionCapabilities struct {
	List   *Capability `json:"list,omitempty"`
	Resume *Capability `json:"resume,omitempty"`
	Close  *Capability `json:"close,omitempty"`
}

// Capability is an ACP capability object. It carries nothing — being there is
// the entire signal.
type Capability struct{}

// CanList reports whether the agent will enumerate the conversations it keeps.
func (c AgentCapabilities) CanList() bool { return c.SessionCapabilities.List != nil }

// CanReopen reports whether an existing conversation can be opened at all, by
// either of the two methods that do it.
func (c AgentCapabilities) CanReopen() bool {
	return c.LoadSession || c.SessionCapabilities.Resume != nil
}

// CanClose reports whether the agent takes being told a conversation is over.
func (c AgentCapabilities) CanClose() bool { return c.SessionCapabilities.Close != nil }

// AuthMethod describes one way to authenticate. Some are a login the client
// asks the agent to perform — Gemini offers four, and its Google one opens a
// browser from inside the agent's own process — and some are an instruction to
// go and run a command, which opencode's description says in prose.
type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Meta is the agent's own extension block, kept raw because every agent
	// puts something different in it and none of it is in the schema.
	Meta json.RawMessage `json:"_meta,omitempty"`
}

// TerminalAuth is the command this method wants run in a terminal, from the
// terminal-auth extension Copilot sends. It carries the running agent's own
// absolute path, which beats a bare name: a binary reachable by the agent is
// not necessarily on the PATH opentree was started with.
//
// Executing it is not a capability opentree is granting away. The agent is
// already running as the user and could spawn it unasked; what a client adds is
// a terminal to run it in, which is the whole reason the extension exists.
func (a AuthMethod) TerminalAuth() (command string, args []string, ok bool) {
	if len(a.Meta) == 0 {
		return "", nil, false
	}
	var meta struct {
		Terminal *struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"terminal-auth"`
	}
	if err := json.Unmarshal(a.Meta, &meta); err != nil || meta.Terminal == nil {
		return "", nil, false
	}
	if meta.Terminal.Command == "" {
		return "", nil, false
	}
	return meta.Terminal.Command, meta.Terminal.Args, true
}

// AuthenticateRequest asks the agent to log itself in one of the ways it
// declared at initialize. The id is all it carries: ACP has nowhere to put a
// key or a password, so a method that needs one reads it from the environment
// the agent was started in.
type AuthenticateRequest struct {
	MethodID string `json:"methodId"`
}

// ---------------------------------------------------------------------------
// sessions
// ---------------------------------------------------------------------------

type NewSessionRequest struct {
	Cwd string `json:"cwd"`
	// MCPServers is required by the schema even when empty. opentree never
	// configures MCP servers; the agent's own config does.
	MCPServers []json.RawMessage `json:"mcpServers"`
}

type NewSessionResponse struct {
	SessionID     string         `json:"sessionId"`
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

type LoadSessionRequest struct {
	SessionID  string            `json:"sessionId"`
	Cwd        string            `json:"cwd"`
	MCPServers []json.RawMessage `json:"mcpServers"`
}

// LoadSessionResponse arrives only after the agent has replayed the whole
// conversation as session/update notifications.
type LoadSessionResponse struct {
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

// ResumeSessionRequest reopens a conversation without replaying it. It is the
// same shape as a load; what differs is that the agent owes no history.
type ResumeSessionRequest struct {
	SessionID  string            `json:"sessionId"`
	Cwd        string            `json:"cwd"`
	MCPServers []json.RawMessage `json:"mcpServers"`
}

type ResumeSessionResponse struct {
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

// ListSessionsRequest asks for the conversations an agent still has. Cwd
// narrows them to one worktree, which is the only scope a chat can offer:
// every session opentree opens is rooted in the worktree it belongs to.
type ListSessionsRequest struct {
	Cwd string `json:"cwd,omitempty"`
}

type ListSessionsResponse struct {
	Sessions []SessionInfo `json:"sessions"`
}

// CloseSessionRequest ends one conversation. The agent keeps it — see
// CloseSession for the one exception the wire has.
type CloseSessionRequest struct {
	SessionID string `json:"sessionId"`
}

// SessionInfo is one conversation the agent kept. Title is its own summary of
// what was discussed — the thing that makes a list of ids worth showing.
//
// Nothing about the order is promised, so callers sort by UpdatedAt themselves.
type SessionInfo struct {
	SessionID string    `json:"sessionId"`
	Cwd       string    `json:"cwd,omitempty"`
	Title     string    `json:"title,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
}

// ConfigOption is an agent-declared control — model, reasoning effort, session
// mode. Not part of base ACP; agents that don't send it simply have none.
type ConfigOption struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Description  string              `json:"description,omitempty"`
	Category     string              `json:"category,omitempty"`
	Type         string              `json:"type,omitempty"`
	CurrentValue string              `json:"currentValue,omitempty"`
	Options      []ConfigOptionValue `json:"options,omitempty"`
}

// SetConfigOptionRequest changes one agent-declared control.
//
// The field is configId, not optionId — the latter belongs to permission
// options, and sending it yields "expected string, received undefined".
type SetConfigOptionRequest struct {
	SessionID string `json:"sessionId"`
	ConfigID  string `json:"configId"`
	Value     string `json:"value"`
}

// SetConfigOptionResponse returns the full set, since changing one option can
// change another: picking a model narrows which effort levels it supports.
type SetConfigOptionResponse struct {
	ConfigOptions []ConfigOption `json:"configOptions,omitempty"`
}

type ConfigOptionValue struct {
	Value       string `json:"value"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ---------------------------------------------------------------------------
// prompt turn
// ---------------------------------------------------------------------------

type PromptRequest struct {
	SessionID string         `json:"sessionId"`
	Prompt    []ContentBlock `json:"prompt"`
}

type PromptResponse struct {
	StopReason string      `json:"stopReason"`
	Usage      *TokenUsage `json:"usage,omitempty"`
}

// Stop reasons. Others pass through as opaque strings.
const (
	StopEndTurn         = "end_turn"
	StopCancelled       = "cancelled"
	StopMaxTokens       = "max_tokens"
	StopMaxTurnRequests = "max_turn_requests"
	StopRefusal         = "refusal"
)

type TokenUsage struct {
	InputTokens       int `json:"inputTokens"`
	OutputTokens      int `json:"outputTokens"`
	TotalTokens       int `json:"totalTokens"`
	CachedReadTokens  int `json:"cachedReadTokens"`
	CachedWriteTokens int `json:"cachedWriteTokens"`
}

type CancelNotification struct {
	SessionID string `json:"sessionId"`
}

// Content block types opentree handles. audio and resource are deliberately
// absent: neither agent advertises audio, and an embedded resource inlines a
// whole file where a resource_link would have the agent read only what it needs.
const (
	BlockText         = "text"
	BlockImage        = "image"
	BlockResourceLink = "resource_link"
)

// ContentBlock is one piece of a message.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

	// image. Data is base64. MimeType is also a resource_link field, which is
	// why it is not nested with the rest of the image ones.
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`

	// resource_link
	URI  string `json:"uri,omitempty"`
	Name string `json:"name,omitempty"`

	Annotations *Annotations `json:"annotations,omitempty"`
}

// Annotations qualify a block. Audience is the one a client has to obey: a
// replayed conversation carries blocks the agent addressed to itself — the
// input it handed a tool, whole files it inlined — and showing those hands the
// reader a conversation nobody had.
type Annotations struct {
	Audience []string `json:"audience,omitempty"`
}

// RoleUser is the audience meaning "the person reading".
const RoleUser = "user"

// ForUser reports whether a block is meant for the reader. A block with no
// audience is for everyone, which is every block outside a replay.
func (c ContentBlock) ForUser() bool {
	if c.Annotations == nil || len(c.Annotations.Audience) == 0 {
		return true
	}
	return slices.Contains(c.Annotations.Audience, RoleUser)
}

func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: BlockText, Text: text}
}

// ResourceLink points the agent at a file without inlining it. This is how an
// @-mention travels: the agent reads the file itself rather than the client
// pasting its contents into the prompt.
func ResourceLink(uri, name string) ContentBlock {
	return ContentBlock{Type: BlockResourceLink, URI: uri, Name: name}
}

// ImageBlock inlines an image, which a link cannot replace: fs/read_text_file
// is text-only, so a link to a PNG leaves the agent nothing to read.
//
// Only send one to an agent whose promptCapabilities.image is set — the spec
// says a client MUST restrict content to the capabilities it was told about.
func ImageBlock(data, mimeType string) ContentBlock {
	return ContentBlock{Type: BlockImage, Data: data, MimeType: mimeType}
}

// ---------------------------------------------------------------------------
// session/update
// ---------------------------------------------------------------------------

// Update kinds seen on the wire. usage_update is an opencode extension rather
// than base ACP; agents that don't send it just never produce one.
const (
	UpdateAgentMessage   = "agent_message_chunk"
	UpdateAgentThought   = "agent_thought_chunk"
	UpdateUserMessage    = "user_message_chunk"
	UpdateToolCall       = "tool_call"
	UpdateToolCallUpdate = "tool_call_update"
	UpdateCommands       = "available_commands_update"
	UpdateUsage          = "usage_update"
	UpdatePlan           = "plan"
	UpdateMode           = "current_mode_update"
	UpdateConfigOptions  = "config_option_update"
)

// PlanEntry is one step of the agent's plan. Captured from claude-agent-acp
// 0.66.0: the whole list arrives on every change, not a delta, so a client
// replaces its copy rather than appending to it.
type PlanEntry struct {
	Content  string `json:"content"`
	Status   string `json:"status"`
	Priority string `json:"priority,omitempty"`
}

// Plan entry statuses.
const (
	PlanPending    = "pending"
	PlanInProgress = "in_progress"
	PlanCompleted  = "completed"
)

// SessionUpdate is one notification from the agent. At most one of the pointer
// fields is set, selected by Type; an unrecognized Type leaves them all nil so
// a newer agent can't break an older client.
type SessionUpdate struct {
	Type string

	Message  *MessageChunk
	ToolCall *ToolCall
	Commands []Command
	Usage    *ContextUsage
	Plan     []PlanEntry

	// CurrentModeID and ConfigOptions carry a change the agent made itself
	// rather than one the client asked for — answering its own plan-mode
	// dialog, or narrowing the effort levels after a model switch.
	CurrentModeID string
	ConfigOptions []ConfigOption
}

// sessionNotification is the params object wrapping every update.
type sessionNotification struct {
	SessionID string        `json:"sessionId"`
	Update    SessionUpdate `json:"update"`
}

// UnmarshalJSON decodes the update union. A custom decoder is unavoidable:
// "content" is an object on message chunks and an array on tool calls, so the
// two cannot share one struct field.
func (u *SessionUpdate) UnmarshalJSON(data []byte) error {
	var head struct {
		Type string `json:"sessionUpdate"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return err
	}
	u.Type = head.Type

	switch head.Type {
	case UpdateAgentMessage, UpdateAgentThought, UpdateUserMessage:
		var m MessageChunk
		if err := json.Unmarshal(data, &m); err != nil {
			return err
		}
		u.Message = &m
	case UpdateToolCall, UpdateToolCallUpdate:
		var t ToolCall
		if err := json.Unmarshal(data, &t); err != nil {
			return err
		}
		u.ToolCall = &t
	case UpdateCommands:
		var c struct {
			AvailableCommands []Command `json:"availableCommands"`
		}
		if err := json.Unmarshal(data, &c); err != nil {
			return err
		}
		u.Commands = c.AvailableCommands
	case UpdateUsage:
		var g ContextUsage
		if err := json.Unmarshal(data, &g); err != nil {
			return err
		}
		u.Usage = &g
	case UpdatePlan:
		var p struct {
			Entries []PlanEntry `json:"entries"`
		}
		if err := json.Unmarshal(data, &p); err != nil {
			return err
		}
		u.Plan = p.Entries
	case UpdateMode:
		var mode struct {
			CurrentModeID string `json:"currentModeId"`
		}
		if err := json.Unmarshal(data, &mode); err != nil {
			return err
		}
		u.CurrentModeID = mode.CurrentModeID
	case UpdateConfigOptions:
		var cfg struct {
			ConfigOptions []ConfigOption `json:"configOptions"`
		}
		if err := json.Unmarshal(data, &cfg); err != nil {
			return err
		}
		u.ConfigOptions = cfg.ConfigOptions
	}
	return nil
}

type MessageChunk struct {
	MessageID string       `json:"messageId"`
	Content   ContentBlock `json:"content"`
}

type Command struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ContextUsage reports context-window consumption and running cost.
type ContextUsage struct {
	Used int   `json:"used"`
	Size int   `json:"size"`
	Cost *Cost `json:"cost,omitempty"`
}

type Cost struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
}

// ---------------------------------------------------------------------------
// tool calls
// ---------------------------------------------------------------------------

// Tool call statuses.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
)

// ToolCall carries only the fields opentree acts on. The wire also sends
// locations, rawInput and rawOutput; they are left undecoded until something
// renders them.
type ToolCall struct {
	ToolCallID string            `json:"toolCallId"`
	Title      string            `json:"title,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Status     string            `json:"status,omitempty"`
	Content    []ToolCallContent `json:"content,omitempty"`
}

// ToolCallContent is either a wrapped content block ("content") or a file diff
// ("diff"). The wrapping is real: a text block arrives as
// {"type":"content","content":{"type":"text","text":"…"}}.
type ToolCallContent struct {
	Type    string        `json:"type"`
	Content *ContentBlock `json:"content,omitempty"`
	Path    string        `json:"path,omitempty"`
	OldText string        `json:"oldText,omitempty"`
	NewText string        `json:"newText,omitempty"`
}

// Merge applies a tool_call_update patch onto retained state. Absent fields are
// left alone: agents routinely omit kind and title on the terminal update, so
// replacing wholesale would blank a row exactly as it finishes.
//
// ponytail: empty string means "absent". No ACP field has a meaningful empty
// value, which saves threading a pointer through every field. If one ever does,
// switch this struct to pointers rather than adding a parallel presence mask.
func (t *ToolCall) Merge(patch ToolCall) {
	if patch.Title != "" {
		t.Title = patch.Title
	}
	if patch.Kind != "" {
		t.Kind = patch.Kind
	}
	if patch.Status != "" {
		t.Status = patch.Status
	}
	// Content arrives cumulatively — each update carries the whole array — so
	// replace rather than append.
	if patch.Content != nil {
		t.Content = patch.Content
	}
}

// ---------------------------------------------------------------------------
// permissions
// ---------------------------------------------------------------------------

// Permission option kinds. Agents choose which to offer — opencode omits
// reject_always — so render the options received rather than a fixed set.
const (
	PermissionAllowOnce   = "allow_once"
	PermissionAllowAlways = "allow_always"
	PermissionRejectOnce  = "reject_once"
)

type PermissionRequest struct {
	SessionID string             `json:"sessionId"`
	ToolCall  ToolCall           `json:"toolCall"`
	Options   []PermissionOption `json:"options"`
}

type PermissionOption struct {
	OptionID string `json:"optionId"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}

type permissionResponse struct {
	Outcome permissionOutcome `json:"outcome"`
}

type permissionOutcome struct {
	Outcome  string `json:"outcome"`
	OptionID string `json:"optionId,omitempty"`
}
