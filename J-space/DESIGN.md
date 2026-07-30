# J-Space Workbench design decision

## Decision summary

Build J-space as an experimental top-level sibling Observer implementation and
research workbench, not as a J-agent runtime feature. Compose the existing
public `Model`, `Tool`, and `EventHandler` seams through J-tui's permissioned
completed-run Observer host, replay exact model turns through an instrumented
local checkpoint, and publish only a token-gated read-only projection through
the existing Funnel.

## Concrete evidence and constraints

- J-agent already exposes complete model requests to `Model` implementations
  and synchronous lifecycle events to consumers.
- The active local backend is oMLX with
  `Qwen3.6-35B-A3B-oQ4e-mtp`; its OpenAI-compatible HTTP API does not expose
  residual activations.
- Anthropic's reference implementation requires model weights, intermediate
  activations, and a model-specific fitted Jacobian lens.
- A fitted lens exists for the exact unquantized Qwen3.6-35B-A3B architecture.
- The current 64 GB host can hold the running quantized oMLX model and a
  short-lived second probe load, but memory pressure and replay latency are
  real operational costs.
- The existing public Funnel already terminates TLS and forwards to a
  path-allowlisted local gateway.

Hypotheses that remain unproven:

- how closely the base-model lens geometry transfers to the mixed-quantized
  oQ checkpoint;
- whether the useful workspace band has the same boundaries as Anthropic's
  studied models;
- whether observed token readouts explain J-agent behavioral differences.

## Invariants and policies

Invariant mechanism:

- immutable, versioned observation artifacts;
- exact model/lens/probe provenance;
- explicit distinction between live events and post-hoc activation replay;
- no raw transcript in the public projection by default;
- public J-space routes are read-only and independently authenticated.

Current policy and defaults:

- one Qwen3.6-35B-A3B/oMLX adapter;
- tail-position sampling to bound unembedding cost;
- heuristic early/middle/late layer labels;
- a single local state directory and latest-run viewer;
- Tailscale Funnel deployment under `/jspace/`.

## Openness decision

- Audience: repository users and external researchers who can run the same
  open-weight checkpoint.
- Shared contract: experimental `jspace.trace.v0.1` JSON artifacts and the
  read-only HTTP projection; `j.observer.run.v0.1` is the separate experimental
  product-host input.
- Private implementation: MLX layer traversal, PyTorch checkpoint decoding,
  local secret loading, process orchestration, and deployment lifecycle.
- Stability: experimental.
- Composition seams: artifact files, the J-agent `Model` wrapper, and a
  replaceable probe executable.

## Restraint decision

Required complexity retained:

- model/lens identity and quantization mismatch;
- replay provenance and failure state;
- per-layer/per-position token rankings;
- access control, rate limits, path allowlists, and secret hygiene;
- memory/latency limits for a 35B MoE checkpoint.

Speculative complexity removed:

- no generic activation Hook API in J-agent;
- no universal interpretability provider interface;
- no database, accounts, roles, collaboration, or remote task submission;
- no causal steering, ablation, training, or automated safety verdicts;
- no promise that layer bands or top tokens are universal semantics.

## Proposed design

```text
J-agent run
  -> J-tui's generic Observer wrapper captures authorized frames and events
  -> explicitly configured J-space process receives one bounded completed-run projection
  -> J-space owns HTTPS delivery, durable retry, and capture credentials
  -> local MLX probe replays each completed frame
  -> matching J_l transports residuals into the final-layer basis
  -> top token readouts enrich jspace.trace.v0.1
  -> local server exposes immutable read-only runs
  -> token-gated gateway publishes /jspace/ through the existing Funnel
```

The simplest alternative was to visualize streamed reasoning text and token
usage. It was rejected because it is not a J-space measurement. A provider
patch that exposes activations inline would reduce replay differences, but it
would couple this experiment to oMLX internals and disrupt the stable local
model service; it should wait for evidence that replay is insufficient.

## Deliberately not generalized

The probe does not accept arbitrary model architectures, hosted APIs, SAE
features, multi-token concept dictionaries, or steering vectors. The gateway
does not become a general hosting framework. The artifact schema is not a
stable J protocol.

## Remote capture boundary

Concrete requirement: a J-tui using the public home-model endpoint from another
computer must create the same truthful post-hoc observation without copying the
checkpoint or lens to that computer.

Invariant mechanism:

- J-tui generically projects authorized root model frames and lifecycle events
  to explicitly configured Observer processes.
- an authenticated, bounded experimental request durably hands one completed
  run to the home J-Space inbox;
- one local worker replays the frames and writes the existing artifact schema;
- delivery and replay failures remain explicit and retryable.

Current policy:

- capture is opt-in;
- the capture token is distinct from the read-only viewer token;
- raw capture frames exist only in mode-0600 outbox/inbox files until accepted
  and measured;
- one worker serializes replay to protect local model memory;
- only the validated Qwen profile is accepted.

The experimental `j.observer.run.v0.1` input belongs to the J-tui product host
and is read-only. J-space privately adapts it to `jspace.capture.v0.1`; J-tui no
longer owns a J-Space URL, token, outbox, retry policy, or protocol type. Neither
schema is a J-agent contract or a provider-wide hook. Root model frames are
measured; subagent lifecycle events may be displayed, but subagent model frames
are deliberately not combined across potentially different checkpoints.

Rejected alternatives:

- capturing every anonymous public model request would permit untrusted callers
  to trigger expensive replay;
- requiring MLX and the lens on each J-tui client would duplicate sensitive,
  machine-specific research state;
- exposing a generic write API would widen the stable surface before a second
  consumer validates it.

## Tradeoffs and risks

- Replay adds latency and may create memory pressure because the quantized
  checkpoint is loaded in a second process.
- A lens fitted on unquantized weights may shift under quantization.
- Top vocabulary tokens are lossy labels for an overcomplete sparse frame.
- Token-gated public access still exposes semantic summaries to anyone holding
  the token; rotation and non-persistence are therefore important.
- A capture credential allows its holder to schedule expensive local replay,
  so it is separate from viewing, rate limited, and should be rotated after
  disclosure.

## Validation

- Unit-test artifact parsing, authentication, allowlists, redaction, and
  checkpoint decoding.
- Build and race-test all Go programs.
- Run one real local Qwen prompt, verify the exact model/lens shapes, and retain
  the complete artifact including any failure.
- Inspect the page at desktop and narrow widths.
- Verify unauthenticated public API access is denied, authenticated access
  succeeds, existing model API paths still succeed, and public mutation paths
  remain denied.
