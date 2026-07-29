package subagents

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/z-chenhao/J/J-agent/agent"
)

func TestToolRunsFreshIsolatedSubagent(t *testing.T) {
	model := &recordingModel{}
	var events []agent.EventType
	subagentTool, err := NewTool(Definition{
		Name:         "research",
		Description:  "Research one bounded question.",
		Model:        model,
		SystemPrompt: "You are a researcher.",
		Tools:        []agent.Tool{staticTool{name: "lookup"}},
		EventHandler: func(event agent.Event) {
			events = append(events, event.Type)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := subagentTool.Spec()
	if spec.Name != "subagent_run" ||
		!strings.Contains(spec.Description, "research: Research one bounded question.") ||
		!strings.Contains(string(spec.InputSchema), `"enum":["research"]`) {
		t.Fatalf("spec=%#v", spec)
	}

	for _, task := range []string{"first", "second"} {
		output, err := subagentTool.Call(
			context.Background(),
			json.RawMessage(`{"agent":"research","task":"`+task+`"}`),
		)
		if err != nil {
			t.Fatal(err)
		}
		var decoded struct {
			Agent   string       `json:"agent"`
			Content string       `json:"content"`
			Turns   int          `json:"turns"`
			Usage   *agent.Usage `json:"usage"`
		}
		if err := json.Unmarshal([]byte(output), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Agent != "research" || decoded.Content != "done: "+task ||
			decoded.Turns != 1 || decoded.Usage == nil ||
			decoded.Usage.TotalTokens != 3 {
			t.Fatalf("result=%#v", decoded)
		}
	}

	requests := model.Requests()
	if len(requests) != 2 {
		t.Fatalf("requests=%d", len(requests))
	}
	for index, request := range requests {
		if len(request.Messages) != 2 ||
			request.Messages[0].Role != agent.RoleSystem ||
			request.Messages[0].Text() != "You are a researcher." ||
			request.Messages[1].Role != agent.RoleUser ||
			len(request.Tools) != 1 || request.Tools[0].Name != "lookup" {
			t.Fatalf("request[%d]=%#v", index, request)
		}
	}
	wantEvents := []agent.EventType{
		agent.EventAgentStarted,
		agent.EventTurnStarted,
		agent.EventMessageStarted,
		agent.EventMessageCompleted,
		agent.EventTurnCompleted,
		agent.EventAgentCompleted,
	}
	if len(events) != len(wantEvents)*2 {
		t.Fatalf("events=%v", events)
	}
	for run := range 2 {
		for index, event := range wantEvents {
			if events[run*len(wantEvents)+index] != event {
				t.Fatalf("events=%v", events)
			}
		}
	}
}

func TestSessionToolResumesAcrossInstances(t *testing.T) {
	store := &memoryTranscriptStore{messages: make(map[string][]agent.Message)}
	model := &recordingModel{}
	definition := Definition{
		Name:         "research",
		Description:  "Research one bounded question.",
		Model:        model,
		SystemPrompt: "You are a researcher.",
	}
	firstTool, err := NewSessionTool(SessionConfig{
		ParentID: "parent-session",
		Store:    store,
	}, definition)
	if err != nil {
		t.Fatal(err)
	}
	firstOutput, err := firstTool.Call(
		context.Background(),
		json.RawMessage(`{"agent":"research","task":"first"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var first struct {
		Session string `json:"session"`
		Resumed bool   `json:"resumed"`
	}
	if err := json.Unmarshal([]byte(firstOutput), &first); err != nil {
		t.Fatal(err)
	}
	if err := validateSessionID(first.Session); err != nil || first.Resumed {
		t.Fatalf("first result=%#v error=%v", first, err)
	}

	secondTool, err := NewSessionTool(SessionConfig{
		ParentID: "parent-session",
		Store:    store,
	}, definition)
	if err != nil {
		t.Fatal(err)
	}
	secondOutput, err := secondTool.Call(
		context.Background(),
		json.RawMessage(
			`{"agent":"research","task":"second","session":"`+
				first.Session+`"}`,
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	var second struct {
		Session string `json:"session"`
		Resumed bool   `json:"resumed"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(secondOutput), &second); err != nil {
		t.Fatal(err)
	}
	if second.Session != first.Session || !second.Resumed ||
		second.Content != "done: second" {
		t.Fatalf("second result=%#v", second)
	}

	requests := model.Requests()
	if len(requests) != 2 || len(requests[1].Messages) != 4 {
		t.Fatalf("requests=%#v", requests)
	}
	want := []struct {
		role agent.Role
		text string
	}{
		{agent.RoleSystem, "You are a researcher."},
		{agent.RoleUser, "first"},
		{agent.RoleAssistant, "done: first"},
		{agent.RoleUser, "second"},
	}
	for index, expected := range want {
		message := requests[1].Messages[index]
		if message.Role != expected.role || message.Text() != expected.text {
			t.Fatalf("message[%d]=%#v", index, message)
		}
	}

	otherParent, err := NewSessionTool(SessionConfig{
		ParentID: "other-parent",
		Store:    store,
	}, definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := otherParent.Call(
		context.Background(),
		json.RawMessage(
			`{"agent":"research","task":"intrude","session":"`+
				first.Session+`"}`,
		),
	); err == nil {
		t.Fatal("another parent resumed the child session")
	}
}

func TestSessionToolCheckpointsFailedRunForExplicitResume(t *testing.T) {
	store := &memoryTranscriptStore{messages: make(map[string][]agent.Message)}
	model := &failOnceModel{}
	definition := Definition{
		Name:         "worker",
		Description:  "Continue after a model failure.",
		Model:        model,
		SystemPrompt: "Keep context.",
	}
	firstTool, err := NewSessionTool(SessionConfig{
		ParentID: "parent",
		Store:    store,
	}, definition)
	if err != nil {
		t.Fatal(err)
	}
	output, callErr := firstTool.Call(
		context.Background(),
		json.RawMessage(`{"agent":"worker","task":"first"}`),
	)
	if callErr == nil {
		t.Fatal("model failure was hidden")
	}
	var failed struct {
		Session string `json:"session"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(output), &failed); err != nil {
		t.Fatal(err)
	}
	if err := validateSessionID(failed.Session); err != nil ||
		!strings.Contains(failed.Error, "temporary failure") {
		t.Fatalf("failed result=%#v error=%v", failed, err)
	}

	secondTool, err := NewSessionTool(SessionConfig{
		ParentID: "parent",
		Store:    store,
	}, definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secondTool.Call(
		context.Background(),
		json.RawMessage(
			`{"agent":"worker","task":"continue","session":"`+
				failed.Session+`"}`,
		),
	); err != nil {
		t.Fatal(err)
	}
	requests := model.Requests()
	if len(requests) != 2 {
		t.Fatalf("requests=%#v", requests)
	}
	messages := requests[1].Messages
	if len(messages) != 3 ||
		messages[0].Role != agent.RoleSystem ||
		messages[0].Text() != "Keep context." ||
		messages[1].Role != agent.RoleUser ||
		messages[1].Text() != "first" ||
		messages[2].Role != agent.RoleUser ||
		messages[2].Text() != "continue" {
		t.Fatalf("resumed messages=%#v", messages)
	}
}

func TestSessionToolRestoresOnlyCompleteToolTurns(t *testing.T) {
	store := &memoryTranscriptStore{messages: make(map[string][]agent.Message)}
	model := &toolThenFailModel{}
	definition := Definition{
		Name:         "worker",
		Description:  "Use a tool before continuing.",
		Model:        model,
		SystemPrompt: "Keep complete turns.",
		Tools:        []agent.Tool{staticTool{name: "lookup"}},
	}
	firstTool, err := NewSessionTool(SessionConfig{
		ParentID: "parent",
		Store:    store,
	}, definition)
	if err != nil {
		t.Fatal(err)
	}
	output, callErr := firstTool.Call(
		context.Background(),
		json.RawMessage(`{"agent":"worker","task":"first"}`),
	)
	if callErr == nil {
		t.Fatal("second model turn failure was hidden")
	}
	var failed struct {
		Session string `json:"session"`
	}
	if err := json.Unmarshal([]byte(output), &failed); err != nil {
		t.Fatal(err)
	}

	secondTool, err := NewSessionTool(SessionConfig{
		ParentID: "parent",
		Store:    store,
	}, definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := secondTool.Call(
		context.Background(),
		json.RawMessage(
			`{"agent":"worker","task":"continue","session":"`+
				failed.Session+`"}`,
		),
	); err != nil {
		t.Fatal(err)
	}

	requests := model.Requests()
	if len(requests) != 3 {
		t.Fatalf("requests=%#v", requests)
	}
	messages := requests[2].Messages
	if len(messages) != 5 ||
		messages[0].Role != agent.RoleSystem ||
		messages[1].Role != agent.RoleUser ||
		messages[2].Role != agent.RoleAssistant ||
		len(messages[2].ToolCalls()) != 1 ||
		messages[3].Role != agent.RoleTool ||
		messages[3].ToolCallID != "lookup-1" ||
		messages[4].Role != agent.RoleUser ||
		messages[4].Text() != "continue" {
		t.Fatalf("resumed messages=%#v", messages)
	}
}

func TestToolRejectsInvalidCalls(t *testing.T) {
	subagentTool, err := NewTool(Definition{
		Name:        "plain",
		Description: "Answer plainly.",
		Model:       &recordingModel{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{
		"unknown agent": `{"agent":"missing","task":"work"}`,
		"empty task":    `{"agent":"plain","task":" "}`,
		"unknown field": `{"agent":"plain","task":"work","extra":true}`,
		"trailing data": `{"agent":"plain","task":"work"} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := subagentTool.Call(
				context.Background(),
				json.RawMessage(input),
			); err == nil {
				t.Fatal("invalid call was accepted")
			}
		})
	}
	if _, err := subagentTool.Call(
		context.Background(),
		json.RawMessage(`{"agent":"plain","task":"work","session":"sub_00000000000000000000000000000000"}`),
	); err == nil {
		t.Fatal("ephemeral tool accepted a session")
	}
}

func TestToolPropagatesCancellation(t *testing.T) {
	model := blockingModel{}
	subagentTool, err := NewTool(Definition{
		Name:        "slow",
		Description: "Wait for cancellation.",
		Model:       model,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = subagentTool.Call(
		ctx,
		json.RawMessage(`{"agent":"slow","task":"wait"}`),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
}

func TestToolCancellationInterruptsSerializationWait(t *testing.T) {
	model := &serialModel{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	subagentTool, err := NewTool(Definition{
		Name:        "serial",
		Description: "Serialize model calls.",
		Model:       model,
	})
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, callErr := subagentTool.Call(
			context.Background(),
			json.RawMessage(`{"agent":"serial","task":"first"}`),
		)
		firstDone <- callErr
	}()
	<-model.started

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = subagentTool.Call(
		ctx,
		json.RawMessage(`{"agent":"serial","task":"second"}`),
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	close(model.release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
}

func TestNewToolValidatesDefinitions(t *testing.T) {
	valid := Definition{
		Name:        "same",
		Description: "Valid.",
		Model:       &recordingModel{},
	}
	tests := map[string][]Definition{
		"empty":             nil,
		"blank name":        {{Name: " ", Description: "Valid.", Model: &recordingModel{}}},
		"unsafe name":       {{Name: "bad name", Description: "Valid.", Model: &recordingModel{}}},
		"punctuation start": {{Name: ".bad", Description: "Valid.", Model: &recordingModel{}}},
		"blank description": {{Name: "x", Description: " ", Model: &recordingModel{}}},
		"nil model":         {{Name: "x", Description: "Valid."}},
		"duplicate":         {valid, valid},
		"invalid tool": {{
			Name:        "x",
			Description: "Valid.",
			Model:       &recordingModel{},
			Tools:       []agent.Tool{staticTool{name: ""}},
		}},
	}
	for name, definitions := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := NewTool(definitions...); err == nil {
				t.Fatal("invalid definitions were accepted")
			}
		})
	}
}

func TestNewSessionToolValidatesSessionConfig(t *testing.T) {
	definition := Definition{
		Name:        "worker",
		Description: "Work.",
		Model:       &recordingModel{},
	}
	if _, err := NewSessionTool(SessionConfig{}, definition); err == nil {
		t.Fatal("empty session configuration was accepted")
	}
	if _, err := NewSessionTool(
		SessionConfig{ParentID: "parent"},
		definition,
	); err == nil {
		t.Fatal("nil transcript store was accepted")
	}
}

type recordingModel struct {
	mu       sync.Mutex
	requests []agent.ModelRequest
}

func (model *recordingModel) Complete(
	_ context.Context,
	request agent.ModelRequest,
	_ func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	model.mu.Lock()
	model.requests = append(model.requests, request)
	model.mu.Unlock()
	task := request.Messages[len(request.Messages)-1].Text()
	return agent.ModelResponse{
		Message:    agent.TextMessage(agent.RoleAssistant, "done: "+task),
		Provider:   "test",
		Model:      "test",
		StopReason: agent.StopReasonStop,
		Usage: &agent.Usage{
			InputTokens:  2,
			OutputTokens: 1,
			TotalTokens:  3,
		},
	}, nil
}

func (model *recordingModel) Requests() []agent.ModelRequest {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([]agent.ModelRequest(nil), model.requests...)
}

type failOnceModel struct {
	mu       sync.Mutex
	calls    int
	requests []agent.ModelRequest
}

func (model *failOnceModel) Complete(
	_ context.Context,
	request agent.ModelRequest,
	_ func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.calls++
	model.requests = append(model.requests, request)
	if model.calls == 1 {
		return agent.ModelResponse{}, errors.New("temporary failure")
	}
	return agent.ModelResponse{
		Message:    agent.TextMessage(agent.RoleAssistant, "continued"),
		Provider:   "test",
		Model:      "test",
		StopReason: agent.StopReasonStop,
	}, nil
}

func (model *failOnceModel) Requests() []agent.ModelRequest {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([]agent.ModelRequest(nil), model.requests...)
}

type toolThenFailModel struct {
	mu       sync.Mutex
	calls    int
	requests []agent.ModelRequest
}

func (model *toolThenFailModel) Complete(
	_ context.Context,
	request agent.ModelRequest,
	_ func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.calls++
	model.requests = append(model.requests, request)
	switch model.calls {
	case 1:
		return agent.ModelResponse{
			Message: agent.Message{
				Role: agent.RoleAssistant,
				Content: []agent.Content{{
					Type: agent.ContentToolCall,
					ToolCall: &agent.ToolCall{
						ID:        "lookup-1",
						Name:      "lookup",
						Arguments: json.RawMessage(`{}`),
					},
				}},
			},
			Provider:   "test",
			Model:      "test",
			StopReason: agent.StopReasonToolCalls,
		}, nil
	case 2:
		return agent.ModelResponse{}, errors.New("failure after complete tool turn")
	default:
		return agent.ModelResponse{
			Message:    agent.TextMessage(agent.RoleAssistant, "resumed"),
			Provider:   "test",
			Model:      "test",
			StopReason: agent.StopReasonStop,
		}, nil
	}
}

func (model *toolThenFailModel) Requests() []agent.ModelRequest {
	model.mu.Lock()
	defer model.mu.Unlock()
	return append([]agent.ModelRequest(nil), model.requests...)
}

type memoryTranscriptStore struct {
	mu       sync.Mutex
	messages map[string][]agent.Message
}

func (store *memoryTranscriptStore) Load(
	_ context.Context,
	key string,
) ([]agent.Message, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	messages, exists := store.messages[key]
	if !exists {
		return nil, errors.New("not found")
	}
	return cloneTestMessages(messages), nil
}

func (store *memoryTranscriptStore) Save(
	_ context.Context,
	key string,
	messages []agent.Message,
) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.messages[key] = cloneTestMessages(messages)
	return nil
}

func cloneTestMessages(messages []agent.Message) []agent.Message {
	data, err := json.Marshal(messages)
	if err != nil {
		panic(err)
	}
	var cloned []agent.Message
	if err := json.Unmarshal(data, &cloned); err != nil {
		panic(err)
	}
	return cloned
}

type blockingModel struct{}

func (blockingModel) Complete(
	ctx context.Context,
	_ agent.ModelRequest,
	_ func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	<-ctx.Done()
	return agent.ModelResponse{}, ctx.Err()
}

type serialModel struct {
	started chan struct{}
	release chan struct{}
}

func (model *serialModel) Complete(
	ctx context.Context,
	request agent.ModelRequest,
	_ func(agent.ModelDelta),
) (agent.ModelResponse, error) {
	close(model.started)
	select {
	case <-ctx.Done():
		return agent.ModelResponse{}, ctx.Err()
	case <-model.release:
	}
	return agent.ModelResponse{
		Message:    agent.TextMessage(agent.RoleAssistant, request.Messages[len(request.Messages)-1].Text()),
		Provider:   "test",
		Model:      "test",
		StopReason: agent.StopReasonStop,
	}, nil
}

type staticTool struct {
	name string
}

func (tool staticTool) Spec() agent.ToolSpec {
	return agent.ToolSpec{
		Name:        tool.name,
		Description: "Static test tool.",
		InputSchema: json.RawMessage(`{"type":"object"}`),
	}
}

func (staticTool) Call(context.Context, json.RawMessage) (string, error) {
	return "ok", nil
}
