# J Package Protocol 0.1

Status: experimental.

## Requirement and current consumers

Users need one explicit, user-owned way to install reusable capabilities into
J-tui and into other products built on J-agent. The current proven capability
types are stdio MCP tools and standard Agent Skills. J-tui is the first product
host; the J-packages tests also compose the same installed Tool through a
custom J-agent host.

The protocol does not attempt to model every Pi extension feature. The
independent [`j-hermes-memory`](../examples/packages/j-hermes-memory/) example
validates a real package with three memory Tools, one Skill, local persistence,
and no access to J internals.

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
          /             \
         v               v
  stdio MCP tools   Agent Skills roots
         \               /
          v             v
        existing J-agent Tool seam
```

J-agent is not involved in discovery, installation, trust, process startup, or
Skill loading. `J-packages` depends on J-agent and J-mcp; J-agent does not
depend on J-packages.

## Manifest

Every package has `j-package.json` at its root:

```json
{
  "schemaVersion": "j.package.v0.1",
  "id": "dev.example.memory",
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
    "skills": ["skills"]
  }
}
```

Rules:

- JSON is strict: unknown fields and multiple JSON values are rejected.
- `schemaVersion` is exactly `j.package.v0.1`.
- `id` is a lowercase dot/dash identifier; `version` is semantic version
  `x.y.z` with an optional pre-release or build suffix.
- At least one MCP or Skills contribution is required.
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

No migration of `~/.j/config.json` is required. After upgrading to J-tui 0.2,
the default `~/.j/packages.json` is read at construction time. A missing
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

## Security and lifecycle

A package containing MCP is executable code with the permissions of the
product host. A package is not a sandbox.

The 0.1 lifecycle is construction-only:

1. validate the private registry and every pinned manifest;
2. start package MCP processes with a bounded environment;
3. initialize MCP, list Tools, apply exact selection, and freeze the result;
4. give ordinary Tools and Skills roots to the product host;
5. close MCP processes in reverse order when the product exits.

There is no hot reload or runtime mutation. Update or installation affects the
next product process.

## Openness and stability decision

- **Open scope:** manifest, CLI behavior, registry schema, and the small Go host
  API are repository-public and experimental.
- **Stable standards reused:** MCP stdio and Agent Skills. J does not wrap them
  in another subprocess protocol.
- **Private policy:** J-tui ordering, collision presentation, model prompt,
  approval, and UI remain product-owned.
- **Stability:** `j.package.v0.1` is experimental. A future stable version must
  be earned by independently maintained packages and hosts.

## Deliberately not exposed

Version 0.1 does not provide:

- a J-agent `Plugin`, `Extension`, `Memory`, or package interface;
- lifecycle Hooks, message interception, hidden prompt injection, transcript
  access, model replacement, provider registration, or runtime mutation;
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
