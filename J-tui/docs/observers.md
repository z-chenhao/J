# J-tui Completed-run Observer Protocol 0.1

Status: experimental.

## Purpose

A prebuilt J-tui needs one narrow way to give an explicitly configured process
read-only evidence about a completed root Agent run. J-space is the first real
consumer. This protocol is owned by the product host; it is not a J-agent
Plugin, Hook, package contribution, or transport.

Observers cannot modify messages, Tools, model responses, lifecycle events,
history, UI, or the Agent result.

## Selection and permissions

An Observer is enabled only by a named entry in
`extensions.observers`. Installing an executable does not activate it.

The manifest requests a non-empty subset of:

- `agent.events`: safe lifecycle metadata, timings, tool names, output byte
  counts, stop reasons, and normalized usage;
- `model.frames`: complete root-model requests and responses, including
  prompts, reasoning blocks, Tool schemas, Tool results, and model output.

`model.frames` is sensitive. J-tui omits every ungranted projection even though
the configured executable is already trusted to run as the user.

Each Observer entry is a typed process recipe:

```json
{
  "extensions": {
    "observers": {
      "trace": {
        "command": "trace-observer",
        "args": ["--one-run"],
        "env": ["TRACE_TOKEN"],
        "cwd": ".",
        "permissions": ["agent.events"]
      }
    }
  }
}
```

Relative commands and `cwd` values resolve from the directory containing the
configuration file. No shell expansion occurs. `PATH`, `HOME`, and available
temporary-directory and locale values form the process baseline; every
additional environment value must be named explicitly.

## Process contract

J-tui invokes each selected Observer once after a root run returns. The process
receives exactly one `j.observer.run.v0.1` JSON value on stdin and no command
shell is involved. Input is bounded to 8 MiB. Runtime is bounded to 15 seconds.
Stdout is ignored and stderr is bounded for diagnostics.

```json
{
  "schemaVersion": "j.observer.run.v0.1",
  "id": "20260730T120000Z-a1b2c3d4e5f6",
  "label": "J-tui · omlx",
  "product": "J-tui",
  "commit": "0123456789ab",
  "profile": "omlx",
  "model": "Qwen3.6-35B-A3B-oQ4e-mtp",
  "succeeded": true,
  "events": [],
  "frames": []
}
```

`events` and `frames` are omitted unless their exact permission was granted.
Frames reuse J-agent's current public `ModelRequest` and `ModelResponse` JSON
shape because the Observer is explicitly coupled to that experimental public
seam. The completed-run envelope remains separately versioned so J-tui can
reject incompatible process contracts without changing J-agent.

## Failure semantics

- Invalid name, command, environment, permission, or working directory fails
  product construction before the Agent is created.
- Once an Agent run starts, Observer timeout, crash, rejection, or transport
  failure is diagnostic only.
- The authoritative `agent.RunResult` and run error are returned unchanged.
- Persistence, retry, upload, and remote credentials belong to the Observer.
- There is no hot reload. Package or configuration changes apply to the next
  J-tui process.

## Deliberately absent

Version 0.1 has no streaming process, response channel, mutation result,
arbitrary event subscription, prompt interception, Tool interception, UI
extension, shared mutable state, dynamic library, J Package contribution, or
J-agent dependency on product configuration. A second independent Observer
must validate the same completed-run semantics before stability is considered.
