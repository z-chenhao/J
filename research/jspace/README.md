# J-Space model and harness research

This directory defines how J uses J-Space research to compare models and agent
harness recipes. It does not add J-Space to the runtime.

## What J-Space is

Anthropic describes the J-space as a small, emergent family of internal neural
representations that can be read with a Jacobian lens. The representations are
reportable, deliberately modulated, used for some multi-step reasoning, reused
flexibly, and selective relative to the model's total processing.

The official `anthropics/jacobian-lens` repository is a Python reference
implementation for open-weight decoder transformers. It is explicitly a
research implementation, is not maintained, and requires model weights and
intermediate activations. A normal hosted-model API does not expose the data
needed to claim a J-lens measurement.

## Research question

For a fixed model and task set:

> Which J harness recipe maximizes task success while minimizing turns, tokens,
> latency, and unsafe or invalid tool actions?

J-lens observations are diagnostic evidence about internal representations.
They are not a replacement for behavioral evaluation.

## Boundary

Research dependencies, model weights, fitted lenses, datasets, and GPU-specific
code stay outside J's Go module. This avoids:

- forcing Python or ML dependencies on runtime users
- coupling J to one interpretability implementation
- exposing model activations as a stable agent contract
- presenting an unvalidated harness as framework semantics

Research artifacts should live in a separate experiment checkout or an
ignored local directory and refer back to a J commit.

## Minimum experiment

Compare one baseline and one changed harness at a time.

Hold constant:

- exact model checkpoint and tokenizer
- generation parameters and random seeds
- tool implementations and schemas
- task corpus and scoring
- hardware and inference engine
- fitted J-lens checkpoint

Record:

- J commit
- model and lens identifiers
- harness text and context assembly
- tool schema encoding
- task-level outputs
- task success and invalid-action rate
- turns, input/output tokens, wall time, and peak memory
- selected J-lens tokens by layer and position

Do not aggregate away failed or timed-out runs.

## Harness axes to test

Each axis must be tested independently before combinations:

1. system instruction content
2. tool schema wording and ordering
3. placement of tool results in conversation history
4. context truncation policy
5. explicit reflection or planning instructions
6. error feedback presented after invalid tool calls

These are hypotheses, not J defaults.

## J-lens observations

Pre-register task-relevant concepts before inspecting results. Useful
observations may include:

- whether intermediate task concepts appear in the middle-layer workspace band
- whether they occur in a causally plausible order
- whether relevant concepts persist while unrelated concepts remain selective
- whether warning, injection, fabrication, or manipulation concepts appear
- whether a harness changes these observations consistently across seeds

Avoid interpreting a surfaced token as a complete thought or ground truth.

## Adoption gate

A harness recipe may become a documented J default only when:

1. J-lens results reproduce across seeds and a held-out task split;
2. behavioral task success improves;
3. invalid tool actions do not increase;
4. token or latency cost is acceptable;
5. a second model does not reveal an obvious model-specific failure;
6. raw results and the scoring procedure are publishable.

If only J-lens metrics improve, keep the recipe experimental. If only behavior
improves, record the result without claiming J-Space explains it.

## Reproducibility record

For each experiment, create a small Markdown report containing:

```text
date:
J commit:
research code commit:
model checkpoint:
lens checkpoint:
hardware:
task corpus:
baseline harness:
candidate harness:
behavioral metrics:
J-lens observations:
failures:
decision:
```

No performance claim belongs in J's README until a completed report supports
it.

## Primary sources

- <https://www.anthropic.com/research/global-workspace>
- <https://transformer-circuits.pub/2026/workspace/>
- <https://github.com/anthropics/jacobian-lens>
