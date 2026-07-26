# J-mem

J-mem will provide optional local memory for J-agent.

Its first version has two independent parts:

- short-term conversation persistence in local SQLite using
  `History`/`WithHistory`;
- long-term memory in local JSONL exposed through retrieve, store, modify, and
  forget tools.

J-mem owns storage schema, migration, indexing, retention, and memory policy.
It does not add a `Memory` or `Storage` interface to J-agent.

J-mem is currently a boundary definition, not an implemented product. See
[the repository design](../docs/design.md).
