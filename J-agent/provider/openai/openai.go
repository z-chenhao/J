// Package openai provides an experimental OpenAI-compatible Chat Completions
// provider for agent.Model.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/z-chenhao/J/J-agent/agent"
)

const maxResponseSize = 16 << 20

// ReasoningField controls how retained reasoning is represented in assistant
// messages sent back during tool continuation.
type ReasoningField string

const (
	ReasoningFieldOmit             ReasoningField = ""
	ReasoningFieldReasoningContent ReasoningField = "reasoning_content"
	ReasoningFieldReasoning        ReasoningField = "reasoning"
)

// ReasoningEffort controls the OpenAI-compatible reasoning_effort request
// field. Individual servers and models may support only a subset.
type ReasoningEffort string

const (
	ReasoningEffortDefault ReasoningEffort = ""
	ReasoningEffortNone    ReasoningEffort = "none"
	ReasoningEffortLow     ReasoningEffort = "low"
	ReasoningEffortMedium  ReasoningEffort = "medium"
	ReasoningEffortHigh    ReasoningEffort = "high"
	ReasoningEffortMax     ReasoningEffort = "max"
)

// Config configures one OpenAI-compatible Chat Completions provider.
type Config struct {
	APIKey          string
	Model           string
	BaseURL         string
	ReasoningField  ReasoningField
	ReasoningEffort ReasoningEffort
	HTTPClient      *http.Client
}

// Model implements agent.Model using an OpenAI-compatible streaming Chat
// Completions API.
type Model struct {
	apiKey          string
	model           string
	endpoint        string
	reasoningField  ReasoningField
	reasoningEffort ReasoningEffort
	client          *http.Client
}

// HTTPError reports a non-successful provider HTTP response.
type HTTPError struct {
	StatusCode int
	Message    string
}

func (err *HTTPError) Error() string {
	return fmt.Sprintf("openai provider HTTP %d: %s", err.StatusCode, err.Message)
}

// New validates config and creates an OpenAI-compatible provider.
func New(config Config) (*Model, error) {
	config.APIKey = strings.TrimSpace(config.APIKey)
	config.Model = strings.TrimSpace(config.Model)
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	if config.Model == "" {
		return nil, errors.New("openai provider model is required")
	}
	if config.BaseURL == "" {
		return nil, errors.New("openai provider base URL is required")
	}
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) ||
		parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New(
			"openai provider base URL must use HTTP or HTTPS and must not contain credentials, a query, or a fragment",
		)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/chat/completions"
	parsed.RawPath = ""
	switch config.ReasoningField {
	case ReasoningFieldOmit, ReasoningFieldReasoningContent, ReasoningFieldReasoning:
	default:
		return nil, fmt.Errorf("unsupported reasoning field %q", config.ReasoningField)
	}
	switch config.ReasoningEffort {
	case ReasoningEffortDefault,
		ReasoningEffortNone,
		ReasoningEffortLow,
		ReasoningEffortMedium,
		ReasoningEffortHigh,
		ReasoningEffortMax:
	default:
		return nil, fmt.Errorf("unsupported reasoning effort %q", config.ReasoningEffort)
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	return &Model{
		apiKey:          config.APIKey,
		model:           config.Model,
		endpoint:        parsed.String(),
		reasoningField:  config.ReasoningField,
		reasoningEffort: config.ReasoningEffort,
		client:          config.HTTPClient,
	}, nil
}

type chatRequest struct {
	Model           string        `json:"model"`
	Messages        []chatMessage `json:"messages"`
	Tools           []chatTool    `json:"tools,omitempty"`
	Stream          bool          `json:"stream"`
	StreamOptions   streamOptions `json:"stream_options"`
	ReasoningEffort string        `json:"reasoning_effort,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type chatMessage struct {
	Role             string     `json:"role"`
	Content          *string    `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	Reasoning        string     `json:"reasoning,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   string          `json:"arguments,omitempty"`
}

type toolCall struct {
	Index    int          `json:"index,omitempty"`
	ID       string       `json:"id,omitempty"`
	Type     string       `json:"type,omitempty"`
	Function toolFunction `json:"function"`
}

type streamChunk struct {
	ID      string   `json:"id"`
	Model   string   `json:"model"`
	Choices []choice `json:"choices"`
	Usage   *usage   `json:"usage"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type choice struct {
	Delta        delta  `json:"delta"`
	FinishReason string `json:"finish_reason"`
}

type delta struct {
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content"`
	Reasoning        string     `json:"reasoning"`
	ToolCalls        []toolCall `json:"tool_calls"`
}

type usage struct {
	PromptTokens          int64  `json:"prompt_tokens"`
	CompletionTokens      int64  `json:"completion_tokens"`
	TotalTokens           int64  `json:"total_tokens"`
	PromptCacheHitTokens  *int64 `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens *int64 `json:"prompt_cache_miss_tokens"`
	PromptTokenDetails    *struct {
		CachedTokens *int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokenDetails *struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

type callAccumulator struct {
	id        string
	name      string
	arguments string
}

// Complete streams and returns one complete assistant response.
func (m *Model) Complete(
	ctx context.Context,
	request agent.ModelRequest,
	emit func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	payload, err := m.request(request)
	if err != nil {
		return agent.ModelResponse{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return agent.ModelResponse{}, fmt.Errorf("encode openai provider request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(body))
	if err != nil {
		return agent.ModelResponse{}, fmt.Errorf("create openai provider request: %w", err)
	}
	if m.apiKey != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	response, err := m.client.Do(httpRequest)
	if err != nil {
		return agent.ModelResponse{}, fmt.Errorf("call openai provider: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return agent.ModelResponse{}, httpError(response)
	}

	return m.readStream(response.Body, emit)
}

func (m *Model) request(request agent.ModelRequest) (chatRequest, error) {
	messages := make([]chatMessage, len(request.Messages))
	for i, message := range request.Messages {
		mapped, err := mapMessage(message, m.reasoningField)
		if err != nil {
			return chatRequest{}, fmt.Errorf("openai provider message %d: %w", i, err)
		}
		messages[i] = mapped
	}
	tools := make([]chatTool, len(request.Tools))
	for i, tool := range request.Tools {
		tools[i] = chatTool{
			Type: "function",
			Function: toolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  append(json.RawMessage(nil), tool.InputSchema...),
			},
		}
	}
	payload := chatRequest{
		Model:           m.model,
		Messages:        messages,
		Tools:           tools,
		Stream:          true,
		StreamOptions:   streamOptions{IncludeUsage: true},
		ReasoningEffort: string(m.reasoningEffort),
	}
	return payload, nil
}

func mapMessage(message agent.Message, reasoningField ReasoningField) (chatMessage, error) {
	mapped := chatMessage{Role: string(message.Role)}
	var text string
	var reasoning string
	var calls []toolCall
	for _, content := range message.Content {
		switch content.Type {
		case agent.ContentText:
			text += content.Text
		case agent.ContentReasoning:
			reasoning += content.Text
		case agent.ContentToolCall:
			if content.ToolCall == nil {
				return chatMessage{}, errors.New("tool-call content is missing its call")
			}
			calls = append(calls, toolCall{
				ID:   content.ToolCall.ID,
				Type: "function",
				Function: toolFunction{
					Name:      content.ToolCall.Name,
					Arguments: string(content.ToolCall.Arguments),
				},
			})
		default:
			return chatMessage{}, fmt.Errorf("unsupported content type %q", content.Type)
		}
	}
	switch message.Role {
	case agent.RoleSystem, agent.RoleUser:
		if len(calls) > 0 || reasoning != "" {
			return chatMessage{}, fmt.Errorf("role %q cannot contain reasoning or tool calls", message.Role)
		}
		mapped.Content = &text
	case agent.RoleAssistant:
		mapped.ToolCalls = calls
		switch reasoningField {
		case ReasoningFieldReasoningContent:
			mapped.ReasoningContent = reasoning
		case ReasoningFieldReasoning:
			mapped.Reasoning = reasoning
		}
		if text != "" {
			mapped.Content = &text
		}
	case agent.RoleTool:
		if len(calls) > 0 || reasoning != "" {
			return chatMessage{}, errors.New("tool messages cannot contain reasoning or tool calls")
		}
		if strings.TrimSpace(message.ToolCallID) == "" {
			return chatMessage{}, errors.New("tool message is missing tool call id")
		}
		mapped.Content = &text
		mapped.ToolCallID = message.ToolCallID
	default:
		return chatMessage{}, fmt.Errorf("unsupported role %q", message.Role)
	}
	return mapped, nil
}

func (m *Model) readStream(reader io.Reader, emit func(agent.ModelDelta)) (agent.ModelResponse, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, maxResponseSize+1))
	scanner.Buffer(make([]byte, 64*1024), maxResponseSize)
	var (
		responseID    string
		responseModel string
		text          string
		reasoning     string
		finishReason  string
		tokenUsage    *agent.Usage
		calls         = make(map[int]*callAccumulator)
		sawDone       bool
	)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			sawDone = true
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return agent.ModelResponse{}, fmt.Errorf("decode openai provider stream: %w", err)
		}
		if chunk.Error != nil {
			return agent.ModelResponse{}, fmt.Errorf("openai provider: %s", chunk.Error.Message)
		}
		if chunk.ID != "" {
			responseID = chunk.ID
		}
		if chunk.Model != "" && chunk.Model != "keepalive" {
			responseModel = chunk.Model
		}
		if chunk.Usage != nil {
			tokenUsage = mapUsage(chunk.Usage)
		}
		for _, choice := range chunk.Choices {
			reasoningDelta := choice.Delta.ReasoningContent
			if reasoningDelta == "" {
				reasoningDelta = choice.Delta.Reasoning
			}
			if reasoningDelta != "" {
				reasoning += reasoningDelta
				if emit != nil {
					emit(agent.ModelDelta{
						Type:  agent.DeltaReasoning,
						Index: 0,
						Delta: reasoningDelta,
					})
				}
			}
			if choice.Delta.Content != "" {
				text += choice.Delta.Content
				if emit != nil {
					emit(agent.ModelDelta{Type: agent.DeltaText, Index: 0, Delta: choice.Delta.Content})
				}
			}
			for _, streamedCall := range choice.Delta.ToolCalls {
				call := calls[streamedCall.Index]
				if call == nil {
					call = &callAccumulator{}
					calls[streamedCall.Index] = call
				}
				call.id += streamedCall.ID
				call.name += streamedCall.Function.Name
				call.arguments += streamedCall.Function.Arguments
				if emit != nil && streamedCall.Function.Arguments != "" {
					emit(agent.ModelDelta{
						Type:       agent.DeltaToolCall,
						Index:      streamedCall.Index,
						Delta:      streamedCall.Function.Arguments,
						ToolCallID: call.id,
						ToolName:   call.name,
					})
				}
			}
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return agent.ModelResponse{}, fmt.Errorf("read openai provider stream: %w", err)
	}
	if !sawDone {
		return agent.ModelResponse{}, errors.New("openai provider stream ended before [DONE]")
	}
	stopReason, err := mapStopReason(finishReason)
	if err != nil {
		return agent.ModelResponse{}, err
	}
	if responseModel == "" {
		responseModel = m.model
	}
	content := make([]agent.Content, 0, 2+len(calls))
	if reasoning != "" {
		content = append(content, agent.Content{Type: agent.ContentReasoning, Text: reasoning})
	}
	if text != "" {
		content = append(content, agent.Content{Type: agent.ContentText, Text: text})
	}
	indexes := make([]int, 0, len(calls))
	for index := range calls {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	for _, index := range indexes {
		call := calls[index]
		tool := agent.ToolCall{
			ID:        call.id,
			Name:      call.name,
			Arguments: json.RawMessage(call.arguments),
		}
		content = append(content, agent.Content{Type: agent.ContentToolCall, ToolCall: &tool})
	}
	return agent.ModelResponse{
		Message: agent.Message{
			Role:    agent.RoleAssistant,
			Content: content,
		},
		Provider:   "openai",
		Model:      responseModel,
		ResponseID: responseID,
		StopReason: stopReason,
		Usage:      tokenUsage,
	}, nil
}

func mapUsage(value *usage) *agent.Usage {
	result := &agent.Usage{
		InputTokens:  value.PromptTokens,
		OutputTokens: value.CompletionTokens,
		TotalTokens:  value.TotalTokens,
	}
	switch {
	case value.PromptTokenDetails != nil && value.PromptTokenDetails.CachedTokens != nil:
		cached := *value.PromptTokenDetails.CachedTokens
		result.CachedInputTokens = &cached
	case value.PromptCacheHitTokens != nil:
		cached := *value.PromptCacheHitTokens
		result.CachedInputTokens = &cached
	case value.PromptCacheMissTokens != nil && value.PromptTokens >= *value.PromptCacheMissTokens:
		cached := value.PromptTokens - *value.PromptCacheMissTokens
		result.CachedInputTokens = &cached
	}
	if value.CompletionTokenDetails != nil {
		reasoning := value.CompletionTokenDetails.ReasoningTokens
		result.ReasoningTokens = &reasoning
	}
	return result
}

func mapStopReason(value string) (agent.StopReason, error) {
	switch value {
	case "stop":
		return agent.StopReasonStop, nil
	case "length":
		return agent.StopReasonLength, nil
	case "tool_calls":
		return agent.StopReasonToolCalls, nil
	case "content_filter":
		return agent.StopReasonContentFilter, nil
	case "insufficient_system_resource":
		return "", errors.New("openai provider reported insufficient system resources")
	default:
		return "", fmt.Errorf("openai provider returned unsupported finish reason %q", value)
	}
}

func httpError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(response.StatusCode)
	}
	return &HTTPError{StatusCode: response.StatusCode, Message: message}
}
