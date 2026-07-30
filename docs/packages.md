# J Package Protocol 0.2

Status: experimental.

## Requirement and current consumers

Users need one explicit, user-owned way to install reusable capabilities into
J-tui and into other products built on J-agent. Version 0.1 proved stdio MCP
tools and standard Agent Skills. Version 0.2 preserves that schema and adds one
read-only Observer contribution after J-space proved that a prebuilt host needs
to expose safe Agent events and complete Model frames without adding
product-specific code.

The protocol does not attempt to model every Pi extension feature. The
independent [`j-hermes-memory`](../examples/packages/j-hermes-memory/) example
validates a real package with three memory Tools, one Skill, local persistence,
and no access to J internals. The top-level [`J-space`](../J-space/) module is
the first Observer package.

The reference case is
[`pi-hermes-memory`](https://github.com/khromov/pi-hermes-memory). That package
also uses Pi-specific Hooks, commands, session events, prompt changes, and UI.
J deliberately reproduces only the independently useful memory-Tool and Skill
shape. The experiment therefore tests interoperability through standards
without treating one Pi product API as a universal J contract.

## Architecture

```text
explicit local path or pinned Git source
                 |
                 v
       j package add/update
                 |
                 v
  ~/.j/packages.json (mode 0600)
                 |
                 v
                    product construction
                 /          |             \
                v           v              v
         stdio MCP    Agent Skills   selected Observer
            tools        roots          process
                \           /              |
                 v         v               v
              J-agent Tool seam    product-owned projection
```

J-agent is not involved in discovery, installation, trust, Observer execution,
or Skill loading. `J-packages` depends on J-agent and J-mcp; J-agent does not
depend on J-packages. J-tui owns its Observer process protocol and permission
projection.

## Manifest

Every package has `j-package.json` at its root:

```json
{
  "schemaVersion": "j.package.v0.2",
  "id": "dev.example.agent-tools",
  "version": "1.0.0",
  "description": "Local memory tools and guidance.",
  "contributes": {
    "mcp": [
      {
        "id": "memory",
        "command": "python3",
        "args": ["server.py"],
        "env": ["J_MEMORY_PATH"],
        "cwd": ".",
        "tools": ["memory_store", "memory_search"]
      }
    ],
    "skills": ["skills"],
    "observers": [
      {
        "id": "trace",
        "command": "trace-observer",
        "env": ["TRACE_PATH"],
        "permissions": ["agent.events"]
      }
    ]
  }
}
```

Rules:

- JSON is strict: unknown fields and multiple JSON values are rejected.
- `j.package.v0.1` remains accepted unchanged. Observer contributions require
  `schemaVersion: "j.package.v0.2"`.
- `id` is a lowercase dot/dash identifier; `version` is semantic version
  `x.y.z` with an optional pre-release or build suffix.
- At least one MCP, Skills, or Observer contribution is required.
- MCP is stdio only. `command` is a bare executable resolved from the forwarded
  `PATH` or a package-relative executable. Arguments are passed directly;
  neither shell expansion nor command substitution occurs.
- `cwd`, commands containing a path separator, and Skills roots must resolve
  inside the package root, including after symlink resolution.
- `env` names the only additional parent environment values passed to that
  process. `PATH`, `HOME`, and available temp/locale values form the operational
  baseline. Missing requested values fail startup.
- Omitted `tools` selects every advertised Tool. A non-empty array is an exact
  allowlist. An empty array and unknown Tool names fail.
- Skills roots contain ordinary Agent Skills directories and `SKILL.md` files.
  J-packages validates the root boundary; the product's J-skills integration
  validates the Skills themselves.
- Observers use the same confined command, cwd, argument, and environment
  rules as stdio MCP processes. Their IDs are selected as
  `<package-id>/<observer-id>`.
- Observer permissions are a non-empty exact subset of `agent.events` and
  `model.frames`. The manifest requests an upper bound; the host sends only
  fields covered by those permissions.
- Installing an Observer package does not activate it. A product host must
  explicitly select it before any run data is sent.

## Installation and update

Local paths and Git are deliberately different:

```sh
j package add ./my-package
j package add local:./my-package
j package add git:https://github.com/example/my-package.git@v1.0.0
j package add git:github.com/example/my-package@4b825dc
```

- Installing is the explicit trust decision. Version 0.1 does not search the
  current project, parent directories, package catalogs, or registries.
- A local entry records the canonical local directory and manifest digest.
  Editing its manifest requires `j package update <id>` before a host will load
  it. Package-owned code and resources remain developer-controlled.
- A Git source must include `@ref`. It is fetched into a clean repository,
  resolved to a commit, and moved to
  `~/.j/packages/<package-id>/<commit>/`. No project Git configuration or
  hooks are reused.
- Git updates fetch the same explicit source/ref again and create a new
  commit-addressed checkout. Old checkouts are retained.
- The registry records ID, version, source, canonical root, resolved commit,
  and SHA-256 of the manifest. Hosts reject identity or digest drift.
- `j package remove` changes only the registry. It does not recursively delete
  cached package data.

Useful audits:

```sh
j package list
j package doctor
j-tui --list-tools
j-tui --list-skills
j-tui --check-skills
```

## J-tui configuration and upgrade

The default `~/.j/packages.json` is read at construction time. A missing
registry is equivalent to no installed packages.

Use an alternate registry or disable all installed packages for one run:

```sh
j-tui --packages-registry /path/to/packages.json
j-tui --no-packages
```

The equivalent environment variable is `J_TUI_PACKAGES_REGISTRY`. The package
manager uses `J_PACKAGES_REGISTRY`; this separation lets one product inspect an
alternate registry without silently changing the manager's persistent target.

Package-required environment values must exist before the product starts:

```sh
export J_MEMORY_PATH="$HOME/.j/hermes-memory.jsonl"
j-tui
```

Existing typed `extensions.mcp`, `memory`, `skills`, and `subagents`
configuration still works. Installed package contributions are composed with
those capabilities, and duplicate Tool or Skill names fail explicitly.

Observers require an exact, typed selection:

```json
{
  "extensions": {
    "observers": {
      "include": ["dev.example.agent-tools/trace"]
    }
  }
}
```

The same `extensions` object may contain both `mcp` and `observers`. An empty,
duplicate, malformed, or uninstalled Observer selection fails startup before an
Agent is constructed.

J-tui's former top-level `jspace` configuration and `--jspace-*` /
`J_TUI_JSPACE_*` overrides are removed. J-Space users install its Observer
package, select `dev.usej.jspace/capture`, and provide the J-Space-owned
`JSPACE_CAPTURE_URL` and `JSPACE_CAPTURE_TOKEN` environment values. No
J-Space-specific transport or credential remains in J-tui.

## Security and lifecycle

A package containing MCP or an Observer is executable code with the permissions
of the product host. A package is not a sandbox.

The package lifecycle remains construction-time:

1. validate the private registry and every pinned manifest;
2. start package MCP processes with a bounded environment;
3. initialize MCP, list Tools, apply exact selection, and freeze the result;
4. give ordinary Tools, Skills roots, and unresolved Observer choices to the
   product host;
5. resolve only the Observers explicitly selected by product configuration;
6. close MCP processes in reverse order when the product exits.

J-tui invokes each selected Observer once per completed run with one
`j.observer.run.v0.1` JSON value on stdin. The input is bounded to 8 MiB and the
process to 15 seconds. Observer stdout is ignored and stderr is bounded.
Observer failure is reported diagnostically but never changes the Agent result.
The complete product-owned contract is documented in
[J-tui Completed-run Observer Protocol 0.1](../J-tui/docs/observers.md).

There is no hot reload or runtime mutation. Update or installation affects the
next product process.

## Openness and stability decision

- **Open scope:** manifest, CLI behavior, registry schema, and the small Go host
  API are repository-public and experimental.
- **Stable standards reused:** MCP stdio and Agent Skills.
- **Experimental mechanism:** `j.observer.run.v0.1` is a narrow J-tui process
  protocol validated first by J-space; it is not a J-agent protocol.
- **Private policy:** J-tui ordering, collision presentation, model prompt,
  approval, and UI remain product-owned.
- **Stability:** both package schemas and the Observer protocol remain
  experimental. A future stable Observer contract must be earned by a second
  independent observer.

## Deliberately not exposed

Version 0.2 does not provide:

- a J-agent `Plugin`, `Extension`, `Memory`, or package interface;
- mutable lifecycle Hooks, message interception, hidden prompt injection,
  transcript mutation, model replacement, provider registration, or runtime
  mutation;
- TUI commands, key bindings, panels, renderers, or themes;
- an arbitrary `map[string]any` configuration bag;
- dynamic libraries, Go `.so` plugins, in-process third-party code, npm/Python
  dependency installation, package scripts, catalogs, or auto-discovery;
- MCP HTTP servers, prompts, resources, sampling, elicitation, or experimental
  task-augmented calls as package contribution types;
- automatic deletion, upgrades, or background network access.

These are deferred until a real package and a real second product cannot
compose through existing Model, Tool, Event, History, MCP, and Skills seams.

## Rejected alternatives

- **Full Pi ExtensionAPI:** broad Hooks and product UI concepts would expose
  J-tui internals and predict domains J does not yet have.
- **J-agent Plugin interface:** reverses the dependency boundary and duplicates
  existing composition seams.
- **Go runtime plugins:** portability, toolchain matching, race validation, and
  process crash isolation are worse than explicit subprocess standards.
- **A new J tool RPC:** duplicates MCP lifecycle, schemas, errors,
  cancellation, and ecosystem support.
- **Project auto-discovery:** makes executable capabilities ambient and obscures
  the trust decision.
