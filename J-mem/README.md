# J-mem

J-mem provides two independent, optional capabilities for J-agent:

- package `transcript` stores complete conversation snapshots in local SQLite
  and restores them through `agent.WithHistory`;
- package `memory` stores long-term memory as an inspectable append-only JSONL
  log and exposes retrieve, store, modify, and forget as ordinary
  `agent.Tool` values.

## Transcript persistence

```go
store, err := transcript.Open("state/transcripts.db")
if err != nil {
    return err
}
defer store.Close()

if err := store.Save(ctx, sessionID, runner.History()); err != nil {
    return err
}

history, err := store.Load(ctx, sessionID)
if err != nil {
    return err
}
restored, err := agent.New(model, agent.WithHistory(history...))
```

The SQLite store owns a versioned local schema and atomically replaces one
session snapshot. J-agent remains responsible for validating restored
transcript invariants during construction.

## Long-term memory tools

```go
memories, err := memory.Open("state/memory.jsonl")
if err != nil {
    return err
}

runner, err := agent.New(
    model,
    agent.WithTools(memories.Tools()...),
)
```

The four tools are:

- `memory_retrieve`
- `memory_store`
- `memory_modify`
- `memory_forget`

The JSONL file is append-only: modify operations add replacement events and
forget operations add tombstones.

Retrieval returns a bounded candidate set:

1. case-insensitive phrase and whitespace-delimited term matches rank first;
2. the most recently updated active records fill remaining capacity;
3. the calling Agent model decides which returned candidates are semantically
   relevant to the current request.

This deliberately fixes false negatives in small local stores such as a query
about `位置 城市 出发 所在地` against `用户现在在杭州`, without claiming that
lexical ranking itself is an embedding model. Returned records are candidates,
not verified matches.

The JSONL event log remains authoritative and inspectable. Embeddings, graph
synthesis, ambient prompt injection, compaction, retention, cross-process
writers, and a universal `Memory` or `Storage` interface remain deliberately
absent until a real larger corpus proves the bounded policy insufficient.
