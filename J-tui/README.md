# J-tui

J-tui is a minimal terminal consumer of J-agent. It keeps one conversation,
renders the runtime event stream, and leaves model/tool execution and
transcript invariants in J-agent.

## Run

J-tui directly composes one existing J-agent adapter. Ollama remains the
default provider, an explicit model is required, and the first-party Bash tool
is enabled in the command's current working directory:

```bash
go run ./cmd/j-tui --model qwen3.6:27b-q4_K_M
```

DeepSeek uses the existing `DEEPSEEK_API_KEY` environment variable:

```bash
go run ./cmd/j-tui \
  --provider deepseek \
  --model deepseek-v4-flash \
  --thinking disabled
```

This explicit selector is command composition, not a model router. J-tui does
not discover providers or change providers during a conversation.

Run J-tui inside the repository container when commands must be isolated from
the host:

```bash
docker build -t j:dev ..
docker run --rm -it \
  -v "$PWD:/workspace" \
  --entrypoint j-tui \
  j:dev \
  --model qwen3.6:27b-q4_K_M \
  --base-url http://host.docker.internal:11434
```

J-tui renders tool activity but does not authorize or sandbox commands. Mounts,
credentials, networking, and resource limits belong to the container operator.

### DeepSeek prompt cache

DeepSeek's context cache is enabled automatically by the service; it does not
define a client-side enable flag. J-tui preserves one J-agent transcript and
appends each turn, so the next DeepSeek request retains the exact prior message
prefix required for a cache hit. JSON mode does the same when several prompts
are passed to one invocation.

The footer reports provider-observed cache hit and miss tokens. JSON event mode
keeps `usage.inputTokens` and `usage.cachedInputTokens`; cache misses are
`inputTokens - cachedInputTokens`. Cache population and hits remain
best-effort service behavior and cannot be guaranteed by the client.

The editor uses:

- `Enter` to submit;
- `Ctrl+J` to insert a newline;
- `PageUp` and `PageDown` to inspect history without following new output;
- `End` to resume following the newest output;
- `Ctrl+O` to expand or collapse tool arguments and results;
- `Esc` to cancel the active run;
- `Ctrl+C` to exit.

Mouse reporting is intentionally disabled so the terminal retains native text
selection and copy. Transcript scrolling stays available through `PageUp`,
`PageDown`, `Home`, and `End`.

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
  --model qwen3.6:27b-q4_K_M \
  --thinking enabled \
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
models. A local Ollama run exercises the real streaming adapter and Bash tool
paths:

```bash
make trace MODEL=qwen3.6:27b-q4_K_M
```

## Boundary

J-tui owns:

- terminal rendering and its private light/dark palette selection;
- Markdown presentation and collapsed or expanded tool cards;
- the textarea, scroll-follow policy, key bindings, and cancellation interaction;
- UI-only status reduction and the experimental JSON projection.

J-tui does not own persistence, memory, model routing, plugin discovery, tool
authorization, retry, or compaction. It composes J-agent's ordinary first-party
Bash Tool; no J-agent runtime API was added for this implementation. The
current event contract reports tool start and completion, not intermediate
progress; J-tui does not invent progress that J-agent did not observe.

See [the repository design](../docs/design.md).
