# J MCP Extension Composition Protocol 0.1

Status: experimental and implemented by J-mcp plus J-tui's direct,
configuration-owned MCP host.

Reusable package installation is a separate layer documented by
[J Package Protocol 0.2](packages.md). J-packages reuses this same MCP Tool
projection rather than changing it.

## Decision summary

J extensions are construction-time product composition, not mutable J-agent
plugins. A product host such as J-tui may load a configured module, obtain
ordinary `agent.Tool` values from it, and pass those tools to `agent.New`.
J-agent remains unaware of extensions and MCP.

Version 0.1 is scoped to one capability: adding tools through J-mcp. It does
not define a universal Extension Go interface or another wire protocol. J-mcp
speaks MCP directly and privately maps discovered tools to J-agent's existing
`Tool` seam.

J-mem, J-skills, and J-subagents now validate the same construction-time Tool
composition while also proving that their lifecycles differ. J-tui keeps them
in separate typed `memory`, `skills`, and `subagents` sections rather than
widening this MCP-specific protocol into a generic loader. They do not justify
an `Extension` interface.

## Concrete requirement and consumers

The current requirement is:

- a J-tui user can explicitly enable configured MCP servers;
- their tools are available to the same J-agent used by the TUI;
- omitting the MCP configuration leaves the current binary behavior unchanged;
- cancellation, errors, tool identity, and JSON schemas remain truthful;
- no MCP lifecycle enters J-agent.

The first configuration host is J-tui. J-mcp is independently validated
through in-memory protocol tests, a real stdio child process, cancellation
tests, and a complete J-agent tool loop. J-tui validates typed configuration,
process environment forwarding, cross-module tool collision handling, and
shutdown. The reference `j-agent` process and hypothetical third-party
extensions are not configuration consumers and do not justify a shared host
package.

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

J-mcp's `DialStdio` is the initial process recipe. Its experimental `Connect`
accepts the official Go SDK's `mcp.Transport`, so applications with another
standard transport can compose it without J defining a duplicate Transport
interface. J-tui uses that seam for both stdio and Streamable HTTP while keeping
transport configuration in the product host.

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

## J-tui configuration

J-tui remains the owner of `~/.j/config.json`. Presence of the typed `mcp`
extension enables it; absence disables it. There is no redundant `enabled`
flag.

The implementation accepts explicit stdio servers and Streamable HTTP
endpoints:

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
          "env": ["FILESYSTEM_TOKEN"],
          "cwd": "servers/filesystem"
        },
        "search": {
          "url": "https://mcp.example.test/mcp/",
          "headers": {
            "Authorization": "Bearer ${SEARCH_MCP_TOKEN}"
          }
        }
      }
    }
  }
}
```

Rules:

- exactly one of `command` or `url` is required.
- stdio accepts optional `args`, `env`, and `cwd`; HTTP does not.
- HTTP accepts optional string `headers`. Header values may combine literal
  text with `${ENV_VAR}` references.
- configured HTTP headers do not follow redirects.
- `env` remains a stdio environment-name allowlist; it is independent from
  HTTP header value resolution.
- omitted `tools` selects every advertised tool; a non-empty array is an exact,
  case-sensitive allowlist.
- an empty allowlist or a name not advertised by that server fails explicitly.
- the host supplies a documented minimal process environment and forwards only
  the additionally named variables.
- relative `cwd` is resolved from the configuration file's directory.
- unknown extension kinds and fields are rejected.
- a configured server that cannot initialize fails startup explicitly.
- no project-local auto-discovery or implicit command execution occurs.
- the Tavily remote MCP integration is the concrete consumer that justifies
  Streamable HTTP and configured headers; HTTP/MCP framing headers, OAuth
  policy, command-based secret lookup, and transport tuning remain absent.

The schema is executable. J-tui sorts server IDs for deterministic startup,
discovers each server's complete tool set through J-mcp, applies its private
construction-time allowlist, freezes the selected Tools, rejects selected-name
collisions across Bash, J-mem, and every MCP server, and closes initialized
connections in reverse order. `j-tui --list-tools` exposes the advertised and
selected names for configuration audit without constructing a model.

## Tool projection

J-mcp projects each accepted MCP tool to one `agent.Tool`:

| MCP | J-agent |
| --- | --- |
| tool name | `ToolSpec.Name` |
| description | `ToolSpec.Description` |
| input schema | `ToolSpec.InputSchema` |
| `tools/call` arguments | `Tool.Call` arguments |
| text result content | `Tool.Call` output |
| `isError: true` | model-visible Tool error |
| request cancellation | `context.Context` cancellation |

Version 0.1 supports text result blocks up to 1 MiB. Image, audio,
embedded-resource, structured-only, and oversized results return an explicit
unsupported-result error rather than being silently dropped, truncated, or
represented with an untyped container.

J-mcp rejects blank or untrimmed names, non-object input schemas, and duplicate
names returned by one server instead of applying lossy sanitization.
Provider-specific tool-name constraints are not generalized into J-mcp; the
product host may validate them for its selected model provider. Namespacing and
duplicate names across servers are likewise host composition concerns.

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
permissions of J-tui. An HTTP MCP server is a remote capability and data
boundary. Neither transport is a sandbox.

Direct J-tui MCP configuration therefore:

- loads only user-configured servers;
- performs no package installation or directory auto-discovery; the explicit
  `j package` workflow is separate;
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
- **Private implementation:** J-tui direct MCP configuration and product
  ordering; J-mcp connection machinery.
- **Stability:** experimental. J-packages and J-tui now validate the same
  connection lifecycle, but independently maintained consumers are still
  required before stabilization.
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
- catalogs, project auto-discovery, or ambient trust;
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

Current validation:

1. one stdio MCP server initializes and exposes frozen J-agent Tools;
2. one projected MCP tool completes a real J-agent tool-call continuation;
3. invalid names and schemas fail before Agent construction;
4. context cancellation reaches an active MCP request;
5. protocol and tool execution errors remain distinguishable;
6. non-text and oversized results fail explicitly;
7. normal, race, vet, and build checks pass.

J-tui integration additionally proves:

1. a configured stdio server's Tool is callable through J-tui's composed
   runner;
2. a configured Streamable HTTP server receives Bearer authentication and its
   Tool is callable through the same composition;
3. duplicate names across built-in, memory, and MCP tools fail before Agent
   construction;
4. omitted MCP configuration starts no process and exposes no MCP tools;
5. only named environment values are forwarded beyond the documented baseline;
6. transcript snapshots restore through `WithHistory`, and accepted user turns
   plus complete model/tool turns persist as restorable checkpoints;
7. configured subagents use the same store through a parent-scoped child
   identity without adding session or storage concepts to J-agent.

A provider-backed live MCP call remains an operational smoke test rather than
a deterministic unit test.

J-packages now extracts only the proven shared package lifecycle: explicit
installation plus MCP Tools and Agent Skills roots. This does not justify
widening J-mcp or J-agent with a common Extension interface.
