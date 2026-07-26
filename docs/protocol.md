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

### State and messages

```json
{"id":"4","type":"state"}
{"id":"5","type":"messages"}
```

State contains observed runtime facts only: session identity, active and queued
work, message count, and ordered task snapshots.

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
  message.created and/or tool.started + tool.completed
  turn.completed
agent.completed
task.completed
```

`message.created` is used instead of artificial start/end pairs because the
current model contract is non-streaming.

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
to 1 MiB.

## Not supported

There are no aliases, protocol-selection environment variables, steering or
follow-up queue modes, provider management, retry, compaction, or compatibility
claims for other agent protocols.
