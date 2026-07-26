# J

J is a small, composable agent system for exploring how far a minimal runtime
can be extended without turning the runtime into a product framework.

The repository currently contains:

| Project | Responsibility | Status |
| --- | --- | --- |
| [`J-agent`](J-agent/) | Minimal Go model/tool runtime | Implemented, experimental |
| [`J-tui`](J-tui/) | Minimal terminal interface built on J-agent events | Boundary defined |
| [`J-mem`](J-mem/) | Local short- and long-term memory for J-agent | Boundary defined |

J-agent remains independently embeddable. J-tui and J-mem are first-party
examples of customization, not privileged runtime layers.

## Design

The repository's architecture, Pi research conclusions, public-boundary
decisions, module ownership, and deliberately deferred abstractions are
maintained in one place:

- [J design and engineering decisions](docs/design.md)

Component-specific protocol details remain next to the component that owns
them, for example [`J-agent/docs`](J-agent/docs/).

## Build

Run all currently implemented checks from the repository root:

```bash
make check
```

Build the reference agent binary:

```bash
make build
```

## Principles

- **Openness over Possession:** open small, validated mechanisms that other
  projects can compose.
- **Restraint over Complexity:** keep product policy, storage, UI, orchestration,
  and distribution outside the runtime kernel.

The public contracts remain experimental until real consumers validate their
semantics.
