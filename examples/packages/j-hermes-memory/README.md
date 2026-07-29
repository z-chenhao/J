# J Hermes Memory example

This is an independent J Package inspired by the useful public shape of
`pi-hermes-memory`: durable memory tools plus an Agent Skill. It does not import
J internals and does not receive lifecycle hooks, prompts, transcripts, or
model reasoning.

The store is append-only JSONL. Forgetting writes a tombstone. Common secret
formats are rejected before persistence. It requires Python 3.10 or newer and
uses only the standard library; J Package does not install language runtimes or
package dependencies.

```sh
export J_MEMORY_PATH="$HOME/.j/hermes-memory.jsonl"
j package check ./examples/packages/j-hermes-memory
j package add ./examples/packages/j-hermes-memory
j package doctor
j-tui --list-tools
j-tui --list-skills
```

To publish your own package, put `j-package.json` at the root of its Git
repository and pin a tag or commit:

```sh
j package add git:https://github.com/example/j-memory-package.git@v1.0.0
```

Run the package-owned tests with:

```sh
python3 -m unittest -v test_server.py
```
