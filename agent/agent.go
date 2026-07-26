package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
)

var (
	// ErrEmptyInput is returned when a run has no user content.
	ErrEmptyInput = errors.New("input is empty")
	// ErrToolRoundLimit is returned when a model never produces a final answer.
	ErrToolRoundLimit = errors.New("tool round limit exceeded")
	// ErrEmptyModelOutput is returned when a model produces neither text nor tools.
	ErrEmptyModelOutput = errors.New("model returned an empty assistant message")
)

const defaultMaxToolRounds = 4

// Option configures an Agent through one of this package's With functions.
type Option interface {
	apply(*config) error
}

type optionFunc func(*config) error

func (option optionFunc) apply(cfg *config) error {
	return option(cfg)
}

type config struct {
	systemPrompt  string
	maxToolRounds int
	tools         []Tool
}

// WithSystemPrompt sets an optional system message prepended to each session.
func WithSystemPrompt(prompt string) Option {
	return optionFunc(func(cfg *config) error {
		cfg.systemPrompt = strings.TrimSpace(prompt)
		return nil
	})
}

// WithMaxToolRounds limits consecutive model turns that request tools.
func WithMaxToolRounds(rounds int) Option {
	return optionFunc(func(cfg *config) error {
		if rounds < 1 {
			return errors.New("max tool rounds must be positive")
		}
		cfg.maxToolRounds = rounds
		return nil
	})
}

// WithTools registers the complete immutable tool set for an Agent.
func WithTools(tools ...Tool) Option {
	return optionFunc(func(cfg *config) error {
		cfg.tools = append([]Tool(nil), tools...)
		return nil
	})
}

// Agent serializes runs over one conversation history.
type Agent struct {
	model         Model
	systemPrompt  string
	maxToolRounds int
	tools         map[string]Tool
	toolSpecs     []ToolSpec

	runMu   sync.Mutex
	stateMu sync.RWMutex
	history []Message
}

// New creates an Agent with an injected model.
func New(model Model, options ...Option) (*Agent, error) {
	if model == nil {
		return nil, errors.New("model is required")
	}

	cfg := config{maxToolRounds: defaultMaxToolRounds}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option.apply(&cfg); err != nil {
			return nil, err
		}
	}

	tools := make(map[string]Tool, len(cfg.tools))
	specs := make([]ToolSpec, 0, len(cfg.tools))
	for _, tool := range cfg.tools {
		if tool == nil {
			return nil, errors.New("tool cannot be nil")
		}
		spec := cloneToolSpec(tool.Spec())
		spec.Name = strings.TrimSpace(spec.Name)
		spec.Description = strings.TrimSpace(spec.Description)
		if spec.Name == "" {
			return nil, errors.New("tool name is required")
		}
		if _, exists := tools[spec.Name]; exists {
			return nil, fmt.Errorf("duplicate tool name %q", spec.Name)
		}
		if len(spec.InputSchema) == 0 || !json.Valid(spec.InputSchema) {
			return nil, fmt.Errorf("tool %q has invalid input schema", spec.Name)
		}
		var schemaObject map[string]json.RawMessage
		if err := json.Unmarshal(spec.InputSchema, &schemaObject); err != nil {
			return nil, fmt.Errorf("tool %q input schema must be a JSON object", spec.Name)
		}
		if schemaObject == nil {
			return nil, fmt.Errorf("tool %q input schema must be a JSON object", spec.Name)
		}
		tools[spec.Name] = tool
		specs = append(specs, spec)
	}

	return &Agent{
		model:         model,
		systemPrompt:  cfg.systemPrompt,
		maxToolRounds: cfg.maxToolRounds,
		tools:         tools,
		toolSpecs:     specs,
	}, nil
}

// Run appends one user message and advances the conversation until the model
// returns a final assistant message or the tool-round limit is reached.
func (a *Agent) Run(ctx context.Context, input string, handler EventHandler) (Message, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return Message{}, ErrEmptyInput
	}
	if ctx == nil {
		return Message{}, errors.New("context is required")
	}

	a.runMu.Lock()
	defer a.runMu.Unlock()

	messages := a.History()
	if len(messages) == 0 && a.systemPrompt != "" {
		messages = append(messages, Message{Role: RoleSystem, Content: a.systemPrompt})
	}
	messages = append(messages, Message{Role: RoleUser, Content: input})
	a.storeHistory(messages)
	emit(handler, Event{Type: EventAgentStarted})

	for round := 0; round < a.maxToolRounds; round++ {
		if err := ctx.Err(); err != nil {
			emit(handler, Event{Type: EventAgentFailed, Error: err.Error()})
			return Message{}, err
		}

		emit(handler, Event{Type: EventTurnStarted})
		output, err := a.model.Complete(ctx, ModelRequest{
			Messages: cloneMessages(messages),
			Tools:    cloneToolSpecs(a.toolSpecs),
		})
		if err != nil {
			emit(handler, Event{Type: EventAgentFailed, Error: err.Error()})
			return Message{}, err
		}
		if output.Role != RoleAssistant {
			err := fmt.Errorf("model returned role %q, want %q", output.Role, RoleAssistant)
			emit(handler, Event{Type: EventAgentFailed, Error: err.Error()})
			return Message{}, err
		}

		output = cloneMessage(output)
		if err := validateToolCalls(output.ToolCalls); err != nil {
			emit(handler, Event{Type: EventAgentFailed, Error: err.Error()})
			return Message{}, err
		}
		if len(output.ToolCalls) == 0 && strings.TrimSpace(output.Content) == "" {
			emit(handler, Event{Type: EventAgentFailed, Error: ErrEmptyModelOutput.Error()})
			return Message{}, ErrEmptyModelOutput
		}
		messages = append(messages, output)
		a.storeHistory(messages)
		outputEvent := cloneMessage(output)
		emit(handler, Event{Type: EventMessageCreated, Message: &outputEvent})

		if len(output.ToolCalls) == 0 {
			emit(handler, Event{Type: EventTurnCompleted})
			emit(handler, Event{Type: EventAgentCompleted})
			return output, nil
		}

		for _, toolCall := range output.ToolCalls {
			call := cloneToolCall(toolCall)
			emit(handler, Event{Type: EventToolStarted, ToolCall: &call})

			result := Message{
				Role:       RoleTool,
				ToolCallID: call.ID,
				ToolName:   call.Name,
			}
			tool, ok := a.tools[call.Name]
			if !ok {
				result.Content = fmt.Sprintf("tool %q is not registered", call.Name)
				result.IsError = true
				messages = append(messages, result)
				a.storeHistory(messages)
				emit(handler, Event{
					Type:     EventToolCompleted,
					ToolCall: &call,
					Output:   result.Content,
					IsError:  true,
					Error:    result.Content,
				})
				continue
			}

			toolOutput, callErr := tool.Call(ctx, append(json.RawMessage(nil), call.Arguments...))
			result.Content = toolOutput
			if callErr != nil {
				result.IsError = true
				if result.Content == "" {
					result.Content = callErr.Error()
				}
			}
			messages = append(messages, result)
			a.storeHistory(messages)
			event := Event{
				Type:     EventToolCompleted,
				ToolCall: &call,
				Output:   result.Content,
				IsError:  callErr != nil,
			}
			if callErr != nil {
				event.Error = callErr.Error()
			}
			emit(handler, event)
		}
		emit(handler, Event{Type: EventTurnCompleted})
	}

	emit(handler, Event{Type: EventAgentFailed, Error: ErrToolRoundLimit.Error()})
	return Message{}, ErrToolRoundLimit
}

func validateToolCalls(calls []ToolCall) error {
	ids := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		if strings.TrimSpace(call.ID) == "" {
			return errors.New("model returned a tool call without an id")
		}
		if strings.TrimSpace(call.Name) == "" {
			return fmt.Errorf("tool call %q has no name", call.ID)
		}
		if _, exists := ids[call.ID]; exists {
			return fmt.Errorf("model returned duplicate tool call id %q", call.ID)
		}
		ids[call.ID] = struct{}{}
		if len(call.Arguments) == 0 || !json.Valid(call.Arguments) {
			return fmt.Errorf("tool call %q has invalid JSON arguments", call.ID)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(call.Arguments, &object); err != nil || object == nil {
			return fmt.Errorf("tool call %q arguments must be a JSON object", call.ID)
		}
	}
	return nil
}

// History returns a deep snapshot of the current conversation.
func (a *Agent) History() []Message {
	a.stateMu.RLock()
	defer a.stateMu.RUnlock()
	return cloneMessages(a.history)
}

// Reset clears conversation history after any active run finishes.
func (a *Agent) Reset() {
	a.runMu.Lock()
	defer a.runMu.Unlock()
	a.storeHistory(nil)
}

func (a *Agent) storeHistory(messages []Message) {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	a.history = cloneMessages(messages)
}

func emit(handler EventHandler, event Event) {
	if handler != nil {
		handler(event)
	}
}

func cloneMessages(messages []Message) []Message {
	cloned := make([]Message, len(messages))
	for i, message := range messages {
		cloned[i] = cloneMessage(message)
	}
	return cloned
}

func cloneMessage(message Message) Message {
	cloned := message
	cloned.ToolCalls = make([]ToolCall, len(message.ToolCalls))
	for i, call := range message.ToolCalls {
		cloned.ToolCalls[i] = cloneToolCall(call)
	}
	return cloned
}

func cloneToolCall(call ToolCall) ToolCall {
	cloned := call
	cloned.Arguments = append(json.RawMessage(nil), call.Arguments...)
	return cloned
}

func cloneToolSpecs(specs []ToolSpec) []ToolSpec {
	cloned := make([]ToolSpec, len(specs))
	for i, spec := range specs {
		cloned[i] = cloneToolSpec(spec)
	}
	return cloned
}

func cloneToolSpec(spec ToolSpec) ToolSpec {
	cloned := spec
	cloned.InputSchema = append(json.RawMessage(nil), spec.InputSchema...)
	return cloned
}
