# Contributing to J

J favors small, complete changes over speculative framework growth.

## Before proposing a change

State:

- the concrete requirement
- the current consumer
- the failing behavior or integration cost
- why the change belongs in the core
- what remains deliberately unsupported

An imagined future consumer is not enough to stabilize a new interface or
Hook.

## Development

Requirements:

- Go 1.26 or newer; repository development selects the patched Go 1.26.5
  toolchain or a newer compatible release

Run all implemented checks from the repository root:

```bash
make check
```

Changes to public J-agent behavior or `j-agent` require executable contract tests.
Use `gofmt`; do not commit generated binaries, provider credentials, model
weights, fitted lenses, or experiment datasets.

## Pull requests

Keep pull requests narrow. Describe:

- what changed and why
- the affected contract
- compatibility implications
- validation performed
- rejected broader alternatives

Public APIs remain experimental until a release explicitly marks them stable.
