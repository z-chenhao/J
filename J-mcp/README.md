# J-mcp

J-mcp is the optional MCP-to-J-agent bridge. It initializes one explicitly
configured MCP server, freezes its advertised tools, and returns them as
ordinary `agent.Tool` values.

It does not depend on J-tui, read product configuration, construct an Agent, or
define a universal extension interface.

```go
connection, err := jmcp.DialStdio(ctx, jmcp.StdioConfig{
    Command: "mcp-server-filesystem",
    Args:    []string{"/workspace"},
})
if err != nil {
    return err
}
defer connection.Close()

tools, err := connection.Tools(ctx)
if err != nil {
    return err
}

runner, err := agent.New(model, agent.WithTools(tools...))
```

`DialStdio` is the first-party recipe. Advanced applications may call
`jmcp.Connect` with an official SDK `mcp.Transport`; J-mcp does not duplicate
that standard with a J-specific transport interface.

Version 0.1 intentionally projects text tool results only. Prompts, resources,
sampling, elicitation, runtime tool mutation, package discovery, and
installation remain outside this module.

An MCP server process runs with the operating-system permissions assigned by
its host. J-mcp provides protocol separation, not a sandbox.
