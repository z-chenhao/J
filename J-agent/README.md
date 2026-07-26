# J-agent

J-agent is a minimal, customizable agent runtime for Go.

> Status: experimental. The public Go package and `j-agent` JSONL protocol may
> change before the first stable release.

## Mission

J-agent keeps only the mechanisms required to run a model/tool loop:

- ordered text, reasoning, and tool-call content
- explicit model and tool adapter contracts
- streaming model output
- context-governed execution and cancellation
- conversation state
- an optional FIFO task queue and typed JSONL event stream

Everything else belongs in an adapter or product built on J-agent.

## Relationship to J

J-agent is the standalone runtime kernel inside the `J` repository. Its sibling
projects J-tui and J-mem demonstrate optional composition; they are not
dependencies or privileged features of J-agent. They use the same public seams
available to every other consumer.

## Non-goals

The core does not provide:

- subagents
- MCP or skills
- plugin discovery
- model fleets or automatic provider selection
- memory or session persistence
- compaction and retry policy
- a privileged terminal abstraction or sandbox

## Requirements

- Go 1.26 or newer; repository development selects Go 1.26.5 or a newer
  compatible toolchain

## Use Ollama

J-agent uses Ollama's native `/api/chat` protocol. Choose the model explicitly:

```bash
go run ./cmd/j-agent \
  --provider ollama \
  --model qwen3 \
  "Explain why small interfaces are useful."
```

Set a custom endpoint with `--base-url` or `J_AGENT_BASE_URL`. Enable or disable
thinking explicitly with `--thinking enabled` or `--thinking disabled`;
`default` leaves the choice to Ollama.

## Use DeepSeek

J-agent uses DeepSeek's streaming Chat Completions protocol. The model name is
deliberately required because provider model names and defaults can change.

```bash
export DEEPSEEK_API_KEY='...'

go run ./cmd/j-agent \
  --provider deepseek \
  --model deepseek-v4-pro \
  "Explain why small interfaces are useful."
```

The API key is read only from `DEEPSEEK_API_KEY`, not from a command-line flag.
Provider, model, base URL, and thinking mode may also be supplied through
`J_AGENT_PROVIDER`, `J_AGENT_MODEL`, `J_AGENT_BASE_URL`, and
`J_AGENT_THINKING`. DeepSeek reasoning
effort is available as `--reasoning-effort high|max` or
`J_AGENT_REASONING_EFFORT`.

## First-party Bash tool

The `tool/bash` package is an ordinary implementation of `agent.Tool`. It fixes
its working directory at construction time, validates arguments, combines
stdout and stderr, removes terminal control characters, limits model-visible
output to the last 2000 lines or 50KB, and kills the command process group on
timeout or context cancellation on the supported Linux and macOS targets.

The reference `j-agent` command enables this tool in its current working
directory. In the repository image that directory is `/workspace`, so the
container owns filesystem, credential, network, and resource isolation:

```bash
docker build -t j:dev ..
docker run --rm -i \
  -v "$PWD:/workspace" \
  j:dev \
  --provider ollama \
  --base-url http://host.docker.internal:11434 \
  --model qwen3 \
  "Use bash to run pwd."
```

An embedding application still chooses its complete tool set through
`agent.WithTools`; importing `agent` alone grants no process capability. The
Bash package does not define a generic terminal, executor, approval, or sandbox
interface.

The runtime does not impose a tool-round limit. Model/tool continuation remains
open until the model returns a final answer or the caller's `context.Context`
ends. Deadlines, cancellation, and any product execution budget remain
composition policy outside J-agent.

## Embed J-agent

Use a checked-in adapter or implement the small `agent.Model` contract:

```go
package main

import (
	"context"
	"fmt"

	"github.com/z-chenhao/J/J-agent/adapter/ollama"
	"github.com/z-chenhao/J/J-agent/agent"
)

func main() {
	model, err := ollama.New(ollama.Config{Model: "qwen3"})
	if err != nil {
		panic(err)
	}
	runner, err := agent.New(model)
	if err != nil {
		panic(err)
	}
	result, err := runner.Run(context.Background(), "hello", nil)
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Message.Text())
}
```

Conversation persistence stays outside the runtime. A store can save the deep
snapshot returned by `History` and restore it when constructing a new agent:

```go
saved := runner.History()

restored, err := agent.New(model, agent.WithHistory(saved...))
if err != nil {
	panic(err)
}
fmt.Println(len(restored.History()))
```

`WithHistory` validates and copies a complete runtime transcript. Restored
history is authoritative, including any system message, so a non-empty
`WithSystemPrompt` cannot be supplied at the same time. Storage format,
durability, session identity, and retention policy remain application concerns.

The `agent` package is experimental. Its seam is now exercised by two
independent adapters, but stability will be declared only after real external
consumers validate its semantics. See [Architecture](docs/architecture.md) and
[model protocol research](docs/model-protocol.md).

## JSONL reference transport

Run:

```bash
printf '%s\n' \
  '{"id":"1","type":"submit","message":"hello"}' |
  go run ./cmd/j-agent --rpc --provider ollama --model qwen3
```

The experimental `j-agent` protocol supports:

- `submit`
- `cancel`
- `task.get`
- `state`
- `messages`
- `reset`

Its event stream exposes message deltas, normalized model identity, stop
reason, token usage, first-delta latency, total model/tool latency, and task
queue/run duration. See the [protocol reference](docs/protocol.md).

## Build and verify

```bash
make build
make check
```

## Project layout

```text
adapter/deepseek/   DeepSeek protocol adapter
adapter/ollama/     Ollama native protocol adapter
agent/              experimental embeddable Go runtime
cmd/j-agent/        reference CLI and JSONL process
internal/runtime/   private queue and JSONL transport
tool/bash/          experimental first-party Bash Tool
docs/               architecture and protocol contracts
research/           external model and harness research
```

## Security

The `agent` package executes only tools explicitly supplied by the embedding
application. The reference command deliberately supplies `tool/bash`; it runs
with the permissions of the containing process. Use the repository container
or an equivalent isolation boundary, expose only the intended workspace and
credentials, and set network and resource policy outside J-agent.

Please report vulnerabilities as described in
[the repository security policy](../SECURITY.md).

## Contributing

See [the repository contribution guide](../CONTRIBUTING.md). New abstractions require a real
consumer, an independent variation axis, and evidence that the capability
belongs in the core.

## License

Apache-2.0. See [LICENSE](../LICENSE).
