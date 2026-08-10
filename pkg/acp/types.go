package acp

import (
	"encoding/json"
	"slices"
)

// ProtocolVersion is the ACP major version this client speaks.
const ProtocolVersion = 1

// Method names. The segment after the slash is snake_case even though every
// JSON key on the wire is camelCase.
const (
	methodInitialize        = "initialize"
	methodSessionNew        = "session/new"
	methodSessionLoad       = "session/load"
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
	LoadSession        bool               `json:"loadSession"`
	PromptCapabilities PromptCapabilities `json:"promptCapabilities"`
}

type PromptCapabilities struct {
	EmbeddedContext bool `json:"embeddedContext"`
	Image           bool `json:"image"`
}

// AuthMethod describes one way to authenticate. opencode offers exactly one,
// and its description is an instruction to run a command in a terminal rather
// than anything the client can perform itself.
type AuthMethod struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
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

// ContentBlock is one piece of a message. Text and resource links are
// modelled; opentree neither sends nor renders images yet.
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`

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
	return ContentBlock{Type: "text", Text: text}
}

// ResourceLink points the agent at a file without inlining it. This is how an
// @-mention travels: the agent reads the file itself rather than the client
// pasting its contents into the prompt.
func ResourceLink(uri, name string) ContentBlock {
	return ContentBlock{Type: "resource_link", URI: uri, Name: name}
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

type ToolCall struct {
	ToolCallID string            `json:"toolCallId"`
	Title      string            `json:"title,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Status     string            `json:"status,omitempty"`
	Locations  []Location        `json:"locations,omitempty"`
	Content    []ToolCallContent `json:"content,omitempty"`
	RawInput   json.RawMessage   `json:"rawInput,omitempty"`
	RawOutput  json.RawMessage   `json:"rawOutput,omitempty"`
}

type Location struct {
	Path string `json:"path"`
	Line int    `json:"line,omitempty"`
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
	if patch.Locations != nil {
		t.Locations = patch.Locations
	}
	// Content arrives cumulatively — each update carries the whole array — so
	// replace rather than append.
	if patch.Content != nil {
		t.Content = patch.Content
	}
	if patch.RawInput != nil {
		t.RawInput = patch.RawInput
	}
	if patch.RawOutput != nil {
		t.RawOutput = patch.RawOutput
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
