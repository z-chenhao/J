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

Current verified consumers of J-agent are its reference CLI and private JSONL
runtime. DeepSeek and Ollama independently exercise the model seam. J-tui and
J-mem are now explicit project boundaries, but do not yet count as validating
implementations.

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

Primary sources:

- [Pi agent core](https://github.com/earendil-works/pi/tree/main/packages/agent)
- [Pi extension system](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md)
- [Pi SDK and ResourceLoader](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/sdk.md)
- [Pi package format](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/packages.md)
- [Pi subagent example](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/examples/extensions/subagent/index.ts)

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
- bounded model/tool execution and cancellation;
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

## 6. First-party consumers

### J-tui

J-tui is a minimal terminal UI. It calls J-agent, consumes lifecycle events,
reads transcript snapshots, and cancels runs through `context.Context`.

It owns rendering, key bindings, terminal state, input editing, and UI-specific
metadata. J-agent must not gain labels, colors, widgets, or TUI component types
to serve J-tui.

The first implementation should use existing start/delta/completed events.
Tool-progress events should be proposed only if an actual interaction cannot be
represented truthfully with the current event stream.

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

## 7. Planned external modules

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

## 8. Deliberately not generalized

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

## 9. Decisions retained

- J is the Git and GitHub repository root.
- J-agent remains a separate Go module under `J/J-agent`.
- DeepSeek and Ollama are real adapters; demo models are not product features.
- J-Space research informs model/Harness choices but is not a runtime
  integration or README identity.
- Reasoning content is preserved when provider continuation requires it.
- The JSONL process is a reference transport, not universal framework
  semantics.
- Tool authorization belongs to embedders or tool wrappers, not prompt-only
  policy.
- Public APIs remain experimental before independent production consumers.

## 10. Minimal development order

1. Keep J-agent passing its protocol and race tests.
2. Build a text-only J-tui against current events.
3. Build J-mem SQLite transcript round-trip using `History` and `WithHistory`.
4. Add the four JSONL-backed long-term memory tools.
5. Re-audit actual integration friction before changing J-agent.
6. Prototype J-mcp and subagent-as-tool.
7. Design skill/plugin distribution only after at least two resource types
   need common discovery and trust semantics.
