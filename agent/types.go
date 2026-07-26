package agent

import (
	"context"
	"encoding/json"
)

// Role identifies the sender of a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall is a model request to invoke one registered tool.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Message is the provider-neutral conversation unit used by the runtime.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"`
	ToolName   string     `json:"toolName,omitempty"`
	IsError    bool       `json:"isError,omitempty"`
}

// ToolSpec describes a tool to a model. InputSchema must be a JSON object using
// JSON Schema semantics understood by the selected model adapter.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Tool is one executable capability available to a model.
type Tool interface {
	Spec() ToolSpec
	Call(ctx context.Context, arguments json.RawMessage) (string, error)
}

// ModelRequest contains the complete conversation and tools available for the
// next model turn.
type ModelRequest struct {
	Messages []Message  `json:"messages"`
	Tools    []ToolSpec `json:"tools,omitempty"`
}

// Model produces exactly one assistant message for each model turn.
type Model interface {
	Complete(ctx context.Context, request ModelRequest) (Message, error)
}

// EventType identifies a synchronous lifecycle event emitted during a run.
type EventType string

const (
	EventAgentStarted   EventType = "agent.started"
	EventAgentCompleted EventType = "agent.completed"
	EventAgentFailed    EventType = "agent.failed"
	EventTurnStarted    EventType = "turn.started"
	EventTurnCompleted  EventType = "turn.completed"
	EventMessageCreated EventType = "message.created"
	EventToolStarted    EventType = "tool.started"
	EventToolCompleted  EventType = "tool.completed"
)

// Event is an observation of a run. Events are delivered synchronously and a
// handler should return promptly.
type Event struct {
	Type     EventType `json:"type"`
	Message  *Message  `json:"message,omitempty"`
	ToolCall *ToolCall `json:"toolCall,omitempty"`
	Output   string    `json:"output,omitempty"`
	IsError  bool      `json:"isError,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// EventHandler observes one run without becoming shared Agent state.
type EventHandler func(Event)
