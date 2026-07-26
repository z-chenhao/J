# Reference runtime

This package implements J's experimental JSONL reference transport. It remains
under `internal/` because process transport and queue policy have not earned a
stable Go API.

Supported commands:

- `submit`
- `cancel`
- `task.get`
- `state`
- `messages`
- `reset`

There are no command aliases, protocol-selection environment variables, or
advertised placeholders for unimplemented features. Downstream systems may
translate the single `j-core` event stream outside J.

The stream reports model deltas, normalized usage and stop reason, first-delta
and total latency, tool duration, and task queue/run duration. It does not
provide a generic lifecycle Hook API.
