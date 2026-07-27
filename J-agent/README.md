# J-agent

J-agent is a minimal, customizable agent runtime for Go.

> Status: experimental. The public Go package and `j-agent` JSONL protocol may
> change before the first stable release.

## Mission

J-agent keeps only the mechanisms required to run a model/tool loop:

- ordered text, reasoning, and tool-call content
- explicit model provider and tool contracts
- streaming model output
- context-governed execution and cancellation
- conversation state
- an optional FIFO task queue and typed JSONL event stream

Everything else belongs in a provider, Tool, or product built on J-agent.

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

## OpenAI provider

J-agent has one experimental streaming Chat Completions Provider with two
explicit wire APIs. `openai-completions` is the default and serves DeepSeek,
oMLX, Ollama, and other compatible servers. `azure-openai-completions` owns the
Azure deployment route, `api-version` query, and `api-key` authentication. The
base URL and model remain opaque operator configuration.

The repository's local recipe uses oMLX:

```bash
go run ./cmd/j-agent \
  --provider openai \
  --model Qwen3.6-35B-A3B-oQ4e-mtp \
  --base-url http://127.0.0.1:8000/v1 \
  --api-key-env OMLX_API_KEY \
  --reasoning-field reasoning_content \
  "Explain why small interfaces are useful."
```

DeepSeek uses the same provider:

```bash
export DEEPSEEK_API_KEY='...'

go run ./cmd/j-agent \
  --provider openai \
  --model deepseek-v4-pro \
  --base-url https://api.deepseek.com \
  --api-key-env DEEPSEEK_API_KEY \
  --reasoning-field reasoning_content \
  "Explain why small interfaces are useful."
```

Ollama's OpenAI-compatible endpoint is typically
`http://127.0.0.1:11434/v1`; use `--reasoning-field reasoning` when retained
reasoning must be replayed during tool continuation. Use
`--reasoning-field omit` for models that do not require reasoning history.
`--reasoning-effort default|none|low|medium|high|max` sends the standard
`reasoning_effort` field only when it is not `default`; the selected server and
model decide which values they support.

An Azure OpenAI-compatible endpoint selects its protocol explicitly:

```bash
export GPT_5_5_API_KEY='...'

go run ./cmd/j-agent \
  --provider openai \
  --api azure-openai-completions \
  --api-version 2024-02-01 \
  --model gpt-5.5-2026-04-24 \
  --base-url https://example.internal/modelhub \
  --api-key-env GPT_5_5_API_KEY \
  "What is 1+1?"
```

The Azure base URL is the endpoint root. The Provider appends
`/openai/deployments/{model}/chat/completions` and the API-version query. It
does not inspect the endpoint hostname or model name.

The API key itself is never accepted as a command-line value.
`--api-key-env` names the environment variable to read and defaults to
`OPENAI_API_KEY`, or `AZURE_OPENAI_API_KEY` when the Azure API is selected.
The equivalent environment configuration also supports `J_AGENT_API` and
`J_AGENT_API_VERSION` alongside `J_AGENT_PROVIDER`, `J_AGENT_MODEL`,
`J_AGENT_BASE_URL`, `J_AGENT_API_KEY_ENV`, `J_AGENT_REASONING_FIELD`, and
`J_AGENT_REASONING_EFFORT`.

DeepSeek and oMLX own their server-side prompt/KV caches. J-agent preserves the
append-only message prefix and maps both DeepSeek cache fields and
`prompt_tokens_details.cached_tokens` to `Usage.CachedInputTokens`. It sends no
fictitious client-side cache switch.

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
  --provider openai \
  --base-url http://host.docker.internal:11434/v1 \
  --model qwen3 \
  --reasoning-field reasoning \
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

Use the checked-in provider or implement the small `agent.Model` contract:

```go
package main

import (
	"context"
	"fmt"

	"github.com/z-chenhao/J/J-agent/agent"
	"github.com/z-chenhao/J/J-agent/provider/openai"
)

func main() {
	model, err := openai.New(openai.Config{
		Model:   "qwen3",
		BaseURL: "http://127.0.0.1:11434/v1",
	})
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

The `agent` package is experimental. One Provider implementation is exercised
against two explicit Chat Completions protocols and several servers, but that
does not substitute for an independent `Model` implementation or external
production consumers.
See [Architecture](docs/architecture.md) and
[model protocol research](docs/model-protocol.md).

## JSONL reference transport

Run:

```bash
printf '%s\n' \
  '{"id":"1","type":"submit","message":"hello"}' |
  go run ./cmd/j-agent --rpc \
    --provider openai \
    --model qwen3 \
    --base-url http://127.0.0.1:11434/v1
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
agent/              experimental embeddable Go runtime
cmd/j-agent/        reference CLI and JSONL process
internal/runtime/   private queue and JSONL transport
provider/openai/    experimental OpenAI Chat Completions provider
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
