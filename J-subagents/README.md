# J-subagents

J-subagents is an optional foreground subagent Tool for J-agent. A host defines
named recipes from J-agent's existing `Model` and `Tool` seams:

```go
subagentTool, err := subagents.NewTool(subagents.Definition{
    Name:         "research",
    Description:  "Research one bounded question and return evidence.",
    Model:        researchModel,
    SystemPrompt: "Work independently and report concise evidence.",
    Tools:        []agent.Tool{searchTool},
    EventHandler: func(event agent.Event) {
        // Project the child run into host-owned logs, metrics, or UI.
    },
})
if err != nil {
    return err
}
runner, err := agent.New(parentModel, agent.WithTools(subagentTool))
```

The parent model calls `subagent_run` with an exact agent name and task. With
`NewTool`, every call constructs a fresh J-agent conversation, propagates
cancellation, exposes only the recipe's Tools, and returns structured final
content, turn count, and provider-reported usage. Calls using the same
definition are serialized because the J-agent `Model` contract does not
promise concurrent safety.

`EventHandler` is optional and reuses J-agent's existing synchronous event
contract. A host may observe the child run without adding a subagent event
protocol to J-agent; the host already knows the definition name and may attach
that identity in its own projection.

This package is intentionally named and implemented as J-subagents. There is no
J-delegate layer or new J-agent runtime interface.

Hosts that already own a durable parent session can opt into child restoration:

```go
subagentTool, err := subagents.NewSessionTool(
    subagents.SessionConfig{
        ParentID: parentSessionID,
        Store:    transcriptStore,
    },
    definitions...,
)
```

The store receives complete transcript snapshots under opaque child keys.
`J-mem/transcript.Store` satisfies the narrow `TranscriptStore` contract, while
an external host may provide another implementation without importing J-mem.
Each new call returns a host-generated `session`; passing that exact value in a
later `subagent_run` continues the child through J-agent's existing
`WithHistory` validation. The restored transcript is authoritative, including
its original system message; changing a recipe's system prompt affects only new
child sessions.

Snapshots are written when the child user turn is accepted and after each
complete model/tool turn. Failed calls return their session ID alongside the
error so a host can explicitly continue from the last valid checkpoint.
J-subagents does not automatically replay an interrupted provider stream or
partially observed Tool call.

The package does not provide background execution, parallel or chain
orchestration, recursive inheritance, worktrees, approval, a session browser,
or a generic agent registry. Hard-crash discovery, retention, and task trees
remain host policy rather than being implied by `WithHistory`. Multiple
processes must not resume the same child concurrently; cross-process
coordination is deliberately not hidden inside the two-method store contract.
