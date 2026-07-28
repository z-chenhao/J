// Package subagents projects explicitly configured, isolated J-agent runs
// through one ordinary Tool.
package subagents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/z-chenhao/J/J-agent/agent"
)

// Definition is one construction-time subagent recipe. Each invocation starts
// with a fresh transcript and receives only the supplied Tools.
type Definition struct {
	Name         string
	Description  string
	Model        agent.Model
	SystemPrompt string
	Tools        []agent.Tool
}

type definitionRuntime struct {
	definition Definition
	gate       chan struct{}
}

type tool struct {
	definitions map[string]*definitionRuntime
	spec        agent.ToolSpec
}

// NewTool validates definitions and returns one foreground subagent_run Tool.
// Model calls for the same definition are serialized because agent.Model does
// not promise concurrent safety.
func NewTool(definitions ...Definition) (agent.Tool, error) {
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
	schema, err := inputSchema(names)
	if err != nil {
		return nil, err
	}
	return &tool{
		definitions: byName,
		spec: agent.ToolSpec{
			Name:        "subagent_run",
			Description: toolDescription(names, byName),
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

func inputSchema(names []string) (json.RawMessage, error) {
	type stringProperty struct {
		Type        string   `json:"type"`
		Description string   `json:"description"`
		Enum        []string `json:"enum,omitempty"`
	}
	schema := struct {
		Type       string `json:"type"`
		Properties struct {
			Agent stringProperty `json:"agent"`
			Task  stringProperty `json:"task"`
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
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode subagent tool schema: %w", err)
	}
	return encoded, nil
}

func toolDescription(
	names []string,
	definitions map[string]*definitionRuntime,
) string {
	var description strings.Builder
	description.WriteString(
		"Run one configured subagent in the foreground with a fresh, isolated transcript. " +
			"The call returns its final content and usage. Available subagents:\n",
	)
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
		Agent string `json:"agent"`
		Task  string `json:"task"`
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
	if definition.SystemPrompt != "" {
		options = append(options, agent.WithSystemPrompt(definition.SystemPrompt))
	}
	runner, err := agent.New(definition.Model, options...)
	if err != nil {
		return "", fmt.Errorf("initialize subagent %q: %w", input.Agent, err)
	}
	result, err := runner.Run(ctx, input.Task, nil)
	if err != nil {
		return "", fmt.Errorf("run subagent %q: %w", input.Agent, err)
	}
	output, err := json.Marshal(struct {
		Agent   string       `json:"agent"`
		Content string       `json:"content"`
		Turns   int          `json:"turns"`
		Usage   *agent.Usage `json:"usage,omitempty"`
	}{
		Agent:   input.Agent,
		Content: result.Message.Text(),
		Turns:   result.Turns,
		Usage:   result.Usage,
	})
	if err != nil {
		return "", fmt.Errorf("encode subagent %q result: %w", input.Agent, err)
	}
	return string(output), nil
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
