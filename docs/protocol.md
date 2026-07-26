# `j-core` protocol 0.1

The reference process reads one JSON command per line from stdin and writes one
JSON response or event per line to stdout.

Version `0.1` is experimental.

## Commands

### Submit

```json
{"id":"1","type":"submit","message":"hello"}
```

The response is emitted before task events:

```json
{
  "type":"response",
  "protocol":"j-core",
  "protocolVersion":"0.1",
  "id":"1",
  "command":"submit",
  "success":true,
  "data":{"taskId":"task-000001"}
}
```

### Cancel

```json
{"id":"2","type":"cancel","taskId":"task-000001"}
```

Canceling a queued or running task produces exactly one `task.canceled`
terminal event. Canceling a task that is already terminal fails with
`task_already_terminal`.

### Get one task

```json
{"id":"3","type":"task.get","taskId":"task-000001"}
```

A settled task reports observed timestamps, queue and run duration, model/tool
duration, first-delta latency, turn count, and aggregate token usage when the
provider reported it.

### State and messages

```json
{"id":"4","type":"state"}
{"id":"5","type":"messages"}
```

State contains observed runtime facts only: session identity, active and queued
work, message count, and ordered task snapshots.

Messages contain ordered content blocks:

```json
{
  "role":"assistant",
  "content":[
    {"type":"reasoning","text":"..."},
    {"type":"text","text":"I will check."},
    {
      "type":"tool_call",
      "toolCall":{
        "id":"call-1",
        "name":"weather",
        "arguments":{"city":"Hangzhou"}
      }
    }
  ]
}
```

Reasoning is retained for correct provider continuation. A client may omit it
from display.

### Reset

```json
{"id":"6","type":"reset"}
```

Reset succeeds only when no task is active or queued.

## Events

Every event contains:

- `type`
- `protocol`
- `protocolVersion`
- monotonic `sequence`
- UTC `timestamp`
- `sessionId`

Applicable events also contain `taskId`, `runId`, `turnId`, or `messageId`.

A successful task emits:

```text
task.queued
task.started
agent.started
  turn.started
    message.started
    message.delta*
    message.completed
    tool.started + tool.completed (zero or more)
  turn.completed
agent.completed
task.completed
```

`message.delta` contains a typed `delta`:

```json
{
  "type":"message.delta",
  "delta":{"type":"text","index":0,"delta":"hello"}
}
```

Delta types are `text`, `reasoning`, and `tool_call`. `index` is scoped to the
delta type, so interleaved streams remain unambiguous.

`turn.completed` contains a normalized model observation:

```json
{
  "type":"turn.completed",
  "model":{
    "provider":"ollama",
    "model":"qwen3",
    "stopReason":"stop",
    "usage":{"inputTokens":12,"outputTokens":8,"totalTokens":20},
    "durationMs":420,
    "firstDeltaMs":85
  }
}
```

`firstDeltaMs` is absent for a non-streaming custom adapter. Tool completion
events carry `durationMs`. Model duration is end-to-end wall time and therefore
includes synchronous delta delivery to the run's event handler.

If a started message or turn fails, it terminates with `message.failed` or
`turn.failed` before `agent.failed`.

Terminal task events are mutually exclusive:

- `task.completed`
- `task.failed`
- `task.canceled`

## Errors

Failed command responses use:

```json
{
  "type":"response",
  "protocol":"j-core",
  "protocolVersion":"0.1",
  "command":"submit",
  "success":false,
  "error":{"code":"message_required","message":"message is required"}
}
```

Unknown fields and commands are rejected. Individual command lines are limited
to 1 MiB. Provider credentials are never written to protocol output.

## Not supported

There are no aliases, universal hooks, protocol-selection environment
variables, steering or follow-up queue modes, provider management, automatic
retry, compaction, or compatibility claims for other agent protocols.
