// Package ollama provides an experimental adapter from Ollama's native Chat
// API to agent.Model.
package ollama

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
	"sync/atomic"

	"github.com/z-chenhao/J-agent/agent"
)

const (
	defaultBaseURL  = "http://localhost:11434"
	maxResponseSize = 16 << 20
)

// Thinking controls Ollama thinking mode.
type Thinking string

const (
	ThinkingDefault  Thinking = ""
	ThinkingEnabled  Thinking = "enabled"
	ThinkingDisabled Thinking = "disabled"
)

// Config configures one Ollama model adapter.
type Config struct {
	Model      string
	BaseURL    string
	Thinking   Thinking
	HTTPClient *http.Client
}

// Model implements agent.Model using Ollama's streaming native Chat API.
type Model struct {
	model    string
	endpoint string
	think    *bool
	client   *http.Client
	turn     atomic.Uint64
}

// HTTPError reports a non-successful Ollama HTTP response.
type HTTPError struct {
	StatusCode int
	Message    string
}

func (err *HTTPError) Error() string {
	return fmt.Sprintf("ollama HTTP %d: %s", err.StatusCode, err.Message)
}

// New validates config and creates an Ollama adapter.
func New(config Config) (*Model, error) {
	config.Model = strings.TrimSpace(config.Model)
	config.BaseURL = strings.TrimSpace(config.BaseURL)
	if config.Model == "" {
		return nil, errors.New("ollama model is required")
	}
	if config.BaseURL == "" {
		config.BaseURL = defaultBaseURL
	}
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		(!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) ||
		parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New(
			"ollama base URL must use HTTP or HTTPS and must not contain credentials, a query, or a fragment",
		)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/chat"
	parsed.RawPath = ""
	switch config.Thinking {
	case ThinkingDefault, ThinkingEnabled, ThinkingDisabled:
	default:
		return nil, fmt.Errorf("unsupported ollama thinking mode %q", config.Thinking)
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	var think *bool
	if config.Thinking != ThinkingDefault {
		value := config.Thinking == ThinkingEnabled
		think = &value
	}
	return &Model{
		model:    config.Model,
		endpoint: parsed.String(),
		think:    think,
		client:   config.HTTPClient,
	}, nil
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
	Think    *bool         `json:"think,omitempty"`
}

type chatMessage struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	Thinking  string     `json:"thinking,omitempty"`
	ToolCalls []toolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function toolFunction `json:"function"`
}

type toolFunction struct {
	Index       *int            `json:"index,omitempty"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Arguments   json.RawMessage `json:"arguments,omitempty"`
}

type toolCall struct {
	Type     string       `json:"type,omitempty"`
	Function toolFunction `json:"function"`
}

type streamChunk struct {
	Model   string      `json:"model"`
	Message chatMessage `json:"message"`
	Done    bool        `json:"done"`
	Reason  string      `json:"done_reason"`

	PromptEvalCount int64  `json:"prompt_eval_count"`
	EvalCount       int64  `json:"eval_count"`
	Error           string `json:"error"`
}

type callAccumulator struct {
	id        string
	name      string
	arguments json.RawMessage
}

// Complete streams and returns one complete Ollama assistant response.
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
		return agent.ModelResponse{}, fmt.Errorf("encode ollama request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(body))
	if err != nil {
		return agent.ModelResponse{}, fmt.Errorf("create ollama request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")

	response, err := m.client.Do(httpRequest)
	if err != nil {
		return agent.ModelResponse{}, fmt.Errorf("call ollama: %w", err)
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
		mapped, err := mapMessage(message)
		if err != nil {
			return chatRequest{}, fmt.Errorf("ollama message %d: %w", i, err)
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
	return chatRequest{
		Model:    m.model,
		Messages: messages,
		Tools:    tools,
		Stream:   true,
		Think:    m.think,
	}, nil
}

func mapMessage(message agent.Message) (chatMessage, error) {
	mapped := chatMessage{Role: string(message.Role)}
	for _, content := range message.Content {
		switch content.Type {
		case agent.ContentText:
			mapped.Content += content.Text
		case agent.ContentReasoning:
			mapped.Thinking += content.Text
		case agent.ContentToolCall:
			if content.ToolCall == nil {
				return chatMessage{}, errors.New("tool-call content is missing its call")
			}
			index := len(mapped.ToolCalls)
			mapped.ToolCalls = append(mapped.ToolCalls, toolCall{
				Type: "function",
				Function: toolFunction{
					Index:     &index,
					Name:      content.ToolCall.Name,
					Arguments: append(json.RawMessage(nil), content.ToolCall.Arguments...),
				},
			})
		default:
			return chatMessage{}, fmt.Errorf("unsupported content type %q", content.Type)
		}
	}
	switch message.Role {
	case agent.RoleSystem, agent.RoleUser:
		if len(mapped.ToolCalls) > 0 || mapped.Thinking != "" {
			return chatMessage{}, fmt.Errorf("role %q cannot contain thinking or tool calls", message.Role)
		}
	case agent.RoleAssistant:
	case agent.RoleTool:
		if len(mapped.ToolCalls) > 0 || mapped.Thinking != "" {
			return chatMessage{}, errors.New("tool messages cannot contain thinking or tool calls")
		}
		if strings.TrimSpace(message.ToolName) == "" {
			return chatMessage{}, errors.New("tool message is missing tool name")
		}
		mapped.ToolName = message.ToolName
	default:
		return chatMessage{}, fmt.Errorf("unsupported role %q", message.Role)
	}
	return mapped, nil
}

func (m *Model) readStream(reader io.Reader, emit func(agent.ModelDelta)) (agent.ModelResponse, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, maxResponseSize+1))
	scanner.Buffer(make([]byte, 64*1024), maxResponseSize)
	turn := m.turn.Add(1)
	var (
		responseModel string
		text          string
		reasoning     string
		finishReason  string
		tokenUsage    *agent.Usage
		calls         = make(map[int]*callAccumulator)
		sawDone       bool
		nextIndex     int
	)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var chunk streamChunk
		if err := json.Unmarshal(line, &chunk); err != nil {
			return agent.ModelResponse{}, fmt.Errorf("decode ollama stream: %w", err)
		}
		if chunk.Error != "" {
			return agent.ModelResponse{}, fmt.Errorf("ollama: %s", chunk.Error)
		}
		if chunk.Model != "" {
			responseModel = chunk.Model
		}
		if chunk.Message.Thinking != "" {
			reasoning += chunk.Message.Thinking
			if emit != nil {
				emit(agent.ModelDelta{Type: agent.DeltaReasoning, Index: 0, Delta: chunk.Message.Thinking})
			}
		}
		if chunk.Message.Content != "" {
			text += chunk.Message.Content
			if emit != nil {
				emit(agent.ModelDelta{Type: agent.DeltaText, Index: 0, Delta: chunk.Message.Content})
			}
		}
		for _, streamedCall := range chunk.Message.ToolCalls {
			index := nextIndex
			if streamedCall.Function.Index != nil {
				index = *streamedCall.Function.Index
			}
			call := calls[index]
			if call == nil {
				call = &callAccumulator{id: fmt.Sprintf("ollama-%d-%d", turn, index)}
				calls[index] = call
				if index >= nextIndex {
					nextIndex = index + 1
				}
			}
			call.name = streamedCall.Function.Name
			call.arguments = append(call.arguments[:0], streamedCall.Function.Arguments...)
			if emit != nil && len(streamedCall.Function.Arguments) > 0 {
				emit(agent.ModelDelta{
					Type:       agent.DeltaToolCall,
					Index:      index,
					Delta:      string(streamedCall.Function.Arguments),
					ToolCallID: call.id,
					ToolName:   call.name,
				})
			}
		}
		if chunk.Done {
			sawDone = true
			finishReason = chunk.Reason
			total := chunk.PromptEvalCount + chunk.EvalCount
			tokenUsage = &agent.Usage{
				InputTokens:  chunk.PromptEvalCount,
				OutputTokens: chunk.EvalCount,
				TotalTokens:  total,
			}
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return agent.ModelResponse{}, fmt.Errorf("read ollama stream: %w", err)
	}
	if !sawDone {
		return agent.ModelResponse{}, errors.New("ollama stream ended before a done response")
	}
	stopReason, err := mapStopReason(finishReason, len(calls) > 0)
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
			Arguments: append(json.RawMessage(nil), call.arguments...),
		}
		content = append(content, agent.Content{Type: agent.ContentToolCall, ToolCall: &tool})
	}
	return agent.ModelResponse{
		Message: agent.Message{
			Role:    agent.RoleAssistant,
			Content: content,
		},
		Provider:   "ollama",
		Model:      responseModel,
		StopReason: stopReason,
		Usage:      tokenUsage,
	}, nil
}

func mapStopReason(value string, hasToolCalls bool) (agent.StopReason, error) {
	if hasToolCalls {
		return agent.StopReasonToolCalls, nil
	}
	switch value {
	case "stop":
		return agent.StopReasonStop, nil
	case "length":
		return agent.StopReasonLength, nil
	default:
		return "", fmt.Errorf("ollama returned unsupported done reason %q", value)
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
