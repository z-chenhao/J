package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/z-chenhao/J/J-agent/agent"
)

type toolKind uint8

const (
	toolRetrieve toolKind = iota
	toolStore
	toolModify
	toolForget
)

var toolSchemas = map[toolKind]json.RawMessage{
	toolRetrieve: json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Case-insensitive substring to find; empty lists recent memories"
			},
			"limit": {
				"type": "integer",
				"minimum": 1,
				"maximum": 100,
				"description": "Maximum memories to return; defaults to 10"
			}
		},
		"additionalProperties": false
	}`),
	toolStore: json.RawMessage(`{
		"type": "object",
		"properties": {
			"content": {
				"type": "string",
				"description": "Durable fact or preference to remember"
			}
		},
		"required": ["content"],
		"additionalProperties": false
	}`),
	toolModify: json.RawMessage(`{
		"type": "object",
		"properties": {
			"id": {
				"type": "string",
				"description": "Stable memory ID returned by memory_store or memory_retrieve"
			},
			"content": {
				"type": "string",
				"description": "Complete replacement content"
			}
		},
		"required": ["id", "content"],
		"additionalProperties": false
	}`),
	toolForget: json.RawMessage(`{
		"type": "object",
		"properties": {
			"id": {
				"type": "string",
				"description": "Stable memory ID to forget"
			}
		},
		"required": ["id"],
		"additionalProperties": false
	}`),
}

type memoryTool struct {
	log  *Log
	kind toolKind
}

func (tool *memoryTool) Spec() agent.ToolSpec {
	name, description := toolIdentity(tool.kind)
	return agent.ToolSpec{
		Name:        name,
		Description: description,
		InputSchema: append(json.RawMessage(nil), toolSchemas[tool.kind]...),
	}
}

func (tool *memoryTool) Call(ctx context.Context, arguments json.RawMessage) (string, error) {
	switch tool.kind {
	case toolRetrieve:
		var input struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := decodeArguments(arguments, &input); err != nil {
			return "", fmt.Errorf("decode memory_retrieve arguments: %w", err)
		}
		records, err := tool.log.Retrieve(ctx, input.Query, input.Limit)
		if err != nil {
			return "", err
		}
		return encodeOutput(struct {
			Memories []Record `json:"memories"`
		}{Memories: records})
	case toolStore:
		var input struct {
			Content string `json:"content"`
		}
		if err := decodeArguments(arguments, &input); err != nil {
			return "", fmt.Errorf("decode memory_store arguments: %w", err)
		}
		record, err := tool.log.Store(ctx, input.Content)
		if err != nil {
			return "", err
		}
		return encodeOutput(record)
	case toolModify:
		var input struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		}
		if err := decodeArguments(arguments, &input); err != nil {
			return "", fmt.Errorf("decode memory_modify arguments: %w", err)
		}
		record, err := tool.log.Modify(ctx, input.ID, input.Content)
		if err != nil {
			return "", err
		}
		return encodeOutput(record)
	case toolForget:
		var input struct {
			ID string `json:"id"`
		}
		if err := decodeArguments(arguments, &input); err != nil {
			return "", fmt.Errorf("decode memory_forget arguments: %w", err)
		}
		id, err := validateID(input.ID)
		if err != nil {
			return "", err
		}
		if err := tool.log.Forget(ctx, id); err != nil {
			return "", err
		}
		return encodeOutput(struct {
			ID        string `json:"id"`
			Forgotten bool   `json:"forgotten"`
		}{ID: id, Forgotten: true})
	default:
		return "", errors.New("unsupported memory tool")
	}
}

func toolIdentity(kind toolKind) (string, string) {
	switch kind {
	case toolRetrieve:
		return "memory_retrieve", "Retrieve relevant long-term memories from the local memory log."
	case toolStore:
		return "memory_store", "Store one durable fact or preference in local long-term memory."
	case toolModify:
		return "memory_modify", "Replace the content of one existing long-term memory."
	case toolForget:
		return "memory_forget", "Forget one existing long-term memory."
	default:
		return "", ""
	}
}

func decodeArguments(data json.RawMessage, target any) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	if object == nil {
		return errors.New("arguments must be a JSON object")
	}
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

func encodeOutput(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode memory tool output: %w", err)
	}
	return string(data), nil
}
