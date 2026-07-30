# Custom J-agent host

This standalone Go module demonstrates source-level J-agent composition without
J-tui, J-packages, a repository `replace` directive, or a universal Extension
interface.

It provides:

- one community-owned `agent.Tool`;
- one community-owned `agent.Model`;
- an ordinary Model wrapper;
- an event handler;
- transcript restoration through `History` and `WithHistory`.

Validate it as an external consumer:

```bash
GOWORK=off go test ./...
GOWORK=off go vet ./...
GOWORK=off go build ./...
```

The scripted Model keeps the example deterministic and credential-free. Replace
it with any Model implementation without changing the Tool, event, or history
composition.
