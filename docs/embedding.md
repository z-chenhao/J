# Embed and assemble J-agent

Status: experimental public Go API.

## The composition root belongs to the embedder

J-agent is an assembly kernel, not an extension host. An application imports
the `agent` package, constructs the exact model and tools it wants, and owns the
top-level call to `agent.New`.

```go
runner, err := agent.New(
	model,
	agent.WithSystemPrompt(systemPrompt),
	agent.WithTools(tools...),
)
if err != nil {
	return err
}

result, err := runner.Run(ctx, input, handler)
```

An embedder restoring `agent.History()` uses `agent.WithHistory(history...)`
instead of a new system prompt. The restored transcript is authoritative,
including its system message.

No registry, manifest, J-tui, or J Package is required. A custom host may use
every sibling module, replace each one, or omit all of them.

## Four orthogonal seams

| Seam | Community-owned variation |
| --- | --- |
| `agent.Model` | provider, routing, cache, recording, fallback, model wrapper |
| `agent.Tool` | local capability, MCP bridge, memory, delegation, authorization wrapper |
| `agent.EventHandler` | UI, logs, metrics, gateway projection, observation |
| `History` / `WithHistory` | storage, session identity, retention, restoration |

Modules should expose the narrowest constructor that represents their real
lifecycle. They do not need to implement a common J Extension interface.

```go
func NewTools(config Config) ([]agent.Tool, io.Closer, error)
func WrapModel(inner agent.Model, config Config) agent.Model
func NewHandler(config Config) agent.EventHandler
```

These signatures are examples, not another protocol. A module that owns no
resource should not return a closer; a module that produces one Tool should
return one Tool.

## Source composition and prebuilt products are different

Source-level Go composition offers complete control and requires rebuilding
the host. A prebuilt product can load only capabilities expressed through a
protocol it understands:

- MCP and Agent Skills may be installed through J Packages.
- J-tui completed-run Observers use J-tui's typed process protocol.
- another product may define a different typed process protocol without
  changing J-agent or J-packages.

J Packages are an optional distribution convenience, not an admission
requirement for J-agent extensions.

## Wrapper composition instead of universal Hooks

Cross-cutting behavior remains ordinary Go composition:

```text
base Model
  -> routing Model
  -> recording Model
  -> agent.New

base Tool
  -> authorization Tool
  -> metrics Tool
  -> agent.WithTools

EventHandler
  -> product UI + logs + metrics
```

A wrapper implements the same small public seam and delegates to the wrapped
value. J-agent does not predict `BeforePrompt`, `AfterTool`, provider,
approval, UI, or session Hook catalogs.

## Independent-consumer validation

A community integration should be testable without this repository's
`go.work`:

```bash
GOWORK=off go test ./...
GOWORK=off go vet ./...
GOWORK=off go build ./...
```

Pin released module versions or an explicit commit-derived pseudo-version.
Do not add a `replace` directive to make an unpublished sibling appear
available. This exposes missing public dependencies before release.

The repository's
[`examples/embedding/custom-host`](../examples/embedding/custom-host/)
module is validated this way. It wraps a Model, supplies its own Tool and event
handler, and restores the resulting transcript without importing J-tui or
J-packages.

## Deliberately not provided

J-agent does not provide:

- `Extension`, `Plugin`, or generic lifecycle interfaces;
- package discovery or dependency installation;
- runtime model, Tool, or transcript mutation;
- untyped extension metadata;
- UI, session, memory, approval, or sandbox policy.

A new public seam requires a failing integration through the existing Model,
Tool, Event, and History mechanisms plus a second meaningfully independent
consumer of the same missing semantic axis.
