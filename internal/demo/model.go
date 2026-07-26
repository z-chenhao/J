// Package demo contains the deterministic model used by the reference CLI.
// It is deliberately not part of J's public runtime contract.
package demo

import (
	"context"
	"strings"

	"github.com/z-chenhao/J/agent"
)

// Model echoes the latest message so the binary can be exercised without
// provider credentials. It is not an LLM adapter.
type Model struct{}

// Complete implements agent.Model.
func (Model) Complete(_ context.Context, request agent.ModelRequest) (agent.Message, error) {
	if len(request.Messages) == 0 {
		return agent.Message{Role: agent.RoleAssistant}, nil
	}
	latest := request.Messages[len(request.Messages)-1]
	return agent.Message{
		Role:    agent.RoleAssistant,
		Content: "J received: " + strings.TrimSpace(latest.Content),
	}, nil
}
