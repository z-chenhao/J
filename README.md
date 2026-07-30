# J

J is a small, composable agent system for exploring how far a minimal runtime
can be extended without turning the runtime into a product framework.

The repository currently contains:

| Project | Responsibility | Status |
| --- | --- | --- |
| [`J-agent`](J-agent/) | Minimal Go model/tool runtime | Implemented, experimental |
| [`J-tui`](J-tui/) | Minimal terminal interface and JSON event trace | Implemented, experimental |
| [`J-mcp`](J-mcp/) | MCP server tools projected into J-agent | Implemented, experimental |
| [`J-mem`](J-mem/) | SQLite transcripts and JSONL long-term memory | Implemented, experimental |
| [`J-packages`](J-packages/) | Explicit MCP and Agent Skills package installation | Implemented, experimental |
| [`J-skills`](J-skills/) | Standard Agent Skills discovery and progressive loading | Implemented, experimental |
| [`J-subagents`](J-subagents/) | Isolated, optionally resumable foreground subagents exposed as one Tool | Implemented, experimental |
| [`J-space`](J-space/) | Optional Jacobian-lens Observer implementation and workbench | Implemented, experimental |

J-agent remains independently embeddable. Every sibling is a first-party
example of customization, not a privileged runtime layer. Each can be replaced
or omitted by an embedding application.

## Install J-tui

With Go 1.26 or newer:

```bash
go install github.com/z-chenhao/J/J-tui/cmd/j-tui@latest
j-tui --init-config
j-tui
```

Versioned releases also publish checked archives containing `j-tui` and the
`j package` manager for macOS and Linux:

```bash
curl -fsSL https://raw.githubusercontent.com/z-chenhao/J/main/J-tui/install.sh | sh

j-tui --init-config
j-tui
```

The installer places both binaries in `~/.local/bin` by default. Set
`J_TUI_INSTALL_DIR` to choose another directory. It targets the current J-tui
component Release, so another module's Release cannot redirect it to unrelated
assets.

`--init-config` creates `~/.j/config.json` with a best-effort public oMLX
profile selected by default, plus DeepSeek and Ollama alternatives. The default
needs no credential; credentialed profiles use explicit `apiKey` values or
environment references. Use `j-tui --profile <name>` to switch profiles. The
public endpoint is rate limited and sends prompts to a project-operated host,
so do not send secrets or private data. See the [J-tui guide](J-tui/) for the
complete configuration and security boundary.

The same typed file can explicitly configure stdio or HTTP MCP servers,
J-mem state, Agent Skills roots, isolated foreground subagents, and product
Observers. Users can also explicitly install reusable MCP and Agent Skills
packages:

```bash
export J_MEMORY_PATH="$HOME/.j/hermes-memory.jsonl"
j package add ./examples/packages/j-hermes-memory
j package doctor
j-tui --list-tools
j-tui --list-skills
```

`j-tui --session <id>` restores and persists a configured transcript, while
long-term memory, skills, MCP, and subagents remain ordinary model-visible
Tools. Omitting a section keeps that module disabled.

J-tui enables the first-party Bash Tool. A direct host installation therefore
runs model-requested commands with the permissions of the `j-tui` process. Use
the repository container when the workspace requires isolation.

## Design

The repository's architecture, Pi research conclusions, public-boundary
decisions, module ownership, and deliberately deferred abstractions are
maintained in one place:

- [J design and engineering decisions](docs/design.md)
- [Embed and assemble J-agent](docs/embedding.md)
- [J Package Protocol 0.1](docs/packages.md)

Component-specific protocol details remain next to the component that owns
them, for example [`J-agent/docs`](J-agent/docs/).

## Build

Run checks for the complete workspace from the repository root:

```bash
make check
```

Build the reference binaries and validate the library modules:

```bash
make build
```

## Run in a container

The reference commands include the first-party `bash` tool. The supported
isolation boundary is the container, not J-agent's model/tool loop:

```bash
docker build -t j:dev .

docker run --rm -i \
  -e DEEPSEEK_API_KEY \
  -v "$PWD:/workspace" \
  j:dev \
  --provider openai \
  --model deepseek-v4-flash \
  --base-url https://api.deepseek.com \
  --api-key '${DEEPSEEK_API_KEY}' \
  --reasoning-field reasoning_content \
  "Use bash to print the working directory."
```

Run the same composition through the TUI by overriding the image entrypoint:

```bash
docker run --rm -it \
  -e DEEPSEEK_API_KEY \
  -v "$PWD:/workspace" \
  --entrypoint j-tui \
  j:dev \
  --provider openai \
  --model deepseek-v4-flash \
  --base-url https://api.deepseek.com \
  --api-key '${DEEPSEEK_API_KEY}' \
  --reasoning-field reasoning_content
```

The mounted `/workspace`, environment variables, network, resource limits, and
container privileges are operator policy. Running either reference command
directly on a host gives Bash the same host permissions as that process.

## Principles

- **Openness over Possession:** open small, validated mechanisms that other
  projects can compose.
- **Restraint over Complexity:** keep product policy, storage, UI, orchestration,
  and distribution outside the runtime kernel.

The public contracts remain experimental until real consumers validate their
semantics.
