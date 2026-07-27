# J Extension Composition Protocol 0.1

Status: experimental design; the configuration shown here is not implemented
yet.

## Decision summary

J extensions are construction-time product composition, not mutable J-agent
plugins. A product host such as J-tui may load a configured module, obtain
ordinary `agent.Tool` values from it, and pass those tools to `agent.New`.
J-agent remains unaware of extensions and MCP.

Version 0.1 is scoped to one capability: adding tools through the planned
J-mcp module. It does not define a universal Extension Go interface or another
wire protocol. J-mcp speaks MCP directly and privately maps the discovered
tools to J-agent's existing `Tool` seam.

## Concrete requirement and consumers

The current requirement is:

- a J-tui user can explicitly enable configured MCP servers;
- their tools are available to the same J-agent used by the TUI;
- omitting the MCP configuration leaves the current binary behavior unchanged;
- cancellation, errors, tool identity, and JSON schemas remain truthful;
- no MCP lifecycle enters J-agent.

The first intended host is J-tui. The first planned extension module is J-mcp.
Neither counts as a validated implementation until the integration exists. The
reference `j-agent` process and hypothetical third-party extensions are not
current consumers and do not justify a shared host package yet.

## Evidence

Pi places its broad extension host in the coding product rather than its agent
core. It supports tools, commands, UI, sessions, provider mutation, and many
interception events. That breadth is valid for Pi's mature product, but it is
not evidence that J needs the same surface.

MCP already defines the external lifecycle J-mcp needs:

- version and capability negotiation;
- stdio and Streamable HTTP transports;
- `tools/list` and `tools/call`;
- protocol errors versus tool execution errors;
- cancellation, timeouts, and shutdown.

The research baseline on 2026-07-27 is MCP protocol version `2025-11-25`.
Its task-augmented tool calls are still experimental and are outside J-mcp
version 0.1.

Go runtime plugins are not the default mechanism. The Go documentation records
platform limits, weak race-detector support, exact toolchain and dependency
matching requirements, crash risk, and recommends normal interprocess
communication for many use cases.

Primary sources:

- [Pi extensions](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/coding-agent/docs/extensions.md)
- [Pi packages](https://github.com/badlogic/pi-mono/blob/5bc1c2c0a6f07e00e8c240304182f213ab8d311f/packages/coding-agent/docs/packages.md)
- [MCP lifecycle](https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle)
- [MCP tools](https://modelcontextprotocol.io/specification/2025-11-25/server/tools)
- [MCP transports](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)
- [MCP versioning](https://modelcontextprotocol.io/docs/learn/versioning)
- [Go plugin warnings](https://pkg.go.dev/plugin#hdr-Warnings)

## Invariants and policies

### Invariant runtime mechanism

J-agent continues to own only:

- immutable construction-time tool registration;
- tool name, description, and input schema;
- context-governed tool calls;
- tool-call/result correlation;
- ordered tool lifecycle events.

No J-agent API change is required for J-mcp.

### Product policy

The host owns:

- which configured extensions are enabled;
- when they are constructed and closed;
- duplicate-tool rejection;
- credentials and process environment;
- whether startup fails when an extension cannot initialize;
- user-facing status and approval policy.

J-mcp owns:

- MCP protocol versions and transports;
- MCP server connection and shutdown;
- capability negotiation and tool pagination;
- MCP cancellation and timeout handling;
- conversion between MCP tool results and J-agent text tool results.

## Composition lifecycle

The version 0.1 lifecycle is deliberately construction-only:

```text
load typed host configuration
        |
        v
construct configured J-mcp servers
        |
        v
initialize MCP + list tools
        |
        v
validate and freeze agent.Tool values
        |
        v
agent.New(model, agent.WithTools(...))
        |
        v
run J-agent; context cancellation reaches J-mcp tools
        |
        v
close J-mcp when the host exits
```

Extension startup completes before the Agent is constructed. A server cannot
add, remove, or replace tools during a conversation. MCP
`notifications/tools/list_changed` may be observed for diagnostics, but version
0.1 requires restarting the host to refresh the frozen tool set.

This preserves J-agent's transcript and tool invariants and avoids runtime
mutation.

## Planned J-tui configuration

J-tui will remain the owner of `~/.j/config.json`. Presence of the typed `mcp`
extension enables it; absence disables it. There is no redundant `enabled`
flag.

The first implementation should accept only explicit stdio servers:

```json
{
  "defaultProfile": "omlx",
  "profiles": {
    "omlx": {
      "provider": "openai",
      "model": "Qwen3.6-35B-A3B-oQ4e-mtp",
      "baseURL": "http://127.0.0.1:8000/v1"
    }
  },
  "extensions": {
    "mcp": {
      "servers": {
        "filesystem": {
          "command": "mcp-server-filesystem",
          "args": ["/workspace"],
          "env": ["FILESYSTEM_TOKEN"]
        }
      }
    }
  }
}
```

Rules:

- `command` is required; `args` and `env` are optional.
- `env` contains environment-variable names, never secret values.
- the host supplies a documented minimal process environment and forwards only
  the additionally named variables.
- unknown extension kinds and fields are rejected.
- a configured server that cannot initialize fails startup explicitly.
- no project-local auto-discovery or implicit command execution occurs.
- Streamable HTTP waits for a real integration that needs it.

The exact schema becomes executable only with the J-mcp implementation. Until
then, current J-tui correctly rejects the unimplemented `extensions` field.

## Tool projection

J-mcp will project each accepted MCP tool to one `agent.Tool`:

| MCP | J-agent |
| --- | --- |
| tool name | `ToolSpec.Name` |
| description | `ToolSpec.Description` |
| input schema | `ToolSpec.InputSchema` |
| `tools/call` arguments | `Tool.Call` arguments |
| text result content | `Tool.Call` output |
| `isError: true` | model-visible Tool error |
| request cancellation | `context.Context` cancellation |

Version 0.1 supports text result blocks. Image, audio, embedded-resource, and
structured-only results return an explicit unsupported-result error rather
than being silently dropped or represented with an untyped container.

Configured server IDs and exposed tool names must already satisfy the model
providers' common tool-name constraints. J-mcp rejects invalid or duplicate
names instead of applying lossy sanitization. Namespacing and exact limits must
be fixed by executable provider tests during implementation.

## Failure and cancellation

- MCP protocol failures and MCP tool execution failures remain distinct inside
  J-mcp.
- Both become truthful model-visible Tool failures through the existing
  `(string, error)` result.
- J-agent cancellation reaches `Tool.Call` through its context.
- J-mcp must send MCP cancellation when supported and own the bounded process
  shutdown fallback.
- The host does not retry, reconnect, or silently remove a failed extension in
  version 0.1.

Retries and reconnects are policies that require operational evidence.

## Security boundary

An explicitly configured stdio MCP server is executable code with the
permissions of J-tui. It is not a sandbox.

Version 0.1 therefore:

- loads only user-configured servers;
- performs no package installation or directory auto-discovery;
- supplies a minimal operational process environment and forwards additional
  secret variables only when named;
- keeps protocol stdout separate from diagnostic stderr;
- does not place credentials in J-agent messages or tool schemas;
- leaves approval and authorization to the host or Tool wrappers.

J-agent importing package `agent` still grants no MCP or process capability.

## Openness decision

- **Audience:** repository consumers and future J-mcp contributors.
- **Shared contract:** the construction lifecycle and projection to
  `agent.Tool`.
- **Private implementation:** J-tui configuration loading and initial extension
  host; J-mcp connection machinery.
- **Stability:** experimental until J-mcp and at least one additional
  tool-producing module validate the same lifecycle.
- **Composition seam:** the already experimental `agent.Tool`, not a new core
  Extension interface.

## Deliberately not generalized

Version 0.1 does not define:

- a J-agent `Extension`, `Plugin`, or `MCP` interface;
- Hooks or message/context interception;
- commands, keyboard shortcuts, UI components, themes, or renderers;
- provider registration or model mutation;
- memory, sessions, compaction, or persistence;
- MCP task-augmented tool calls, prompts, resources, sampling, or elicitation;
- hot reload or runtime tool mutation;
- package discovery, installation, catalogs, or project trust;
- a J-specific JSON-RPC or JSONL extension transport;
- arbitrary extension configuration payloads.

These wait for real consumers that cannot be composed through Model, Tool,
events, or transcript.

## Rejected alternatives

### Copy Pi's full ExtensionAPI

Rejected because J has no product domains for most of Pi's hooks, commands, UI,
session, compaction, and provider mutation.

### Add Extension to J-agent

Rejected because MCP is already expressible as Tools and its connection
lifecycle belongs outside the runtime.

### Use Go runtime plugins

Rejected because distribution, race validation, portability, and exact build
compatibility are worse than normal Go composition for the current consumers.

### Invent a J extension subprocess protocol

Rejected for version 0.1 because J-mcp already has MCP as its external process
protocol. Wrapping MCP in another tool-list/tool-call protocol would duplicate
lifecycle, errors, cancellation, and schemas without a second non-MCP process
consumer.

### Import J-mcp directly into J-agent

Rejected because it reverses the dependency boundary and makes optional product
capability part of the kernel.

## Validation required

The J-mcp implementation must prove:

1. one configured stdio MCP server initializes and exposes frozen J-agent
   Tools;
2. J-tui can call one MCP tool through a real model;
3. duplicate and invalid tool names fail before Agent construction;
4. cancellation reaches the MCP request and child process;
5. protocol and tool execution errors remain distinguishable in tests;
6. omitted MCP configuration starts no process and exposes no MCP tools;
7. credentials do not enter logs, events, transcripts, or configuration
   values;
8. normal, race, and end-to-end tests pass.

Only after a second meaningfully different tool-producing module needs the same
host lifecycle should J extract a shared package or public Go interface.
