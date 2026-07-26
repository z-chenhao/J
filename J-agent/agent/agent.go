package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	// ErrEmptyInput is returned when a run has no user content.
	ErrEmptyInput = errors.New("input is empty")
	// ErrToolRoundLimit is returned when a model requests another tool after the
	// configured number of tool rounds has completed.
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
	history       []Message
}

// WithSystemPrompt sets an optional system message prepended to each session.
func WithSystemPrompt(prompt string) Option {
	return optionFunc(func(cfg *config) error {
		cfg.systemPrompt = strings.TrimSpace(prompt)
		return nil
	})
}

// WithMaxToolRounds limits completed model/tool rounds. A final model turn is
// still allowed after the last permitted tool round.
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

// WithHistory restores a complete conversation snapshot produced by History.
// History is validated and copied during construction. It cannot be combined
// with a non-empty system prompt because restored history is authoritative,
// including any system message.
func WithHistory(history ...Message) Option {
	return optionFunc(func(cfg *config) error {
		cfg.history = cloneMessages(history)
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
	if len(cfg.history) > 0 && cfg.systemPrompt != "" {
		return nil, errors.New("system prompt cannot be combined with restored history")
	}
	if err := validateHistory(cfg.history); err != nil {
		return nil, fmt.Errorf("invalid history: %w", err)
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
		if err := json.Unmarshal(spec.InputSchema, &schemaObject); err != nil || schemaObject == nil {
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
		history:       cloneMessages(cfg.history),
	}, nil
}

// Run appends one user message and advances the conversation until the model
// returns a final assistant message or requests a tool beyond the configured
// tool-round limit.
func (a *Agent) Run(ctx context.Context, input string, handler EventHandler) (RunResult, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return RunResult{}, ErrEmptyInput
	}
	if ctx == nil {
		return RunResult{}, errors.New("context is required")
	}

	a.runMu.Lock()
	defer a.runMu.Unlock()

	messages := a.History()
	if len(messages) == 0 && a.systemPrompt != "" {
		messages = append(messages, TextMessage(RoleSystem, a.systemPrompt))
	}
	messages = append(messages, TextMessage(RoleUser, input))
	a.storeHistory(messages)
	emit(handler, Event{Type: EventAgentStarted})

	var result RunResult
	toolRounds := 0
	for {
		if err := ctx.Err(); err != nil {
			emit(handler, Event{Type: EventAgentFailed, Error: err.Error()})
			return result, err
		}

		emit(handler, Event{Type: EventTurnStarted})
		messageStarted := false
		startMessage := func() {
			if !messageStarted {
				messageStarted = true
				emit(handler, Event{Type: EventMessageStarted})
			}
		}
		modelStarted := time.Now()
		var firstDelta *time.Duration
		response, err := a.model.Complete(ctx, ModelRequest{
			Messages: cloneMessages(messages),
			Tools:    cloneToolSpecs(a.toolSpecs),
		}, func(delta ModelDelta) {
			if firstDelta == nil {
				duration := time.Since(modelStarted)
				firstDelta = &duration
			}
			startMessage()
			copied := cloneDelta(delta)
			emit(handler, Event{Type: EventMessageDelta, Delta: &copied})
		})
		modelDuration := time.Since(modelStarted)
		result.ModelDuration += modelDuration
		if result.FirstDelta == nil && firstDelta != nil {
			result.FirstDelta = cloneDuration(firstDelta)
		}
		if err != nil {
			if messageStarted {
				emit(handler, Event{Type: EventMessageFailed, Duration: modelDuration, Error: err.Error()})
			}
			emit(handler, Event{Type: EventTurnFailed, Duration: modelDuration, Error: err.Error()})
			emit(handler, Event{Type: EventAgentFailed, Error: err.Error()})
			return result, err
		}
		startMessage()
		addUsage(&result.Usage, response.Usage)

		output := cloneMessage(response.Message)
		if err := validateModelResponse(response, output); err != nil {
			emit(handler, Event{Type: EventMessageFailed, Duration: modelDuration, Error: err.Error()})
			emit(handler, Event{Type: EventTurnFailed, Duration: modelDuration, Error: err.Error()})
			emit(handler, Event{Type: EventAgentFailed, Error: err.Error()})
			return result, err
		}
		calls := output.ToolCalls()
		if err := validateToolCalls(calls); err != nil {
			emit(handler, Event{Type: EventMessageFailed, Duration: modelDuration, Error: err.Error()})
			emit(handler, Event{Type: EventTurnFailed, Duration: modelDuration, Error: err.Error()})
			emit(handler, Event{Type: EventAgentFailed, Error: err.Error()})
			return result, err
		}
		if len(calls) == 0 && strings.TrimSpace(output.Text()) == "" {
			emit(handler, Event{
				Type:     EventMessageFailed,
				Duration: modelDuration,
				Error:    ErrEmptyModelOutput.Error(),
			})
			emit(handler, Event{
				Type:     EventTurnFailed,
				Duration: modelDuration,
				Error:    ErrEmptyModelOutput.Error(),
			})
			emit(handler, Event{Type: EventAgentFailed, Error: ErrEmptyModelOutput.Error()})
			return result, ErrEmptyModelOutput
		}
		if len(calls) > 0 && toolRounds >= a.maxToolRounds {
			emit(handler, Event{
				Type:     EventMessageFailed,
				Duration: modelDuration,
				Error:    ErrToolRoundLimit.Error(),
			})
			emit(handler, Event{
				Type:     EventTurnFailed,
				Duration: modelDuration,
				Error:    ErrToolRoundLimit.Error(),
			})
			emit(handler, Event{Type: EventAgentFailed, Error: ErrToolRoundLimit.Error()})
			return result, ErrToolRoundLimit
		}

		messages = append(messages, output)
		a.storeHistory(messages)
		outputEvent := cloneMessage(output)
		emit(handler, Event{Type: EventMessageCompleted, Message: &outputEvent})

		observation := ModelObservation{
			Provider:   response.Provider,
			Model:      response.Model,
			ResponseID: response.ResponseID,
			StopReason: response.StopReason,
			Usage:      cloneUsage(response.Usage),
			Duration:   modelDuration,
			FirstDelta: cloneDuration(firstDelta),
		}
		result.Message = output
		result.Turns++

		if len(calls) == 0 {
			emit(handler, Event{
				Type:     EventTurnCompleted,
				Model:    &observation,
				Duration: modelDuration,
			})
			emit(handler, Event{Type: EventAgentCompleted})
			return result, nil
		}

		for _, toolCall := range calls {
			call := cloneToolCall(toolCall)
			emit(handler, Event{Type: EventToolStarted, ToolCall: &call})
			toolStarted := time.Now()

			toolOutput, callErr := a.executeTool(ctx, call)
			toolDuration := time.Since(toolStarted)
			result.ToolDuration += toolDuration
			toolResult := TextMessage(RoleTool, toolOutput)
			toolResult.ToolCallID = call.ID
			toolResult.ToolName = call.Name
			toolResult.IsError = callErr != nil
			messages = append(messages, toolResult)
			a.storeHistory(messages)

			event := Event{
				Type:     EventToolCompleted,
				ToolCall: &call,
				Output:   toolOutput,
				Duration: toolDuration,
				IsError:  callErr != nil,
			}
			if callErr != nil {
				event.Error = callErr.Error()
			}
			emit(handler, event)
		}
		toolRounds++
		emit(handler, Event{
			Type:     EventTurnCompleted,
			Model:    &observation,
			Duration: modelDuration,
		})
	}
}

func (a *Agent) executeTool(ctx context.Context, call ToolCall) (string, error) {
	tool, ok := a.tools[call.Name]
	if !ok {
		err := fmt.Errorf("tool %q is not registered", call.Name)
		return err.Error(), err
	}

	output, err := tool.Call(ctx, append(json.RawMessage(nil), call.Arguments...))
	if err != nil && output == "" {
		output = err.Error()
	}
	return output, err
}

func validateModelResponse(response ModelResponse, message Message) error {
	if message.Role != RoleAssistant {
		return fmt.Errorf("model returned role %q, want %q", message.Role, RoleAssistant)
	}
	if strings.TrimSpace(response.Provider) == "" {
		return errors.New("model response provider is required")
	}
	if strings.TrimSpace(response.Model) == "" {
		return errors.New("model response model is required")
	}
	switch response.StopReason {
	case StopReasonStop, StopReasonLength, StopReasonToolCalls, StopReasonContentFilter:
	default:
		return fmt.Errorf("model returned unsupported stop reason %q", response.StopReason)
	}
	for index, content := range message.Content {
		switch content.Type {
		case ContentText, ContentReasoning:
			if content.ToolCall != nil {
				return fmt.Errorf("model content %d has an unexpected tool call", index)
			}
		case ContentToolCall:
			if content.ToolCall == nil {
				return fmt.Errorf("model content %d has no tool call", index)
			}
			if content.Text != "" {
				return fmt.Errorf("model content %d has unexpected text", index)
			}
		default:
			return fmt.Errorf("model content %d has unsupported type %q", index, content.Type)
		}
	}
	if response.StopReason == StopReasonToolCalls && len(message.ToolCalls()) == 0 {
		return errors.New("model stopped for tool calls without returning a tool call")
	}
	if response.StopReason != StopReasonToolCalls && len(message.ToolCalls()) > 0 {
		return fmt.Errorf("model returned tool calls with stop reason %q", response.StopReason)
	}
	return nil
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

func validateHistory(messages []Message) error {
	pendingCalls := make(map[string]string)
	seenCallIDs := make(map[string]struct{})
	var previousRole Role

	for index, message := range messages {
		if err := validateHistoryMessage(index, message); err != nil {
			return err
		}

		switch message.Role {
		case RoleSystem:
			if index != 0 {
				return fmt.Errorf("message %d: system message must be first", index)
			}
		case RoleUser:
			if len(pendingCalls) > 0 {
				return fmt.Errorf("message %d: user message precedes tool results", index)
			}
		case RoleAssistant:
			if previousRole == "" || previousRole == RoleSystem {
				return fmt.Errorf("message %d: assistant message has no preceding user message or tool result", index)
			}
			if previousRole == RoleAssistant {
				return fmt.Errorf("message %d: consecutive assistant messages are not a valid runtime transcript", index)
			}
			if len(pendingCalls) > 0 {
				return fmt.Errorf("message %d: assistant message precedes tool results", index)
			}
			calls := message.ToolCalls()
			if err := validateToolCalls(calls); err != nil {
				return fmt.Errorf("message %d: %w", index, err)
			}
			for _, call := range calls {
				if _, exists := seenCallIDs[call.ID]; exists {
					return fmt.Errorf("message %d: duplicate tool call id %q in history", index, call.ID)
				}
				seenCallIDs[call.ID] = struct{}{}
				pendingCalls[call.ID] = call.Name
			}
		case RoleTool:
			name, exists := pendingCalls[message.ToolCallID]
			if !exists {
				return fmt.Errorf("message %d: tool result %q has no pending tool call", index, message.ToolCallID)
			}
			if message.ToolName != name {
				return fmt.Errorf(
					"message %d: tool result %q names %q, want %q",
					index,
					message.ToolCallID,
					message.ToolName,
					name,
				)
			}
			delete(pendingCalls, message.ToolCallID)
		}
		previousRole = message.Role
	}

	if len(pendingCalls) > 0 {
		return errors.New("history ends before all tool calls have results")
	}
	return nil
}

func validateHistoryMessage(index int, message Message) error {
	if len(message.Content) == 0 {
		return fmt.Errorf("message %d: content is required", index)
	}

	switch message.Role {
	case RoleSystem, RoleUser:
		if message.ToolCallID != "" || message.ToolName != "" || message.IsError {
			return fmt.Errorf("message %d: role %q has tool-result metadata", index, message.Role)
		}
	case RoleAssistant:
		if message.ToolCallID != "" || message.ToolName != "" || message.IsError {
			return fmt.Errorf("message %d: assistant message has tool-result metadata", index)
		}
	case RoleTool:
		if strings.TrimSpace(message.ToolCallID) == "" {
			return fmt.Errorf("message %d: tool result call id is required", index)
		}
		if strings.TrimSpace(message.ToolName) == "" {
			return fmt.Errorf("message %d: tool result name is required", index)
		}
	default:
		return fmt.Errorf("message %d: unsupported role %q", index, message.Role)
	}

	for contentIndex, content := range message.Content {
		switch content.Type {
		case ContentText:
			if content.ToolCall != nil {
				return fmt.Errorf("message %d content %d: text has an unexpected tool call", index, contentIndex)
			}
		case ContentReasoning:
			if message.Role != RoleAssistant {
				return fmt.Errorf("message %d content %d: reasoning requires assistant role", index, contentIndex)
			}
			if content.ToolCall != nil {
				return fmt.Errorf("message %d content %d: reasoning has an unexpected tool call", index, contentIndex)
			}
		case ContentToolCall:
			if message.Role != RoleAssistant {
				return fmt.Errorf("message %d content %d: tool call requires assistant role", index, contentIndex)
			}
			if content.ToolCall == nil {
				return fmt.Errorf("message %d content %d: tool call is missing", index, contentIndex)
			}
			if content.Text != "" {
				return fmt.Errorf("message %d content %d: tool call has unexpected text", index, contentIndex)
			}
		default:
			return fmt.Errorf(
				"message %d content %d: unsupported type %q",
				index,
				contentIndex,
				content.Type,
			)
		}
	}

	switch message.Role {
	case RoleSystem, RoleUser:
		if strings.TrimSpace(message.Text()) == "" {
			return fmt.Errorf("message %d: role %q requires text", index, message.Role)
		}
	case RoleAssistant:
		if strings.TrimSpace(message.Text()) == "" && len(message.ToolCalls()) == 0 {
			return fmt.Errorf("message %d: assistant message requires text or tool calls", index)
		}
	case RoleTool:
		for contentIndex, content := range message.Content {
			if content.Type != ContentText {
				return fmt.Errorf("message %d content %d: tool result must contain only text", index, contentIndex)
			}
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
	cloned.Content = make([]Content, len(message.Content))
	for i, content := range message.Content {
		cloned.Content[i] = content
		if content.ToolCall != nil {
			call := cloneToolCall(*content.ToolCall)
			cloned.Content[i].ToolCall = &call
		}
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

func cloneDelta(delta ModelDelta) ModelDelta {
	return delta
}

func cloneUsage(usage *Usage) *Usage {
	if usage == nil {
		return nil
	}
	cloned := *usage
	if usage.CachedInputTokens != nil {
		value := *usage.CachedInputTokens
		cloned.CachedInputTokens = &value
	}
	if usage.ReasoningTokens != nil {
		value := *usage.ReasoningTokens
		cloned.ReasoningTokens = &value
	}
	return &cloned
}

func addUsage(total **Usage, usage *Usage) {
	if usage == nil {
		return
	}
	if *total == nil {
		*total = &Usage{}
	}
	(*total).InputTokens += usage.InputTokens
	(*total).OutputTokens += usage.OutputTokens
	(*total).TotalTokens += usage.TotalTokens
	addOptionalTokenCount(&(*total).CachedInputTokens, usage.CachedInputTokens)
	addOptionalTokenCount(&(*total).ReasoningTokens, usage.ReasoningTokens)
}

func addOptionalTokenCount(total **int64, value *int64) {
	if value == nil {
		return
	}
	if *total == nil {
		zero := int64(0)
		*total = &zero
	}
	**total += *value
}

func cloneDuration(duration *time.Duration) *time.Duration {
	if duration == nil {
		return nil
	}
	value := *duration
	return &value
}
