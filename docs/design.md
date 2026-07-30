# J design and engineering decisions

This is the single repository-level source for J's architecture, module
ownership, extension strategy, and accumulated design conclusions.

Last reviewed: 2026-07-30.

## 1. Concrete goal

J explores one question:

> What is the smallest truthful agent runtime that remains easy to customize?

J is the umbrella repository. J-agent is its independently embeddable runtime
kernel. J-tui, J-mcp, J-mem, J-packages, J-skills, J-subagents, and J-space are
first-party consumers used to prove that the public seams are sufficient for
real customization.

The repository name also comes from the Jacobian and the J-space research
direction. That identity is implemented by the top-level `J-space` sibling. It
observes J-agent through the same public seams and J-tui's permissioned Observer
host, without making model internals a runtime responsibility.

Current verified consumers of J-agent are its reference CLI, private JSONL
runtime, J-tui, J-mcp, J-mem, J-packages, J-skills, J-subagents, and the
standalone `examples/embedding/custom-host` module. One OpenAI Provider
exercises the model seam through OpenAI-compatible and Azure OpenAI Chat
Completions protocols; the backing servers do not count as independent `Model`
implementations. J-tui consumes the public event and cancellation seams, J-mcp
projects MCP tools through `agent.Tool`, and J-mem persists `History` and
supplies four ordinary memory Tools. J-skills projects standard capability
resources through one Tool, J-subagents constructs isolated child Agents behind
another Tool, all without a runtime API change.

## 2. Engineering constitution

### Openness over Possession

Open stable mechanisms that others can understand, replace, wrap, and compose.
Do not require consumers to adopt J's preferred UI, persistence, model,
orchestration, or distribution policy.

Openness does not mean exposing mutable internals, promising universality, or
stabilizing an interface before real consumers have validated it.

### Restraint over Complexity

Keep the smallest set of concepts that preserves correctness, lifecycle,
provider compatibility, cancellation, and observability. Product features and
speculative variation axes remain outside the kernel.

Short code is not automatically simple. Explicit transcript and tool-call
invariants are required domain complexity; generic Hooks and untyped extension
bags are speculative complexity.

## 3. What Pi demonstrates

Pi's ecosystem is enabled by three distinct layers:

1. `pi-agent-core` owns the model/tool loop, transcript state, tool execution,
   and lifecycle events.
2. `pi-coding-agent` owns the product extension host: UI, commands, sessions,
   resource discovery, trust, compaction, and broad interception Hooks.
3. Pi packages and the catalog own installation, discovery, and distribution.

Pi's agent core accepts initial messages, while SQLite storage lives in a
separate package. Its official subagent example is implemented as an ordinary
registered tool. These precedents support opening transcript, model, tool, and
event mechanisms without moving memory, subagent, MCP, or plugin products into
J-agent.

Pi's `--mode json` also demonstrates that a UI can expose its event input as a
JSON Lines diagnostic stream. J-tui adopts only that narrow mechanism:
`j-tui --mode json` projects J-agent's existing events and does not claim Pi
protocol compatibility. Pi's queue, compaction, retry, extension, and session
events remain product policy outside J-agent.

Pi also separates provider identity, wire API, model metadata, settings scope,
and stored authentication. Its custom model configuration can select an API
such as `openai-completions` independently from the provider name. J-tui adopts
only the currently proven subset: one user-scoped file of named model
connections, an explicit API protocol, and string values that may reference
environment variables through `${ENV_VAR}`. Literal credentials remain
possible because the connection schema must not require one secret source, but
the starter file recommends environment references. Project merging, a
credential store, command-based secret resolution, model catalogs,
compatibility bags, and an interactive settings editor would add policy
without a current consumer and are deferred.

Primary sources:

- [Pi agent core](https://github.com/badlogic/pi-mono/tree/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/agent)
- [Pi Bash Tool](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/agent/src/harness/tools/bash.ts)
- [Pi shell execution](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/agent/src/harness/env/nodejs.ts)
- [Pi extension system](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/coding-agent/docs/extensions.md)
- [Pi SDK and ResourceLoader](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/coding-agent/docs/sdk.md)
- [Pi package format](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/coding-agent/docs/packages.md)
- [Pi JSON event stream mode](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/coding-agent/docs/json.md)
- [Pi settings](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/settings.md)
- [Pi custom models](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/models.md)
- [Pi subagent example](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/coding-agent/examples/extensions/subagent/index.ts)
- [Pi skills](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/docs/skills.md)
- [Agent Skills specification](https://agentskills.io/specification)

The conclusion is not that J should copy Pi's entire mutable state or
ExtensionAPI. Pi is a mature coding product; J-agent is intentionally only a
runtime. J should copy the separation of concerns, then open fewer seams until
real J consumers prove more are necessary.

## 4. Repository architecture

```text
J/
├── J-agent/   minimal model/tool loop
├── J-tui/     terminal presentation and interaction
├── J-mcp/     MCP client lifecycle and Tool projection
├── J-mem/     local persistence and memory tools
├── J-packages/ explicit package installation and product-host composition
├── J-skills/  Agent Skills validation and progressive resource loading
├── J-subagents/ isolated foreground child Agents projected as a Tool
├── J-space/   optional Jacobian-lens observer package and workbench
└── docs/      repository-level design decisions
```

Dependency rules:

- J-agent must not import J-tui, J-mcp, J-mem, J-packages, J-skills,
  J-subagents, J-space, or future J product modules.
- Sibling product modules may depend on J-agent's experimental public Go API.
- Sibling modules must not own each other's policy.
- Product entrypoints compose modules; the runtime does not discover them.
- Component-specific protocol details remain inside the owning component.

## 5. J-agent boundary

J-agent owns:

- ordered provider-neutral `Message`, `Content`, and `ToolCall` values;
- the `Model` variation axis and typed streaming deltas;
- the `Tool` variation axis and tool-call/result correlation;
- context-governed model/tool execution and cancellation;
- ordered synchronous lifecycle events;
- one serialized conversation per `Agent`;
- defensive transcript snapshots and validated construction-time restoration.

J-agent currently opens four orthogonal composition seams:

| Seam | Purpose | Stability |
| --- | --- | --- |
| `Model` | Providers, routing, research Harness wrappers | Experimental |
| `Tool` | External capabilities, memory tools, MCP bridges, delegation | Experimental |
| `EventHandler` | TUI, gateway, logs, metrics | Experimental |
| `History` / `WithHistory` | External transcript persistence and restoration | Experimental |

One concrete OpenAI provider serves two explicit Chat Completions protocols
behind the `Model` seam: `openai-completions` for DeepSeek, Ollama, and the
local oMLX integration, and `azure-openai-completions` for Azure deployment
routing. This is protocol reuse, not independent `Model` implementations.
oMLX owns KV/prefix-cache storage and policy; J-agent owns only ordered
transcript continuity and provider-reported usage normalization.

`WithHistory` is intentionally construction-only. The restored transcript is
authoritative, including any system message. J-agent validates message shape,
tool identity, tool-result correlation, and completed tool batches before
accepting it.

Recovery stops at complete transcript checkpoints. J-tui persists the accepted
root user turn and every complete model/tool turn, then reconstructs an Agent
through `WithHistory`; it does not claim to resume an in-flight provider stream
or partially completed tool batch. J-subagents now proves a second real
transcript consumer: a host may give a child a stable identity, save the same
checkpoints, and restore it through the same `WithHistory` seam. Child identity,
storage keys, and parent ownership remain outside J-agent.

J-agent does not own:

- databases, files, durability, retention, or session identity;
- UI rendering or terminal input;
- long-term memory policy;
- subagent roles or orchestration plans;
- MCP connection lifecycle;
- skills, plugin discovery, package installation, or project trust;
- gateway transports or per-chat routing;
- retry, compaction, model fleets, or provider selection policy.

## 6. First-party Bash and container boundary

### Concrete requirement and consumers

The reference `j-agent` command and J-tui both need to inspect and modify the
workspace in which the agent is running. A model/tool loop with no supplied
tools truthfully reports that it cannot act. These two real consumers justify
one first-party Bash implementation; they do not justify a terminal framework.

The supported deployment premise is that a reference agent runs inside a
container. The repository Docker image makes that premise executable by fixing
the command working directory at `/workspace` and running as an unprivileged
user. A direct host invocation remains available for development, but it grants
Bash the host permissions of that process and is not an isolation boundary.

### Invariant mechanism and operator policy

The experimental `J-agent/tool/bash` package implements the existing
`agent.Tool` contract. Its domain-required behavior is:

- one construction-time working directory and resolved Bash executable;
- strict JSON arguments containing a command and optional timeout;
- combined stdout/stderr capture in observed write order;
- removal of terminal control characters before model or TUI display;
- bounded tail output of at most 2000 lines or 50KB;
- process-group termination on timeout or context cancellation on the
  supported Linux and macOS targets;
- model-visible command failure and partial output.

The container operator owns mounts, credentials, environment, network, Linux
capabilities, resource limits, image provenance, and disposal. Those are real
security semantics but are not agent runtime semantics.

### Openness and stability

`tool/bash.New` is experimental public composition. The reference commands
select it explicitly through `WithTools`; importing package `agent` alone
grants no process capability. The implementation opens a concrete reusable
Tool, while its buffers and process machinery remain private.

The following are deliberately not exposed or generalized:

- `Terminal`, `Executor`, `Sandbox`, `Approval`, or command-policy interfaces;
- PTY or interactive terminal sessions;
- runtime tool discovery or mutation;
- a provider-specific shell prompt;
- automatic persistence of complete command output.

The last choice differs intentionally from Pi. Persisting every truncated
command result would add a secret-bearing storage lifecycle; J currently keeps
bounded tail output and lets the model issue a narrower follow-up command.

Rejected alternatives:

- A shell branch inside the agent loop would privilege one Tool and couple
  process policy to transcript invariants.
- Auto-discovering tools would make capabilities ambient and harder to audit.
- A generic execution environment would predict unverified backends before a
  second implementation exists.
- Prompt-only authorization would describe a policy without enforcing it.

## 7. First-party consumers

### J-tui

J-tui is a minimal terminal UI. It calls J-agent, consumes lifecycle events,
reads transcript snapshots, and cancels runs through `context.Context`.
Its command explicitly composes the OpenAI Provider with an operator-selected
API protocol and endpoint; it does not own dynamic model routing or provider
discovery.

DeepSeek context caching requires no new runtime mechanism: the service enables
it automatically, and J-agent's ordered transcript makes each new turn an
extension of the prior request prefix. Provider-reported cached input tokens
remain ordinary `Usage`; J-tui may derive and display cache misses from total
input minus cached input. J-tui aggregates complete usage across the current
run and preserves an unknown optional breakdown when any model turn omits it.
Cache policy and storage remain provider-owned.

It owns rendering, key bindings, terminal state, input editing, and UI-specific
metadata. J-agent must not gain labels, colors, widgets, or TUI component types
to serve J-tui.

J-tui also owns its user-scoped `~/.j/config.json`, named model profiles, and
binary distribution. The file separates the `openai` Provider implementation
from its selected `api` wire protocol. Its `apiKey` is a string value with
optional `${ENV_VAR}` references, so environment variables are a recommended
source rather than a schema requirement. Command-line flags override
environment variables, which override the selected profile. This is an
experimental J-tui contract, not a J-agent configuration API.
Project-local configuration merging, runtime profile mutation, credential
store integration, provider discovery, and a general settings framework remain
deliberately absent.

The starter configuration may select one experimentally operated public model
endpoint so a new J-tui installation can validate the real OpenAI-compatible
stream without first owning model infrastructure. That endpoint is a
replaceable product default, not a J-agent capability or a stable provider
contract. Its tunnel, upstream credential injection, rate limits, capacity,
and availability remain deployment policy outside this repository. J-tui
documents the network and privacy boundary, while users retain the same
`baseURL` seam for local or independently hosted services.

When transcript persistence is configured, J-tui creates a fresh named session
for each normal invocation and immediately records its empty snapshot. Exact
restoration remains explicit through `--session`; `--no-session` selects an
ephemeral invocation. Pi's default new persisted session is the validated
product precedent, but J deliberately does not copy Pi's branching JSONL tree,
compaction, automatic continuation selector, or session manager into J-agent.
Session identity and the SQLite snapshot recipe remain J-tui/J-mem policy over
`History` and `WithHistory`.

The Tavily integration also proved a second MCP transport recipe. Launching the
remote server through `npx mcp-remote` made package startup dominate J-tui
launch, while the remote endpoint itself spoke Streamable HTTP. J-tui now
constructs the official SDK transport directly. Tavily authentication and
vendor endpoints proved that the stable input is a small map of HTTP header
strings, not a Bearer-token policy. Header values share the private
literal-plus-`${ENV_VAR}` resolver used by model profiles, while protocol-owned
HTTP/MCP headers remain closed. OAuth policy, command-based secret lookup,
cached tool schemas, background daemons, and a J-specific transport interface
remain unjustified.

Real Tavily use exposed intermittent TLS handshakes beyond Go's ten-second
default. J-tui therefore privately clones the default HTTP transport and
extends only its TLS handshake budget to twenty seconds, preserving proxy,
pooling, and certificate behavior. It does not expose network tuning or enable
request retries; those remain unjustified without evidence that this bounded
change is insufficient.

Stdio startup diagnostics reuse J-mcp's existing stderr seam. J-tui privately
captures a bounded tail during initialization, reports it only on failure, and
discards runtime stderr rather than mixing server logs with MCP stdout, J-agent
events, or terminal rendering. This does not add a logging contract to J-mcp or
J-agent.

The first implementation uses existing start/delta/completed events. Its
private reducer maps them to `thinking`, `responding`, `tool`, `completed`,
`failed`, and `canceled` display states. J-tui displays reasoning content by
default and privately owns its collapse policy, Markdown rendering, tool-card
expansion, run-state colors, and whether a manually scrolled viewport follows
new output. `--mode json` exposes an experimental JSON projection of the same
event input for event-order diagnostics.

The current terminal policy follows Bubble Tea v2's event-loop-owned background
query, maps the observed result to one of two private palettes, and uses
Bubbles v2's one-to-five-line dynamic editor. Lip Gloss and Glamour remain pure
renderers and do not compete with Bubble Tea for terminal input, so OSC
background replies cannot become user text. Mouse reporting remains disabled so
the host terminal owns native selection and copy; keyboard navigation owns
transcript scrolling. This validated variation axis does not justify a general
theme or mouse contract, and no terminal detail enters J-agent. J-tui follows
the repository-wide Go 1.26 baseline.

The implementation proved that text streaming, reasoning observation,
tool start/completion, model observations, failure, and cancellation can be
represented without a new J-agent API. Tool-progress events should be proposed
only if a future real tool cannot be represented truthfully with the current
start/completed lifecycle.

### J-mcp

J-mcp is an independently usable MCP-to-J-agent bridge. `DialStdio` provides
the first concrete process recipe; `Connect` accepts the official MCP Go SDK's
`Transport`, preserving that standard variation axis without defining a
J-specific Transport interface.

The first successful `Tools` call paginates, validates, and freezes the
server's tools as ordinary `agent.Tool` values. Calls preserve context
cancellation and distinguish protocol failures from MCP tool failures.
Version 0.1 accepts text results and rejects non-text or structured-only
results explicitly. Text output is bounded at 1 MiB.

J-mcp does not construct an Agent, read J-tui configuration, render UI,
discover packages, or define a universal Extension interface. Its stdio child
process is a protocol boundary, not an operating-system sandbox.

### J-mem

J-mem contains two deliberately separate capabilities:

1. Short-term conversation persistence stores J-agent transcripts in local
   SQLite and restores them through `WithHistory`.
2. Long-term memory uses local JSONL and exposes retrieval, storage,
   modification, and forgetting as ordinary J-agent tools.

The implemented transcript store atomically replaces complete snapshots in a
versioned SQLite schema. Loaded values are intended for `WithHistory`, where
J-agent revalidates transcript invariants.

The implemented long-term store is an inspectable append-only JSONL event log.
`store`, `modify`, and `forget` operations append events. Retrieval
materializes active records, ranks phrase and term matches first, and fills the
bounded result with recent records for model-side semantic relevance
selection. Its four model capabilities are
`memory_retrieve`, `memory_store`, `memory_modify`, and `memory_forget`.

J-mem owns schema migration, file formats, retrieval ordering, retention,
conflict handling, and memory policy. The bounded hybrid policy is experimental
and was earned by a real false negative: `位置 城市 出发 所在地` did not
lexically contain the stored fact `用户现在在杭州`. Returning recent candidates
lets the already participating Agent model judge semantic relevance without a
second provider call or opaque index.

Current memory research separates small high-value in-context memory from
larger archival semantic search. A-MEM and Zep show useful structured-note and
temporal-graph policies for larger memory systems, while current trust research
warns that semantic recall is also an admission boundary rather than a
similarity-only feature. J does not yet have the corpus, independent embedder,
temporal facts, or measured failure modes that would justify those systems.

Primary sources:

- [Letta context hierarchy](https://docs.letta.com/guides/core-concepts/memory/context-hierarchy)
- [A-MEM](https://arxiv.org/abs/2502.12110)
- [Zep temporal knowledge graph](https://arxiv.org/abs/2501.13956)
- [Beyond Similarity](https://arxiv.org/abs/2606.06054)

Cross-process writers, embeddings, vector databases, graph synthesis,
compaction, and ambient injection remain absent. The JSONL log remains
authoritative and inspectable. J-agent does not define a universal `Memory` or
`Storage` interface.

Ambient retrieval or context injection should wait until the tool-based design
proves insufficient. If required, a model wrapper should be tried before
adding a runtime Hook.

### J-skills

J-skills is an independently usable consumer of the Agent Skills standard. Its
concrete users are an embedding application that supplies explicit skill roots
and J-tui's typed construction-time host. It recursively finds directories
containing `SKILL.md`, validates required YAML metadata and standard naming
rules, and exposes one `skill_read` Tool.

The invariant mechanism is progressive disclosure: names and descriptions are
part of the Tool contract at Agent construction, while the complete
instructions and referenced files enter the transcript only when the model
requests them. Resource reads are confined beneath the selected skill
directory and bounded at 1 MiB. The common `{baseDir}` placeholder is replaced
on read for compatibility with existing Pi skills.

Discovery roots are explicit. J-skills does not choose global or project
locations, install packages, run scripts, interpret the experimental
`allowed-tools` field, or own trust policy. Those are host policies and remain
unstabilized. Strict standard validation is preferable to silently changing
skill identity; a future compatibility relaxation needs a real failing skill
consumer.

J-tui may select an exact subset from the discovered immutable catalog and
exposes list/check audit commands. Selection is host policy; J-skills adds only
the narrow catalog operation needed to validate exact names deterministically.

### J-subagents

J-subagents proves that isolated child runs fit J-agent's existing Model,
Tool, event, and transcript seams. A host supplies named definitions containing
a Model, optional system prompt, and exact Tools. The module returns one
`subagent_run` Tool. Calls for one definition are serialized because
`agent.Model` does not promise concurrent safety. An optional existing
`EventHandler` observes child lifecycle events; the host attaches configured
subagent identity in its own private projection.

J-tui is the first product host. Each recipe may select an existing model
profile and an exact subset of the already composed non-subagent Tools.
Omitting the tool list inherits that snapshot; an explicit empty list creates
a model-only child. The subagent Tool is added last, so recursive delegation is
not ambient.

`NewTool` retains the minimal fresh-transcript recipe. `NewSessionTool` is an
experimental optional composition for a host that already owns a durable
parent session. It returns a host-generated child session ID, scopes opaque
storage keys to the parent and configured recipe, and accepts that exact ID on
a later call. Its narrow `TranscriptStore` contract is implemented by
J-mem's existing SQLite transcript store without making J-subagents depend on
J-mem. External hosts can implement the same two complete-snapshot operations.
As with root restoration, the saved child transcript is authoritative; a later
recipe prompt change applies only to new child sessions.

Checkpoint semantics are explicit. The accepted child user turn and each
complete model/tool turn are restorable. Failed calls return their child
session ID with the error. An in-flight provider stream, a partial tool batch,
or a hard crash before the parent observes the child ID is not automatically
replayed or discovered. Automatic retry would make side effects ambiguous and
therefore remains absent.

This design deliberately has no J-delegate intermediate and no J-agent
`SubAgent`, `Session`, or `Storage` interface. Background jobs, parallel and
chain orchestration, steering, worktrees, approval, recursive inheritance,
session browsing, hard-crash discovery, and a universal registry remain
outside this foreground mechanism. Child-provider usage is returned in the
Tool result rather than being misattributed to the parent model's `RunResult`.
Concurrent resume of one child from multiple processes is unsupported; adding
leases or optimistic revisions before a multi-process consumer exists would
turn a local transcript recipe into a speculative workflow store.

### External information retrieval

J does not currently ship a dedicated external-information module. Tavily
already exposes its capability through standard MCP, and J-tui can connect to
that Streamable HTTP server directly through J-mcp. A second direct Tavily API
wrapper would duplicate authentication, networking, configuration, tool
selection, and maintenance without adding an independently valuable retrieval
mechanism.

This is a restraint decision, not a claim that retrieval has no product value.
A future module should first prove a J-owned capability such as retrieval
planning, source normalization, multi-backend evidence, indexing, or
reproducible quality evaluation. Its name and contract should follow that
proven mechanism. `J-web` remains unallocated so it can naturally name a future
Web UI; neither that product nor a generic search-provider interface is
promised yet.

## 8. Extension and package composition

The composition root belongs to the embedding application. A source-level Go
host may wrap any `Model`, provide any `Tool`, fan out events, and own transcript
storage without a manifest, registry, or J product module. The standalone
custom-host example is built with `GOWORK=off` and no `replace` directive so
workspace-local modules cannot hide a missing public dependency. The complete
embedding recipe is documented in [Embed and assemble J-agent](embedding.md).

Prebuilt products are necessarily narrower: they can load only capabilities
expressed through a protocol they understand. This is why MCP, Agent Skills,
and J-tui's completed-run Observer remain distinct protocols rather than being
collapsed into one universal Extension lifecycle.

The current package requirement does not justify a universal Extension API.
J-agent already has the required mechanisms: construction-time Tools and
external Agent Skills projected through a Tool. J-mcp initializes MCP sessions
and projects their tools to `agent.Tool` before an Agent is constructed.

J-packages owns the narrow shared installation and construction mechanism
validated by J-tui and a second custom J-agent host. Version 0.1 supports only
package-owned stdio MCP servers and Agent Skills roots. The CLI records
explicit local paths or pinned Git sources in a user-owned private registry. It
performs no project discovery, hidden prompt injection, or runtime mutation.
The complete contract is [J Package Protocol 0.1](packages.md).

J-packages deliberately does not become an ontology of every product
extension. A prebuilt host may define a separate typed process protocol for a
real product capability, but that protocol remains owned by the host instead
of adding `memory`, `observer`, `ui`, `provider`, or `session` contribution
categories to the shared package manifest.

The experimental [J MCP Extension Composition Protocol](extensions.md) defines
the direct host configuration lifecycle, cancellation path, and deliberately
narrow tool projection. Neither direct MCP configuration nor packages add a
J-specific wire protocol: J-mcp speaks MCP directly. They also do not use Go
runtime plugins, runtime tool mutation, or generic configuration payloads.

J-mem, J-skills, and J-subagents provide meaningfully different Tool-producing
modules. J-mem also owns transcript lifecycle, J-skills owns resource
discovery, and J-subagents consumes Model plus Tool recipes. J-tui therefore
composes them through separate typed sections rather than pretending they
share MCP's connection lifecycle. These differences are evidence against
extracting a common lifecycle prematurely. `Extension`, `Plugin`, `Skill`,
`SubAgent`, and `MCP` remain absent from J-agent.

J-space is the first J-tui Observer implementation. J-tui owns the experimental
`j.observer.run.v0.1` completed-run process protocol, captures frames by wrapping
the public `Model` seam, projects safe lifecycle metadata from its existing
event stream, and invokes selected Observer processes with bounded input and
time. Observer failure is diagnostic and cannot change the authoritative Agent
result. J-space owns capture credentials, HTTPS delivery, durable retry, replay,
and visualization policy.

J-tui configures Observers directly through a typed product-owned process
recipe. Installing J-space does not activate observation. This keeps the
Observer protocol open to any executable implementation without requiring
J-packages or teaching J-agent a product lifecycle.

## 9. Planned external modules

These names describe product modules, not promised J-agent interfaces:

| Module | Initial composition strategy |
| --- | --- |
| J-mcp | Implemented independent bridge; map MCP tools to `agent.Tool`; own connection lifecycle |
| J-skills | Implemented standard skill discovery and progressive resource Tool |
| J-subagents | Implemented foreground isolated child Agents with optional transcript restoration |
| J-packages | Implemented explicit local/Git installation and MCP/Skills composition |
| J-space | Implemented independent J-tui Observer and Jacobian-lens workbench |
| J-gateway | Map platform chat identity to external Agent/session ownership |

`J-plugins` is deliberately not allocated. The implemented package protocol
first validates whether standards-based construction-time composition solves
the real use case before any broader plugin domain is named.

## 10. Deliberately not generalized

Do not add these to J-agent without a failing real integration:

- `Memory`, `Session`, `SubAgent`, `Skill`, `Plugin`, `MCP`, or `Transport`
  framework interfaces;
- universal `Before`/`After` Hooks;
- arbitrary `map[string]any` metadata;
- runtime mutation of model, tools, or transcript;
- UI rendering details on tools or messages;
- parallel-tool, retry, compaction, or model-fleet policy;
- an arbitrary-code or dynamic-library loader.

Potential future mechanisms must be validated in this order:

1. Build the narrow consumer outside J-agent.
2. Identify the exact blocked operation.
3. Try composition through `Model`, `Tool`, events, or transcript first.
4. Add the smallest experimental runtime primitive only if wrapping is
   insufficient.
5. Stabilize only after another independent consumer validates the same
   semantics.

## 11. Decisions retained

- J is the Git and GitHub repository root.
- J uses Go 1.26 across its modules and workspace. CI follows the latest
  `1.26.x` patch, repository development selects at least Go 1.26.5, and J does
  not maintain an older-version matrix without a real consumer.
- J-agent remains a separate Go module under `J/J-agent`.
- The OpenAI provider's typed Chat Completions API modes are real integration
  code; specific backend profiles remain J-tui recipes rather than J-agent
  features.
- J-tui may ship a replaceable, best-effort public model profile as an
  experimental onboarding default; public hosting policy remains outside the
  runtime and carries no availability promise.
- J-tui owns its experimental profile file and release artifacts; J-agent does
  not read product configuration or install packages. J-packages is a sibling
  product-host module and its `j` CLI owns explicit installation.
- J-Space is part of J's research identity and is an independent top-level
  Observer implementation and workbench; it is not a runtime dependency or a
  model-internals promise in J-agent's public API.
- Reasoning content is preserved when provider continuation requires it.
- The JSONL process is a reference transport, not universal framework
  semantics.
- Tool authorization belongs to embedders or tool wrappers, not prompt-only
  policy.
- Reference commands compose the first-party Bash Tool; the container, not the
  agent loop, owns execution isolation.
- Public APIs remain experimental before independent production consumers.
- Extension hosting remains product-owned construction-time composition;
  J-agent does not discover or load extensions.
- J-mcp, J-mem, J-packages, J-skills, J-subagents, and J-space are independent Go
  modules; none depends on J-tui.
- J Package 0.1 supports stdio MCP and Agent Skills only. J-tui owns its typed
  read-only Observer process protocol; broader plugin Hooks, UI, model
  mutation, transcript mutation, and runtime mutation remain absent.
- J-skills implements the external Agent Skills format without moving Skill
  semantics into J-agent.
- J-subagents is the direct subagent module; J-delegate does not exist.

## 12. Minimal development order

1. Keep J-agent passing its protocol and race tests.
2. Keep the text-only J-tui and JSON event trace passing against current events.
3. Keep J-mcp's real stdio, cancellation, and J-agent integration tests
   passing.
4. Keep J-tui's typed MCP host, exact construction-time tool selection,
   discovery audit, collision checks, environment forwarding, and shutdown
   tests passing.
5. Keep J-mem's SQLite transcript round-trip and four JSONL memory Tools
   passing through J-tui's separate memory lifecycle.
6. Keep J-skills standard validation, bounded resource confinement, and
   progressive Tool loading passing.
7. Keep J-subagents' fresh and restored transcripts, parent isolation,
   complete-checkpoint persistence, explicit capability selection,
   cancellation, child event projection, and provider usage result passing.
8. Keep J-packages manifest confinement, private registry, explicit
   local/pinned-Git lifecycle, MCP process, custom-host Agent loop, and
   independent memory-package tests passing.
9. Keep J-tui's package Tool/Skill composition, disable switch, collision
   checks, and package-only audit commands passing.
10. Validate provider-backed MCP and subagent calls as operational smoke tests
   without a J-agent API change.
11. Re-audit actual integration friction before changing J-agent.
12. Widen J Package only after multiple independent packages and hosts prove
   one missing cross-host standard contribution type; keep product-specific
   process protocols with their owning hosts.
