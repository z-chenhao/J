package agent

import (
	"context"
	"encoding/json"
	"time"
)

// Role identifies the sender of a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ContentType identifies one ordered message content block.
type ContentType string

const (
	ContentText      ContentType = "text"
	ContentReasoning ContentType = "reasoning"
	ContentToolCall  ContentType = "tool_call"
)

// ToolCall is a model request to invoke one registered tool.
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// Content is one ordered block in a message. Exactly one of Text or ToolCall is
// meaningful according to Type.
type Content struct {
	Type     ContentType `json:"type"`
	Text     string      `json:"text,omitempty"`
	ToolCall *ToolCall   `json:"toolCall,omitempty"`
}

// Message is the provider-neutral conversation unit used by the runtime.
// Reasoning blocks are retained because some model protocols require them for
// correct tool-call continuation; consumers decide whether to display them.
type Message struct {
	Role       Role      `json:"role"`
	Content    []Content `json:"content"`
	ToolCallID string    `json:"toolCallId,omitempty"`
	ToolName   string    `json:"toolName,omitempty"`
	IsError    bool      `json:"isError,omitempty"`
}

// Text joins all text blocks in message order.
func (m Message) Text() string {
	var text string
	for _, content := range m.Content {
		if content.Type == ContentText {
			text += content.Text
		}
	}
	return text
}

// ToolCalls returns a copy of all tool calls in message order.
func (m Message) ToolCalls() []ToolCall {
	var calls []ToolCall
	for _, content := range m.Content {
		if content.Type == ContentToolCall && content.ToolCall != nil {
			calls = append(calls, cloneToolCall(*content.ToolCall))
		}
	}
	return calls
}

// TextMessage constructs a single-text-block message.
func TextMessage(role Role, text string) Message {
	return Message{
		Role:    role,
		Content: []Content{{Type: ContentText, Text: text}},
	}
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

// StopReason is the normalized successful termination reason for a model turn.
type StopReason string

const (
	StopReasonStop          StopReason = "stop"
	StopReasonLength        StopReason = "length"
	StopReasonToolCalls     StopReason = "tool_calls"
	StopReasonContentFilter StopReason = "content_filter"
)

// Usage contains provider-reported token counts. Optional breakdowns are nil
// when a provider does not report them.
type Usage struct {
	InputTokens       int64  `json:"inputTokens"`
	OutputTokens      int64  `json:"outputTokens"`
	TotalTokens       int64  `json:"totalTokens"`
	CachedInputTokens *int64 `json:"cachedInputTokens,omitempty"`
	ReasoningTokens   *int64 `json:"reasoningTokens,omitempty"`
}

// ModelResponse is one complete provider-neutral model turn.
type ModelResponse struct {
	Message    Message    `json:"message"`
	Provider   string     `json:"provider"`
	Model      string     `json:"model"`
	ResponseID string     `json:"responseId,omitempty"`
	StopReason StopReason `json:"stopReason"`
	Usage      *Usage     `json:"usage,omitempty"`
}

// DeltaType identifies the kind of streamed model content.
type DeltaType string

const (
	DeltaText      DeltaType = "text"
	DeltaReasoning DeltaType = "reasoning"
	DeltaToolCall  DeltaType = "tool_call"
)

// ModelDelta is one incremental model output. Index is scoped to Type.
type ModelDelta struct {
	Type       DeltaType `json:"type"`
	Index      int       `json:"index"`
	Delta      string    `json:"delta"`
	ToolCallID string    `json:"toolCallId,omitempty"`
	ToolName   string    `json:"toolName,omitempty"`
}

// Model produces exactly one assistant response for each turn and may emit
// ordered deltas while doing so. Complete must call emit synchronously and must
// not call it after returning. A model that cannot stream may emit no deltas.
type Model interface {
	Complete(ctx context.Context, request ModelRequest, emit func(ModelDelta)) (ModelResponse, error)
}

// ModelObservation records comparable facts about one completed model call.
type ModelObservation struct {
	Provider   string         `json:"provider"`
	Model      string         `json:"model"`
	ResponseID string         `json:"responseId,omitempty"`
	StopReason StopReason     `json:"stopReason"`
	Usage      *Usage         `json:"usage,omitempty"`
	Duration   time.Duration  `json:"-"`
	FirstDelta *time.Duration `json:"-"`
}

// RunResult is the final message plus aggregate facts for one run.
type RunResult struct {
	Message       Message
	Usage         *Usage
	Turns         int
	ModelDuration time.Duration
	ToolDuration  time.Duration
	FirstDelta    *time.Duration
}

// EventType identifies a synchronous lifecycle event emitted during a run.
type EventType string

const (
	EventAgentStarted     EventType = "agent.started"
	EventAgentCompleted   EventType = "agent.completed"
	EventAgentFailed      EventType = "agent.failed"
	EventTurnStarted      EventType = "turn.started"
	EventTurnCompleted    EventType = "turn.completed"
	EventTurnFailed       EventType = "turn.failed"
	EventMessageStarted   EventType = "message.started"
	EventMessageDelta     EventType = "message.delta"
	EventMessageCompleted EventType = "message.completed"
	EventMessageFailed    EventType = "message.failed"
	EventToolStarted      EventType = "tool.started"
	EventToolCompleted    EventType = "tool.completed"
)

// Event is an observation of a run. Events are delivered synchronously and a
// handler should return promptly.
type Event struct {
	Type     EventType         `json:"type"`
	Message  *Message          `json:"message,omitempty"`
	Delta    *ModelDelta       `json:"delta,omitempty"`
	Model    *ModelObservation `json:"model,omitempty"`
	ToolCall *ToolCall         `json:"toolCall,omitempty"`
	Output   string            `json:"output,omitempty"`
	Duration time.Duration     `json:"-"`
	IsError  bool              `json:"isError,omitempty"`
	Error    string            `json:"error,omitempty"`
}

// EventHandler observes one run without becoming shared Agent state. A handler
// may call History, but must not call Run or Reset on the same Agent because
// handlers run synchronously while that Agent's run is active.
type EventHandler func(Event)
