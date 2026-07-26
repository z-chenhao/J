# Architecture

## Decision summary

J exposes the smallest currently justified embedding seam: ordered model
content, tool specifications and calls, streaming deltas, normalized completion
facts, and synchronous per-run events. The agent loop owns the invariants.
Product policy, transports, and research tooling remain replaceable outside it.

The public API and wire protocol are experimental. Stability must be earned by
real consumers rather than declared from the first implementation.

## Concrete requirements

Current consumers are:

- applications embedding the Go `agent` package
- the reference `cmd/j` CLI
- the private JSONL queue used by the reference process

The runtime must:

- preserve conversation and tool-call identity
- preserve provider-required reasoning across tool continuations
- give models the exact tool schemas they may use
- return tool errors to the model
- bound tool-call loops
- propagate cancellation
- make task terminal states unambiguous
- emit an ordered, writable event stream
- report comparable stop, usage, and latency facts

## Mechanism and policy

### Runtime mechanism

- `agent.Message`, ordered `agent.Content`, and `agent.ToolCall`
- `agent.Model` and `agent.Tool`
- typed model deltas and normalized model observations
- bounded model/tool execution
- cancellation through `context.Context`
- defensive conversation snapshots
- FIFO task admission and one terminal task state

### Replaceable policy

- system prompts
- provider selection and credentials
- concrete tools and their authorization
- retry, compaction, persistence, and memory
- model routing and harness recipes
- transport and downstream protocol translation

The DeepSeek and Ollama adapters validate the model seam. The JSONL process is
a reference transport, not mandatory framework semantics.

## Openness decision

### Experimental public contract

Package `agent` is intended for external Go embedders. Its responsibility is
one conversation-scoped model/tool loop. Provider wire details live in public
adapter packages; the agent package does not own provider selection, process
transport, or durable task state.

Evolution strategy:

- breaking changes are allowed before the first stable release
- changes require executable contract tests
- the API becomes stable only after independent adapters expose shared
  semantics

### Private implementation

`internal/runtime` owns the current queue, IDs, JSONL parsing, and event
serialization. These are private because only one process transport consumes
them today.

### Experimental wire contract

`j-core` version `0.1` is a canonical stream for the reference process. It is
documented so downstream experiments are possible, but it is not yet a
compatibility promise.

## Deliberately not generalized

J does not define universal interfaces for:

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

These capabilities should be composed outside the core until multiple real
implementations reveal a stable mechanism.

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
