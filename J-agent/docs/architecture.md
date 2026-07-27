# Architecture

## Decision summary

J-agent exposes the smallest currently justified embedding seam: ordered model
content, tool specifications and calls, streaming deltas, normalized completion
facts, and synchronous per-run events. The agent loop owns the invariants.
Product policy, transports, and research tooling remain replaceable outside it.

The public API and wire protocol are experimental. Stability must be earned by
real consumers rather than declared from the first implementation.

## Concrete requirements

Current in-repository consumers are:

- the reference `cmd/j-agent` CLI
- the private JSONL queue used by the reference process
- J-tui's interactive and JSON event modes

The OpenAI Provider is exercised through the `openai-completions` API against
DeepSeek, Ollama, and oMLX, and through the
`azure-openai-completions` API's deployment-routing contract. Those protocols
and servers validate one `Model` implementation; they do not count as
independent implementations of the `Model` seam. Applications embedding the
Go `agent` package are the intended external audience, but external production
consumers have not yet been verified.

The runtime must:

- preserve conversation and tool-call identity
- preserve provider-required reasoning across tool continuations
- give models the exact tool schemas they may use
- return tool errors to the model
- keep tool-call continuation cancelable without imposing a runtime round cap
- propagate cancellation
- make task terminal states unambiguous
- emit an ordered, writable event stream
- report comparable stop, usage, and latency facts

Run-level mandatory token counts sum the provider-reported turns. An optional
usage breakdown remains absent when any reported turn omits that breakdown, so
unknown cached or reasoning tokens are not silently counted as misses or zero.

## Mechanism and policy

### Runtime mechanism

- `agent.Message`, ordered `agent.Content`, and `agent.ToolCall`
- `agent.Model` and `agent.Tool`
- typed model deltas and normalized model observations
- context-governed model/tool execution
- cancellation through `context.Context`
- defensive conversation snapshots
- validated construction-time conversation restoration
- FIFO task admission and one terminal task state

One `Agent` owns one serialized conversation. A run commits its user message
before model execution and then commits only completed, accepted model/tool
messages. An invalid model response is not committed. A tool round means one
accepted model response containing one or more tool calls, followed by
execution of those calls. J-agent does not count or cap tool rounds; the run
continues until the model returns a final answer or the caller's
`context.Context` ends.

Per-run event handlers execute synchronously to preserve ordering. They may
read `History`, but must not reenter `Run` or `Reset` on the same `Agent`.

### Replaceable policy

- system prompts
- provider selection and credentials
- concrete tools and their authorization
- retry, compaction, persistence backends, and memory policy
- model routing and harness recipes
- transport and downstream protocol translation

The OpenAI Provider validates the model seam against two explicit Chat
Completions protocols. The JSONL process is a reference transport, not
mandatory framework semantics.

The first-party `tool/bash` package is replaceable policy implemented through
the existing `Tool` seam. The reference commands opt into it; the `agent`
package does not discover or grant process capabilities.

## Openness decision

### Experimental public contract

Package `agent` is intended for external Go embedders. Its responsibility is
one conversation-scoped model/tool loop. Provider wire details live outside
the `agent` package; the agent package does not own provider selection, process
transport, or durable task state.

`WithHistory` is an experimental construction-time seam for restoring the
complete transcript returned by `History`. The runtime validates message
shapes, tool-call/result correlation, and completion of tool batches before
accepting restored state. It deliberately does not define a storage interface,
session identifier, persistence format, retention policy, or runtime message
mutation API.

`tool/bash.New` is an experimental first-party Tool constructor. It exposes one
fixed-workspace Bash capability because the reference commands and J-tui are
real consumers. Its output and cancellation invariants are public behavior;
its buffering and process implementation remain private.

Evolution strategy:

- breaking changes are allowed before the first stable release
- changes require executable contract tests
- the API becomes stable only after independent provider implementations expose shared
  semantics

### Private implementation

`internal/runtime` owns the current queue, IDs, JSONL parsing, and event
serialization. These are private because only one process transport consumes
them today.

### Experimental wire contract

`j-agent` version `0.1` is a canonical stream for the reference process. It is
documented so downstream experiments are possible, but it is not yet a
compatibility promise.

## Deliberately not generalized

J-agent does not define universal interfaces for:

- subagents
- memory
- skills or plugins
- MCP
- model discovery
- retry and compaction
- session trees
- multiple agent protocols
- J-space activations or interpretability signals
- arbitrary event metadata
- terminals, executors, approvals, or sandboxes

These capabilities should be composed outside the core until multiple real
implementations reveal a stable mechanism.

## Relationship to J

J-agent is an independently usable runtime kernel inside the J repository.
Its sibling projects J-tui and J-mem demonstrate a default customization; they
do not become mandatory runtime layers, and their planned existence does not
justify interfaces before real integration work exposes an independent
variation axis.

J-tui and J-mem should initially consume the same experimental public seams as
third-party embedders. A seam may be revised while experimental, and should
become stable only after those implementations and at least one independent
consumer validate the same semantics.

## J-Space boundary

J-Space research evaluates models and harness recipes; it does not change the
runtime domain model. The official Jacobian-lens implementation requires access
to model weights and intermediate activations, so it cannot be inferred from a
normal hosted-model API response.

Research may recommend a default harness recipe only after:

1. a pinned model and task corpus produce reproducible J-lens observations;
2. the same recipe improves behavioral task success;
3. latency, token, and operational costs are measured;
4. the result holds on a held-out task set.

Until then, a harness remains an external experiment.
