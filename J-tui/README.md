# J-tui

J-tui is a minimal terminal consumer of J-agent. It keeps one conversation,
renders the runtime event stream, and leaves model/tool execution and
transcript invariants in J-agent.

## Install

### Go toolchain

With Go 1.26 or newer:

```bash
go install github.com/z-chenhao/J/J-tui/cmd/j-tui@latest
```

Ensure the Go binary directory, normally `~/go/bin`, is on `PATH`.

### Prebuilt binary

Versioned releases provide checked archives for macOS and Linux on amd64 and
arm64:

```bash
curl -fsSL https://raw.githubusercontent.com/z-chenhao/J/main/J-tui/install.sh | sh
```

The installer:

- downloads only from this repository's latest GitHub Release;
- selects the current operating system and architecture;
- verifies the archive against the published SHA-256 checksums;
- installs to `~/.local/bin` without requiring root.

Set `J_TUI_INSTALL_DIR` to select another directory.

## First run

Create the starter configuration:

```bash
j-tui --init-config
```

This creates `~/.j/config.json` with permissions `0600` and refuses to
overwrite an existing file. It contains connection recipes, not API keys:

```json
{
  "defaultProfile": "omlx",
  "profiles": {
    "omlx": {
      "provider": "openai",
      "api": "openai-completions",
      "model": "Qwen3.6-35B-A3B-oQ4e-mtp",
      "baseURL": "http://127.0.0.1:8000/v1",
      "apiKeyEnv": "OMLX_API_KEY",
      "reasoningField": "reasoning_content"
    },
    "deepseek": {
      "provider": "openai",
      "api": "openai-completions",
      "model": "deepseek-chat",
      "baseURL": "https://api.deepseek.com",
      "apiKeyEnv": "DEEPSEEK_API_KEY",
      "reasoningField": "reasoning_content"
    },
    "ollama": {
      "provider": "openai",
      "api": "openai-completions",
      "model": "qwen3",
      "baseURL": "http://127.0.0.1:11434/v1",
      "reasoningField": "reasoning"
    }
  }
}
```

Start the default profile or select another:

```bash
j-tui
j-tui --profile deepseek
j-tui --profile ollama
```

Export `OMLX_API_KEY` or `DEEPSEEK_API_KEY` when the selected service requires
it. J-tui reads only the named environment variable; credentials are not stored
in the configuration file.

Use `--config <path>` or `J_TUI_CONFIG` to select another file. Configuration
precedence is:

```text
command-line flag > J_TUI_* environment variable > selected profile > default
```

The default file is user-scoped. J-tui does not search parent directories,
merge project settings, mutate the file during a conversation, or expose the
configuration schema through J-agent.

`provider` selects the J-agent Provider implementation. `api` independently
selects its wire protocol. Existing profiles that omit `api` continue to use
`openai-completions`.

An Azure OpenAI Chat Completions profile is:

```json
{
  "provider": "openai",
  "api": "azure-openai-completions",
  "apiVersion": "2024-02-01",
  "model": "gpt-5.5-2026-04-24",
  "baseURL": "https://example.internal/modelhub",
  "apiKeyEnv": "GPT_5_5_API_KEY"
}
```

The endpoint and model are opaque configuration. Azure mode appends
`/openai/deployments/{model}/chat/completions`, adds the API-version query, and
uses the `api-key` header. It deliberately does not provide arbitrary headers,
query parameters, or request-body configuration.

## MCP and memory

J-tui can compose optional J-mcp and J-mem capabilities from the same typed
configuration. Presence enables a capability; omission disables it. The
starter configuration does not enable either one.

Add these top-level fields alongside `profiles`:

```json
{
  "extensions": {
    "mcp": {
      "servers": {
        "filesystem": {
          "command": "npx",
          "args": [
            "-y",
            "@modelcontextprotocol/server-filesystem",
            "/Users/you/Projects"
          ]
        },
        "github": {
          "command": "github-mcp-server",
          "args": ["stdio"],
          "env": ["GITHUB_TOKEN"]
        }
      }
    }
  },
  "memory": {
    "transcript": {
      "path": "state/transcripts.db"
    },
    "longTerm": {
      "path": "state/memory.jsonl"
    }
  }
}
```

`extensions.mcp` accepts explicit stdio servers. `command` is required;
`args`, `env`, and `cwd` are optional. `env` contains environment-variable
names rather than values. J-tui supplies `PATH`, `HOME`, and available
operational locale/temp variables, then forwards only the additionally named
variables. A named variable that is not set fails startup.

Relative `cwd` and memory paths are resolved from the directory containing the
configuration file. With the default `~/.j/config.json`, the example memory
files are therefore `~/.j/state/transcripts.db` and
`~/.j/state/memory.jsonl`.

Select a transcript explicitly:

```bash
j-tui --session project-j
```

An existing session is restored before Agent construction; a missing session
starts empty. Each successful run atomically replaces that session's SQLite
snapshot. `J_TUI_SESSION` provides the environment equivalent. Without
`--session`, the transcript store is not opened. Supplying a session without
`memory.transcript` is rejected.

Long-term memory is independent of transcript sessions. When configured, it
adds `memory_retrieve`, `memory_store`, `memory_modify`, and `memory_forget` as
ordinary model-visible Tools backed by inspectable JSONL. Retrieval remains an
explicit Tool call; J-tui does not inject memory into prompts.

All configured MCP servers initialize before the Agent starts, and their
advertised Tools remain frozen for that process. A startup failure or a name
collision with Bash, J-mem, or another MCP server fails explicitly. J-tui does
not install MCP packages, sanitize tool names, retry servers, or silently omit
failed capabilities.

## Run from the repository

J-tui directly composes J-agent's OpenAI Provider. The repository's
local development recipe selects the oMLX model
`Qwen3.6-35B-A3B-oQ4e-mtp`; the first-party Bash tool is enabled in the
command's current working directory:

```bash
make run
```

The equivalent explicit invocation is:

```bash
go run ./cmd/j-tui \
  --provider openai \
  --api openai-completions \
  --model Qwen3.6-35B-A3B-oQ4e-mtp \
  --base-url http://127.0.0.1:8000/v1 \
  --api-key-env OMLX_API_KEY \
  --reasoning-field reasoning_content
```

DeepSeek uses the same provider with its own endpoint and credential
environment:

```bash
go run ./cmd/j-tui \
  --provider openai \
  --api openai-completions \
  --model deepseek-v4-flash \
  --base-url https://api.deepseek.com \
  --api-key-env DEEPSEEK_API_KEY \
  --reasoning-field reasoning_content
```

Ollama uses `--base-url http://127.0.0.1:11434/v1` and, when reasoning history
is required for tool continuation, `--reasoning-field reasoning`. These are
explicit command recipes, not provider discovery or a model router.

Run J-tui inside the repository container when commands must be isolated from
the host:

```bash
docker build -t j:dev ..
docker run --rm -it \
  -v "$PWD:/workspace" \
  --entrypoint j-tui \
  j:dev \
  --provider openai \
  --model qwen3.6:27b-q4_K_M \
  --base-url http://host.docker.internal:11434/v1 \
  --reasoning-field reasoning
```

J-tui renders tool activity but does not authorize or sandbox commands. Mounts,
credentials, networking, and resource limits belong to the container operator.

### Prompt and KV cache

DeepSeek's context cache is enabled automatically by the service; it does not
define a client-side enable flag. oMLX owns its configurable hot-memory and SSD
KV/prefix cache. J-tui does not pretend to enable either provider's server-side
storage: it preserves one J-agent transcript and appends each turn, so the next
request retains the exact prior message prefix required for reuse. JSON mode
does the same when several prompts are passed to one invocation.

The footer reports cache hit and miss tokens aggregated across the current run
when every model turn reports the required usage fields. It leaves an
unreported or partial cache breakdown unknown instead of counting it as a
miss. JSON event mode keeps each turn's `usage.inputTokens` and
`usage.cachedInputTokens`; per-turn cache misses are `inputTokens -
cachedInputTokens`. The `openai-completions` API maps
`prompt_tokens_details.cached_tokens` into that same provider-neutral usage
field and also understands DeepSeek's cache hit fields. Cache population and
hits remain best-effort service behavior and cannot be guaranteed by the
client.

The editor uses:

- `Enter` to submit;
- `Ctrl+J` to insert a newline;
- `Alt+Up` and `Alt+Down` to scroll the transcript one line without taking
  the arrow keys away from input editing;
- `PageUp` and `PageDown` to inspect history without following new output;
- `Home` to jump to the oldest output;
- `End` to resume following the newest output;
- `Ctrl+O` to expand or collapse tool arguments and results;
- `Esc` to cancel the active run;
- `Ctrl+C` to exit.

Mouse reporting is intentionally disabled so the terminal retains native text
selection and copy. Transcript scrolling stays available through `Alt+Up`,
`Alt+Down`, `PageUp`, `PageDown`, `Home`, and `End`.

Reasoning deltas update the visible run state but their private content is not
rendered. The transcript shows only a `Thinking…` presence marker. Assistant
text streams through a terminal Markdown renderer. Tool cards retain complete
arguments, results, and failures while keeping a compact default presentation.
Model duration, first-delta timing, and token usage are rendered from J-agent
events. A spinner and the editor border color reflect the current run state.
The editor starts at one row and grows to five rows as content wraps.

The renderer follows Bubble Tea v2's terminal lifecycle. `Init` requests the
background color, Bubble Tea reads the terminal reply inside its event loop,
and a `BackgroundColorMsg` selects J-tui's light or dark internal palette.
Before that observation arrives, the first frame uses a dark fallback. Lip
Gloss and Glamour only receive the observed choice and never probe stdin
themselves, so an OSC reply cannot race the textarea reader and appear as
`]11;rgb:...` input.

This is one private presentation decision, not a public theme framework. J-tui
does not expose arbitrary theme configuration or move terminal semantics into
J-agent. J-tui follows the repository-wide Go 1.26 baseline.

## JSON event mode

Pi's JSON event mode demonstrates a useful diagnostic seam: the same lifecycle
events that drive an interactive UI can be written as JSON Lines. J-tui adopts
that mechanism without adopting Pi's protocol or product-level session events:

```bash
go run ./cmd/j-tui \
  --mode json \
  --provider openai \
  --model Qwen3.6-35B-A3B-oQ4e-mtp \
  --base-url http://127.0.0.1:8000/v1 \
  --api-key-env OMLX_API_KEY \
  --reasoning-field reasoning_content \
  "Use bash to run pwd, then report the output."
```

Each output line is one projection of `agent.Event`. Event names and payloads
remain J-agent's own contract. Durations are represented as `durationMs` for
inspection. The command is experimental and is intended to verify event order,
streaming deltas, tool lifecycle, failures, and terminal state.

JSON mode includes reasoning deltas and tool output when the runtime emits
them. Treat its stdout as sensitive local diagnostic data.

The full-screen TUI and JSON mode do not share a second event taxonomy: both
consume the same synchronous J-agent `EventHandler` stream.

## Event tracking

The TUI reducer keeps this mapping private to J-tui:

| J-agent observation | TUI state |
| --- | --- |
| `agent.started`, `turn.started` | running, then thinking |
| reasoning `message.delta` | thinking; content remains hidden |
| text `message.delta` | responding with streamed transcript text |
| tool-call `message.delta` | preparing tool |
| `tool.started`, `tool.completed` | named tool row with running/result/failure |
| `turn.completed` | model duration, first-delta timing, and usage become observable |
| `agent.completed` | ready for the next prompt |
| failed lifecycle event | explicit failed state and error |
| canceled `context.Context` | canceled state |

Tests exercise complete tool and failure event sequences with deterministic
models. The default local oMLX run exercises the real streaming provider and
Bash tool paths:

```bash
make trace
```

## Boundary

J-tui owns:

- terminal rendering and its private light/dark palette selection;
- Markdown presentation and collapsed or expanded tool cards;
- the textarea, scroll-follow policy, key bindings, and cancellation interaction;
- typed product configuration, session selection, and module composition;
- UI-only status reduction and the experimental JSON projection.

J-tui does not implement persistence, memory policy, MCP, model routing,
plugin discovery, tool authorization, retry, or compaction. It composes the
independent J-mem and J-mcp modules plus J-agent's ordinary first-party Bash
Tool; no J-agent runtime API was added. The current event contract reports
tool start and completion, not intermediate progress; J-tui does not invent
progress that J-agent did not observe.

See [the repository design](../docs/design.md).
