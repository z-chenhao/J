# Model protocol boundary

This note records the evidence used to define J-agent's experimental model seam. The
source snapshot was taken on 2026-07-26.

## Compared systems

### pi-agent

Snapshot: `badlogic/pi-mono@5bc1c2c0a6f07e00e8c240304182f213ab8d311f`

Relevant sources:

- [`packages/ai/src/types.ts`](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/ai/src/types.ts)
- [`packages/agent/src/types.ts`](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/agent/src/types.ts)
- [`packages/agent/src/agent-loop.ts`](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/agent/src/agent-loop.ts)

pi separates the agent loop from provider conversion. Its model-facing message
schema uses discriminated text, thinking, image, and tool-call blocks; assistant
messages carry provider/model identity, usage, and a normalized stop reason.
Streaming has typed start, block delta/end, done, and error events. Tool results
retain both call ID and tool name.

### OpenCode

Snapshot: `anomalyco/opencode@7534d23551f665e65080809975b4ca5c7d63807b`

Relevant sources:

- [`packages/schema/src/v1/session.ts`](https://github.com/anomalyco/opencode/blob/7534d23551f665e65080809975b4ca5c7d63807b/packages/schema/src/v1/session.ts)
- [`packages/opencode/src/session/message-v2.ts`](https://github.com/anomalyco/opencode/blob/7534d23551f665e65080809975b4ca5c7d63807b/packages/opencode/src/session/message-v2.ts)
- [`packages/opencode/src/session/processor.ts`](https://github.com/anomalyco/opencode/blob/7534d23551f665e65080809975b4ca5c7d63807b/packages/opencode/src/session/processor.ts)

OpenCode's durable product schema is a message plus ordered parts. Tool parts
use explicit pending, running, completed, and error states. Text/reasoning
deltas are separate events keyed by message and part identity. Provider
metadata is preserved only where it is needed to replay the same provider.
Session, compaction, patch, retry, subtask, cost, and permission parts are
product semantics, not a minimal model protocol.

### Codex

Snapshot: `openai/codex@61a44880a85d2fd0d8770908dea5733495e571c8`

Relevant sources:

- [`codex-rs/protocol/src/models.rs`](https://github.com/openai/codex/blob/61a44880a85d2fd0d8770908dea5733495e571c8/codex-rs/protocol/src/models.rs)
- [`codex-rs/protocol/src/protocol.rs`](https://github.com/openai/codex/blob/61a44880a85d2fd0d8770908dea5733495e571c8/codex-rs/protocol/src/protocol.rs)
- [`codex-rs/app-server-protocol/src/protocol/v2/item.rs`](https://github.com/openai/codex/blob/61a44880a85d2fd0d8770908dea5733495e571c8/codex-rs/app-server-protocol/src/protocol/v2/item.rs)
- [`codex-rs/app-server/README.md`](https://github.com/openai/codex/blob/61a44880a85d2fd0d8770908dea5733495e571c8/codex-rs/app-server/README.md)

Codex keeps provider response items distinct from its app-server product
protocol. Model items include typed message content and function-call output
identity. The app-server uses item started/delta/completed notifications and
separate turn status. Token usage distinguishes input, cached input, output,
and reasoning output. Approval, sandbox, hook, collaboration, and thread
schemas are Codex product domains and do not belong in a minimal runtime.

## Confirmed common mechanism

The three implementations independently justify these concepts:

1. ordered, discriminated message content;
2. text and reasoning as distinct content;
3. tool-call identity, name, and JSON arguments;
4. tool results correlated with the call;
5. typed incremental model output;
6. explicit successful stop reason;
7. provider-reported token usage;
8. model/provider identity for diagnostics and replay.

J-agent exposes exactly this subset through `agent.Message`, `agent.Content`,
`agent.ToolCall`, `agent.ModelDelta`, `agent.ModelResponse`, and
`agent.ModelObservation`.

This is not a claim that one universal agent protocol exists. It is a narrow
model/tool loop contract shared by the current consumers.

## JSON Schema boundary

`agent.ToolSpec.InputSchema` must contain a valid JSON object. J-agent does not claim
to implement or validate every JSON Schema draft or provider subset.

- DeepSeek accepts function `parameters` using JSON Schema semantics. Its
  strict mode is a beta API with a narrower supported subset, so J-agent does not
  enable strict mode or promise that every schema will be accepted.
- Ollama's OpenAI-compatible API accepts the same function/parameters shape,
  but actual enforcement still depends on the selected local model.

Providers pass the caller's schema through without rewriting it. Provider
rejection remains explicit. A cross-provider schema normalizer should wait
until incompatible real tools demonstrate a stable transformation rule.

## OpenAI-compatible provider mapping

Provider sources:

- [DeepSeek Chat Completions](https://api-docs.deepseek.com/api/create-chat-completion)
- [DeepSeek thinking and tool continuation](https://api-docs.deepseek.com/guides/thinking_mode)
- [DeepSeek context cache](https://api-docs.deepseek.com/zh-cn/guides/kv_cache/)
- [Ollama OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility)
- [oMLX repository and tiered KV cache](https://github.com/jundot/omlx)

| J-agent concept | DeepSeek | Ollama | oMLX |
| --- | --- | --- | --- |
| base URL recipe | `https://api.deepseek.com` | `http://127.0.0.1:11434/v1` | `http://127.0.0.1:8000/v1` |
| endpoint and stream | `/chat/completions`, SSE | `/chat/completions`, SSE | `/chat/completions`, SSE |
| retained reasoning field | `reasoning_content` | `reasoning` | `reasoning_content` |
| tool call arguments | JSON encoded as a string | JSON encoded as a string | JSON encoded as a string |
| tool result correlation | `tool_call_id` | `tool_call_id` | `tool_call_id` |
| stop reason | `finish_reason` | `finish_reason` | `finish_reason` |
| input/output usage | `prompt_tokens` / `completion_tokens` | `prompt_tokens` / `completion_tokens` | `prompt_tokens` / `completion_tokens` |
| cache usage | DeepSeek cache fields | not reported by chat usage | `prompt_tokens_details.cached_tokens` |

One `provider/openai` implementation owns this narrow Chat Completions
contract. Its typed `ReasoningField` setting covers the only request-history
shape difference observed in tool continuation; the response reader accepts
both reasoning fields. It does not expose a generic extra-body map, provider
profile registry, or arbitrary compatibility hook. None of these differences
appear as conditionals in the agent loop.

DeepSeek enables its context cache automatically. J-agent does not send a
provider-specific cache switch: its ordered, append-only transcript preserves
the exact request prefix needed for reuse across conversation turns. The
provider maps `prompt_cache_hit_tokens` to `Usage.CachedInputTokens`.
`prompt_cache_miss_tokens` remains derivable as
`Usage.InputTokens - Usage.CachedInputTokens`, because DeepSeek defines prompt
tokens as the sum of cache hits and misses. The provider keeps
`CachedInputTokens` nil when the provider reports neither cache field, so an
unreported metric is not confused with an observed zero hit. Cache population
is best effort and therefore remains an observed usage result, not a runtime
guarantee.

oMLX exposes a separate server-owned KV/prefix cache. The provider does not send
a fictitious client cache switch. It maps oMLX's observed
`prompt_tokens_details.cached_tokens` metric to `Usage.CachedInputTokens`, while
the ordered J-agent transcript supplies the stable prefix that makes reuse
possible.

## Deliberately not generalized

J-agent does not currently standardize:

- image, audio, or file content;
- provider-specific signatures or opaque encrypted reasoning state;
- structured final-output schemas;
- partial tool execution output;
- parallel tool execution policy;
- retry, compaction, cost, or context-window policy;
- approvals, permissions, sessions, trees, subagents, plugins, or hooks;
- model capability discovery.

These are either absent from current J-agent consumers or belong to product policy.
They can be added reversibly after a real second use case proves the shared
semantics.

## Stability

The model seam and `j-agent` protocol remain experimental. One provider
implementation exercised against three servers shows that the current boundary
is useful, but independent implementations, external consumers, and production
experience are still required before a compatibility promise is justified.
