# J design and engineering decisions

This is the single repository-level source for J's architecture, module
ownership, extension strategy, and accumulated design conclusions.

Last reviewed: 2026-07-26.

## 1. Concrete goal

J explores one question:

> What is the smallest truthful agent runtime that remains easy to customize?

J is the umbrella repository. J-agent is its independently embeddable runtime
kernel. J-tui and J-mem are first-party consumers used to prove that the public
seams are sufficient for real customization.

Current verified consumers of J-agent are its reference CLI, private JSONL
runtime, and J-tui. DeepSeek and Ollama independently exercise the model seam.
J-tui consumes the public event and cancellation seams without a runtime API
change. J-mem is an explicit project boundary but does not yet count as a
validating implementation.

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

Primary sources:

- [Pi agent core](https://github.com/badlogic/pi-mono/tree/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/agent)
- [Pi Bash Tool](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/agent/src/harness/tools/bash.ts)
- [Pi shell execution](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/agent/src/harness/env/nodejs.ts)
- [Pi extension system](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/coding-agent/docs/extensions.md)
- [Pi SDK and ResourceLoader](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/coding-agent/docs/sdk.md)
- [Pi package format](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/coding-agent/docs/packages.md)
- [Pi JSON event stream mode](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/coding-agent/docs/json.md)
- [Pi subagent example](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/coding-agent/examples/extensions/subagent/index.ts)

The conclusion is not that J should copy Pi's entire mutable state or
ExtensionAPI. Pi is a mature coding product; J-agent is intentionally only a
runtime. J should copy the separation of concerns, then open fewer seams until
real J consumers prove more are necessary.

## 4. Repository architecture

```text
J/
├── J-agent/   model/tool loop and provider-neutral transcript
├── J-tui/     terminal presentation and interaction
├── J-mem/     local persistence and memory tools
└── docs/      repository-level design decisions
```

Dependency rules:

- J-agent must not import J-tui, J-mem, or future J product modules.
- J-tui and J-mem may depend on J-agent's experimental public Go API.
- J-tui and J-mem must not own each other's policy.
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
| `Model` | Provider adapters, routing, research Harness wrappers | Experimental |
| `Tool` | External capabilities, memory tools, MCP adapters, delegation | Experimental |
| `EventHandler` | TUI, gateway, logs, metrics | Experimental |
| `History` / `WithHistory` | External transcript persistence and restoration | Experimental |

`WithHistory` is intentionally construction-only. The restored transcript is
authoritative, including any system message. J-agent validates message shape,
tool identity, tool-result correlation, and completed tool batches before
accepting it.

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
Its command may explicitly compose an existing Ollama or DeepSeek adapter; it
does not own dynamic model routing or provider discovery.

DeepSeek context caching requires no new runtime mechanism: the service enables
it automatically, and J-agent's ordered transcript makes each new turn an
extension of the prior request prefix. Provider-reported cached input tokens
remain ordinary `Usage`; J-tui may derive and display cache misses from total
input minus cached input. Cache policy and storage remain provider-owned.

It owns rendering, key bindings, terminal state, input editing, and UI-specific
metadata. J-agent must not gain labels, colors, widgets, or TUI component types
to serve J-tui.

The first implementation uses existing start/delta/completed events. Its
private reducer maps them to `thinking`, `responding`, `tool`, `completed`,
`failed`, and `canceled` display states. Reasoning content is retained by the
runtime but only its presence is displayed. J-tui privately owns Markdown
rendering, tool-card expansion, run-state colors, and whether a manually
scrolled viewport follows new output. `--mode json` exposes an experimental
JSON projection of the same event input for event-order diagnostics.

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

### J-mem

J-mem contains two deliberately separate capabilities:

1. Short-term conversation persistence stores J-agent transcripts in local
   SQLite and restores them through `WithHistory`.
2. Long-term memory uses local JSONL and exposes retrieval, storage,
   modification, and forgetting as ordinary J-agent tools.

J-mem owns schema migration, indexing, retention, conflict handling, and memory
policy. J-agent does not define a universal `Memory` or `Storage` interface.

Ambient retrieval or context injection should wait until the tool-based design
proves insufficient. If required, a model wrapper should be tried before
adding a runtime Hook.

## 8. Planned external modules

These names describe product modules, not promised J-agent interfaces:

| Module | Initial composition strategy |
| --- | --- |
| J-agent-swarm | Child J-agent instances exposed to the parent as delegation tools |
| J-mcp | Map discovered MCP tools to `agent.Tool`; own connection/auth lifecycle |
| J-skills | Parse skill resources into prompt/tool recipes outside the runtime |
| J-plugins | Own discovery, trust, installation, and composition |
| J-gateway | Map platform chat identity to external Agent/session ownership |

Go's runtime plugin ABI should not be assumed to provide Pi-style TypeScript
extension loading. J-plugins must first validate whether compile-time Go
composition, external processes, or resource-only packages solve the real use
case.

## 9. Deliberately not generalized

Do not add these to J-agent without a failing real integration:

- `Memory`, `Session`, `SubAgent`, `Skill`, `Plugin`, `MCP`, or `Transport`
  framework interfaces;
- universal `Before`/`After` Hooks;
- arbitrary `map[string]any` metadata;
- runtime mutation of model, tools, or transcript;
- UI rendering details on tools or messages;
- parallel-tool, retry, compaction, or model-fleet policy;
- a package manager or arbitrary-code loader.

Potential future mechanisms must be validated in this order:

1. Build the narrow consumer outside J-agent.
2. Identify the exact blocked operation.
3. Try composition through `Model`, `Tool`, events, or transcript first.
4. Add the smallest experimental runtime primitive only if wrapping is
   insufficient.
5. Stabilize only after another independent consumer validates the same
   semantics.

## 10. Decisions retained

- J is the Git and GitHub repository root.
- J uses Go 1.26 across its modules and workspace. CI follows the latest
  `1.26.x` patch, repository development selects at least Go 1.26.5, and J does
  not maintain an older-version matrix without a real consumer.
- J-agent remains a separate Go module under `J/J-agent`.
- DeepSeek and Ollama are real adapters; demo models are not product features.
- J-Space research informs model/Harness choices but is not a runtime
  integration or README identity.
- Reasoning content is preserved when provider continuation requires it.
- The JSONL process is a reference transport, not universal framework
  semantics.
- Tool authorization belongs to embedders or tool wrappers, not prompt-only
  policy.
- Reference commands compose the first-party Bash Tool; the container, not the
  agent loop, owns execution isolation.
- Public APIs remain experimental before independent production consumers.

## 11. Minimal development order

1. Keep J-agent passing its protocol and race tests.
2. Keep the text-only J-tui and JSON event trace passing against current events.
3. Build J-mem SQLite transcript round-trip using `History` and `WithHistory`.
4. Add the four JSONL-backed long-term memory tools.
5. Re-audit actual integration friction before changing J-agent.
6. Prototype J-mcp and subagent-as-tool.
7. Design skill/plugin distribution only after at least two resource types
   need common discovery and trust semantics.
