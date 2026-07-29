// Package observe owns J-tui's private projection of root and child Agent
// events. It does not extend J-agent's event contract.
package observe

import "github.com/z-chenhao/J/J-agent/agent"

// Event identifies the configured subagent that emitted Runtime. Subagent is
// empty for the root J-agent run.
type Event struct {
	Subagent string
	Runtime  agent.Event
}

// Handler observes the product-level projection synchronously.
type Handler func(Event)
