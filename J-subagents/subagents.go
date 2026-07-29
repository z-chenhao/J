// Package subagents projects explicitly configured, isolated J-agent runs
// through one ordinary Tool.
package subagents

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/z-chenhao/J/J-agent/agent"
)

const checkpointTimeout = 5 * time.Second

// Definition is one construction-time subagent recipe. Each new session starts
// with a fresh transcript and receives only the supplied Tools.
type Definition struct {
	Name         string
	Description  string
	Model        agent.Model
	SystemPrompt string
	Tools        []agent.Tool
	EventHandler agent.EventHandler
}

// TranscriptStore persists complete child transcript snapshots under opaque
// keys owned by J-subagents. J-mem's transcript Store satisfies this contract,
// and another host may provide its own implementation.
type TranscriptStore interface {
	Load(context.Context, string) ([]agent.Message, error)
	Save(context.Context, string, []agent.Message) error
}

// SessionConfig scopes resumable children to one host-owned parent session.
// It is product policy outside J-agent.
type SessionConfig struct {
	ParentID string
	Store    TranscriptStore
}

type definitionRuntime struct {
	definition Definition
	gate       chan struct{}
}

type sessionRuntime struct {
	parentHash string
	store      TranscriptStore
}

type tool struct {
	definitions map[string]*definitionRuntime
	spec        agent.ToolSpec
	sessions    *sessionRuntime
}

// NewTool validates definitions and returns one foreground subagent_run Tool.
// Model calls for the same definition are serialized because agent.Model does
// not promise concurrent safety.
func NewTool(definitions ...Definition) (agent.Tool, error) {
	return newTool(nil, definitions...)
}

// NewSessionTool returns a foreground subagent_run Tool whose child
// transcripts can be resumed by the session IDs returned in prior results.
// Snapshots are committed only at complete J-agent transcript checkpoints.
func NewSessionTool(
	sessions SessionConfig,
	definitions ...Definition,
) (agent.Tool, error) {
	parentID := strings.TrimSpace(sessions.ParentID)
	if parentID == "" {
		return nil, errors.New("subagent parent session ID is required")
	}
	if len(parentID) > 256 {
		return nil, errors.New("subagent parent session ID exceeds 256 bytes")
	}
	if sessions.Store == nil {
		return nil, errors.New("subagent transcript store is required")
	}
	parentHash := sha256.Sum256([]byte(parentID))
	return newTool(&sessionRuntime{
		parentHash: fmt.Sprintf("%x", parentHash),
		store:      sessions.Store,
	}, definitions...)
}

func newTool(
	sessions *sessionRuntime,
	definitions ...Definition,
) (agent.Tool, error) {
	if len(definitions) == 0 {
		return nil, errors.New("at least one subagent definition is required")
	}
	byName := make(map[string]*definitionRuntime, len(definitions))
	for _, configured := range definitions {
		definition := cloneDefinition(configured)
		if err := validateDefinition(definition); err != nil {
			return nil, err
		}
		if _, exists := byName[definition.Name]; exists {
			return nil, fmt.Errorf("duplicate subagent name %q", definition.Name)
		}
		options := []agent.Option{agent.WithTools(definition.Tools...)}
		if definition.SystemPrompt != "" {
			options = append(options, agent.WithSystemPrompt(definition.SystemPrompt))
		}
		if _, err := agent.New(definition.Model, options...); err != nil {
			return nil, fmt.Errorf("validate subagent %q: %w", definition.Name, err)
		}
		gate := make(chan struct{}, 1)
		gate <- struct{}{}
		byName[definition.Name] = &definitionRuntime{
			definition: definition,
			gate:       gate,
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	schema, err := inputSchema(names, sessions != nil)
	if err != nil {
		return nil, err
	}
	return &tool{
		definitions: byName,
		sessions:    sessions,
		spec: agent.ToolSpec{
			Name:        "subagent_run",
			Description: toolDescription(names, byName, sessions != nil),
			InputSchema: schema,
		},
	}, nil
}

func cloneDefinition(definition Definition) Definition {
	definition.Tools = append([]agent.Tool(nil), definition.Tools...)
	return definition
}

func validateDefinition(definition Definition) error {
	if definition.Name == "" || definition.Name != strings.TrimSpace(definition.Name) ||
		utf8.RuneCountInString(definition.Name) > 64 {
		return fmt.Errorf(
			"subagent name %q must contain 1 to 64 trimmed characters",
			definition.Name,
		)
	}
	first := definition.Name[0]
	if first < 'a' || first > 'z' {
		if first < 'A' || first > 'Z' {
			if first < '0' || first > '9' {
				return fmt.Errorf(
					"subagent name %q must start with an ASCII letter or digit",
					definition.Name,
				)
			}
		}
	}
	for index := 0; index < len(definition.Name); index++ {
		character := definition.Name[index]
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			strings.ContainsRune("._-", rune(character)) {
			continue
		}
		return fmt.Errorf(
			"subagent name %q may contain only ASCII letters, digits, '.', '_', and '-'",
			definition.Name,
		)
	}
	if definition.Description == "" ||
		definition.Description != strings.TrimSpace(definition.Description) ||
		utf8.RuneCountInString(definition.Description) > 1024 {
		return fmt.Errorf(
			"subagent %q description must contain 1 to 1024 trimmed characters",
			definition.Name,
		)
	}
	if definition.Model == nil {
		return fmt.Errorf("subagent %q model is required", definition.Name)
	}
	return nil
}

func inputSchema(names []string, resumable bool) (json.RawMessage, error) {
	type stringProperty struct {
		Type        string   `json:"type"`
		Description string   `json:"description"`
		Enum        []string `json:"enum,omitempty"`
	}
	schema := struct {
		Type       string `json:"type"`
		Properties struct {
			Agent   stringProperty  `json:"agent"`
			Task    stringProperty  `json:"task"`
			Session *stringProperty `json:"session,omitempty"`
		} `json:"properties"`
		Required             []string `json:"required"`
		AdditionalProperties bool     `json:"additionalProperties"`
	}{
		Type: "object",
		Required: []string{
			"agent",
			"task",
		},
		AdditionalProperties: false,
	}
	schema.Properties.Agent = stringProperty{
		Type:        "string",
		Description: "Exact configured subagent name",
		Enum:        names,
	}
	schema.Properties.Task = stringProperty{
		Type:        "string",
		Description: "Complete bounded task for the isolated subagent",
	}
	if resumable {
		schema.Properties.Session = &stringProperty{
			Type: "string",
			Description: "Exact child session ID from a prior subagent_run result; " +
				"omit to start a new child session",
		}
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode subagent tool schema: %w", err)
	}
	return encoded, nil
}

func toolDescription(
	names []string,
	definitions map[string]*definitionRuntime,
	resumable bool,
) string {
	var description strings.Builder
	if resumable {
		description.WriteString(
			"Run one configured subagent in the foreground. Omit session to start " +
				"a new isolated child transcript, or pass an exact session ID from " +
				"a prior result to continue that child. The call returns its session, " +
				"final content, and usage. Available subagents:\n",
		)
	} else {
		description.WriteString(
			"Run one configured subagent in the foreground with a fresh, isolated transcript. " +
				"The call returns its final content and usage. Available subagents:\n",
		)
	}
	for _, name := range names {
		fmt.Fprintf(
			&description,
			"- %s: %s\n",
			name,
			definitions[name].definition.Description,
		)
	}
	return strings.TrimSpace(description.String())
}

func (subagentTool *tool) Spec() agent.ToolSpec {
	spec := subagentTool.spec
	spec.InputSchema = append(json.RawMessage(nil), spec.InputSchema...)
	return spec
}

func (subagentTool *tool) Call(
	ctx context.Context,
	arguments json.RawMessage,
) (string, error) {
	var input struct {
		Agent   string `json:"agent"`
		Task    string `json:"task"`
		Session string `json:"session,omitempty"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return "", fmt.Errorf("decode subagent_run arguments: %w", err)
	}
	if input.Agent == "" || input.Agent != strings.TrimSpace(input.Agent) {
		return "", errors.New("subagent name must be non-empty and trimmed")
	}
	input.Task = strings.TrimSpace(input.Task)
	if input.Task == "" {
		return "", errors.New("subagent task is required")
	}
	if input.Session != "" {
		if subagentTool.sessions == nil {
			return "", errors.New("subagent sessions are not enabled")
		}
		if err := validateSessionID(input.Session); err != nil {
			return "", err
		}
	}
	runtime, exists := subagentTool.definitions[input.Agent]
	if !exists {
		return "", fmt.Errorf("subagent %q is not configured", input.Agent)
	}

	if ctx == nil {
		return "", errors.New("context is required")
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case <-runtime.gate:
	}
	defer func() {
		runtime.gate <- struct{}{}
	}()
	definition := runtime.definition
	options := []agent.Option{agent.WithTools(definition.Tools...)}
	sessionID := ""
	sessionKey := ""
	resumed := false
	if subagentTool.sessions != nil {
		sessionID = input.Session
		if sessionID == "" {
			var err error
			sessionID, err = newSessionID()
			if err != nil {
				return "", err
			}
		} else {
			resumed = true
		}
		sessionKey = subagentTool.sessions.storageKey(input.Agent, sessionID)
		if resumed {
			history, err := subagentTool.sessions.store.Load(ctx, sessionKey)
			if err != nil {
				return "", fmt.Errorf(
					"load subagent %q session %q: %w",
					input.Agent,
					sessionID,
					err,
				)
			}
			if len(history) == 0 {
				return "", fmt.Errorf(
					"subagent %q session %q has an empty transcript",
					input.Agent,
					sessionID,
				)
			}
			options = append(options, agent.WithHistory(history...))
		}
	}
	if !resumed && definition.SystemPrompt != "" {
		options = append(options, agent.WithSystemPrompt(definition.SystemPrompt))
	}
	runner, err := agent.New(definition.Model, options...)
	if err != nil {
		return "", fmt.Errorf("initialize subagent %q: %w", input.Agent, err)
	}
	runContext := ctx
	cancel := func() {}
	handler := definition.EventHandler
	var checkpointErr error
	if subagentTool.sessions != nil {
		runContext, cancel = context.WithCancel(ctx)
		handler = func(event agent.Event) {
			if checkpointErr == nil && isCheckpoint(event.Type) {
				saveContext, stop := context.WithTimeout(
					context.WithoutCancel(ctx),
					checkpointTimeout,
				)
				checkpointErr = subagentTool.sessions.store.Save(
					saveContext,
					sessionKey,
					runner.History(),
				)
				stop()
				if checkpointErr != nil {
					cancel()
				}
			}
			if definition.EventHandler != nil {
				definition.EventHandler(event)
			}
		}
	}
	defer cancel()
	result, runErr := runner.Run(runContext, input.Task, handler)
	if checkpointErr != nil {
		runErr = fmt.Errorf(
			"checkpoint subagent %q session %q: %w",
			input.Agent,
			sessionID,
			checkpointErr,
		)
	}
	output, err := encodeResult(input.Agent, sessionID, resumed, result, runErr)
	if err != nil {
		return "", err
	}
	if runErr != nil {
		return output, fmt.Errorf("run subagent %q: %w", input.Agent, runErr)
	}
	return output, nil
}

func encodeResult(
	name, sessionID string,
	resumed bool,
	result agent.RunResult,
	runErr error,
) (string, error) {
	output, err := json.Marshal(struct {
		Agent   string       `json:"agent"`
		Session string       `json:"session,omitempty"`
		Resumed bool         `json:"resumed,omitempty"`
		Content string       `json:"content"`
		Turns   int          `json:"turns"`
		Usage   *agent.Usage `json:"usage,omitempty"`
		Error   string       `json:"error,omitempty"`
	}{
		Agent:   name,
		Session: sessionID,
		Resumed: resumed,
		Content: result.Message.Text(),
		Turns:   result.Turns,
		Usage:   result.Usage,
		Error: func() string {
			if runErr == nil {
				return ""
			}
			return runErr.Error()
		}(),
	})
	if err != nil {
		return "", fmt.Errorf("encode subagent %q result: %w", name, err)
	}
	return string(output), nil
}

func isCheckpoint(eventType agent.EventType) bool {
	return eventType == agent.EventAgentStarted ||
		eventType == agent.EventTurnCompleted
}

func newSessionID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate subagent session ID: %w", err)
	}
	return fmt.Sprintf("sub_%x", value), nil
}

func validateSessionID(sessionID string) error {
	if len(sessionID) != len("sub_")+32 || !strings.HasPrefix(sessionID, "sub_") {
		return errors.New("subagent session ID is invalid")
	}
	for _, character := range sessionID[len("sub_"):] {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return errors.New("subagent session ID is invalid")
			}
		}
	}
	return nil
}

func (sessions *sessionRuntime) storageKey(agentName, sessionID string) string {
	return "subagents/" + sessions.parentHash + "/" + agentName + "/" + sessionID
}

func decodeArguments(data json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra json.RawMessage
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.New("unexpected data after the JSON object")
}
