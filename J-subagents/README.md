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
})
if err != nil {
    return err
}
runner, err := agent.New(parentModel, agent.WithTools(subagentTool))
```

The parent model calls `subagent_run` with an exact agent name and task. Every
call constructs a fresh J-agent conversation, propagates cancellation, exposes
only the recipe's Tools, and returns structured final content, turn count, and
provider-reported usage. Calls using the same definition are serialized because
the J-agent `Model` contract does not promise concurrent safety.

This package is intentionally named and implemented as J-subagents. There is no
J-delegate layer or new J-agent runtime interface.

The first version does not provide background execution, parallel or chain
orchestration, transcript persistence, recursive inheritance, worktrees,
approval, or a generic agent registry. Those policies can be composed by a
host after a concrete integration requires them.
