# J

J is a minimal, customizable agent runtime for Go.

> Status: experimental. The public Go package and `j-core` JSONL protocol may
> change before the first stable release.

## Mission

J keeps only the mechanisms required to run a model/tool loop:

- provider-neutral messages and tool calls
- explicit model and tool adapter contracts
- bounded synchronous execution with cancellation
- conversation state
- an optional FIFO task queue and canonical JSONL event stream

Everything else belongs in an adapter or product built on J.

## Non-goals

The core does not provide:

- subagents
- MCP or skills
- plugin discovery
- model fleets or provider selection
- memory or session persistence
- compaction and retry policy
- AG-UI, A2A, or provider-specific protocol translations
- a built-in shell tool

## Requirements

- Go 1.23 or newer

## Try the reference CLI

The checked-in binary uses a deterministic echo model. It verifies wiring
without provider credentials; it is not an LLM integration.

```bash
go run ./cmd/j "hello"
```

Build and verify:

```bash
make build
make check
```

## Embed J

Implement `agent.Model`, optionally implement `agent.Tool`, then construct an
agent. A model receives both conversation history and strongly described tool
schemas on every turn.

```go
package main

import (
	"context"
	"fmt"

	"github.com/z-chenhao/J/agent"
)

type model struct{}

func (model) Complete(
	ctx context.Context,
	request agent.ModelRequest,
) (agent.Message, error) {
	return agent.Message{
		Role:    agent.RoleAssistant,
		Content: "hello from a custom model",
	}, nil
}

func main() {
	runner, err := agent.New(model{})
	if err != nil {
		panic(err)
	}
	result, err := runner.Run(context.Background(), "hello", nil)
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Content)
}
```

The package is experimental because the model/tool seam has not yet been
validated by multiple independent production adapters. See
[Architecture](docs/architecture.md) for its intended stability boundary.

## JSONL reference transport

Run:

```bash
printf '%s\n' \
  '{"id":"1","type":"submit","message":"hello"}' |
  go run ./cmd/j --rpc
```

The experimental `j-core` protocol supports:

- `submit`
- `cancel`
- `task.get`
- `state`
- `messages`
- `reset`

The transport has one FIFO queue and one active task. It does not advertise
unimplemented commands or pretend to implement other agent protocols. See the
[protocol reference](docs/protocol.md).

## J-Space research

Anthropic's J-space is an emergent family of internal model representations
observed with the Jacobian lens. It is not an agent API or a runtime dependency.

J uses J-space research outside the core to compare open-weight models and
harness recipes. A recipe is adopted only when J-lens observations and
behavioral measurements agree that it improves a concrete outcome. The
reproducible study boundary is documented in
[research/jspace](research/jspace/README.md).

Primary sources:

- [Anthropic: A global workspace in language models](https://www.anthropic.com/research/global-workspace)
- [Jacobian lens reference implementation](https://github.com/anthropics/jacobian-lens)
- [Full research paper](https://transformer-circuits.pub/2026/workspace/)

## Project layout

```text
agent/              experimental embeddable Go runtime
cmd/j/              reference CLI and JSONL process
internal/demo/      deterministic reference model
internal/runtime/   private queue and JSONL transport
docs/               architecture and protocol contracts
research/jspace/    external model/harness research method
```

## Security

J executes only tools explicitly supplied by the embedding application. The
core does not ship an unrestricted shell tool. Tool implementers remain
responsible for authorization, sandboxing, input validation, and output limits.

Please report vulnerabilities as described in [SECURITY.md](SECURITY.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). New abstractions require a real
consumer, an independent variation axis, and evidence that the capability
belongs in the core.

## License

Apache-2.0. See [LICENSE](LICENSE).
