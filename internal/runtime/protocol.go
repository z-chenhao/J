package runtime

import (
	"time"

	"github.com/z-chenhao/J-agent/agent"
)

const (
	protocolName    = "j-agent"
	protocolVersion = "0.1"
)

const (
	commandSubmit   = "submit"
	commandCancel   = "cancel"
	commandState    = "state"
	commandMessages = "messages"
	commandReset    = "reset"
	commandTask     = "task.get"
)

const (
	taskQueued    = "queued"
	taskRunning   = "running"
	taskCompleted = "completed"
	taskFailed    = "failed"
	taskCanceled  = "canceled"
)

const (
	codeInvalidJSON    = "invalid_json"
	codeInvalidCommand = "invalid_command"
	codeMessageNeeded  = "message_required"
	codeTaskNeeded     = "task_id_required"
	codeTaskNotFound   = "task_not_found"
	codeTaskTerminal   = "task_already_terminal"
	codeRuntimeBusy    = "runtime_busy"
)

type command struct {
	ID      string `json:"id,omitempty"`
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
	TaskID  string `json:"taskId,omitempty"`
}

type protocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type taskSnapshot struct {
	ID              string       `json:"id"`
	RunID           string       `json:"runId,omitempty"`
	Status          string       `json:"status"`
	Prompt          string       `json:"prompt"`
	Output          string       `json:"output,omitempty"`
	Error           string       `json:"error,omitempty"`
	CreatedAt       string       `json:"createdAt"`
	StartedAt       string       `json:"startedAt,omitempty"`
	UpdatedAt       string       `json:"updatedAt"`
	CompletedAt     string       `json:"completedAt,omitempty"`
	QueueDurationMS *int64       `json:"queueDurationMs,omitempty"`
	DurationMS      *int64       `json:"durationMs,omitempty"`
	ModelDurationMS *int64       `json:"modelDurationMs,omitempty"`
	ToolDurationMS  *int64       `json:"toolDurationMs,omitempty"`
	FirstDeltaMS    *int64       `json:"firstDeltaMs,omitempty"`
	Turns           *int         `json:"turns,omitempty"`
	Usage           *agent.Usage `json:"usage,omitempty"`
}

type stateSnapshot struct {
	SessionID    string         `json:"sessionId"`
	Running      bool           `json:"running"`
	ActiveTaskID string         `json:"activeTaskId,omitempty"`
	QueuedTasks  int            `json:"queuedTasks"`
	MessageCount int            `json:"messageCount"`
	Tasks        []taskSnapshot `json:"tasks"`
}

type responseData struct {
	TaskID   string          `json:"taskId,omitempty"`
	Task     *taskSnapshot   `json:"task,omitempty"`
	State    *stateSnapshot  `json:"state,omitempty"`
	Messages []agent.Message `json:"messages,omitempty"`
}

type response struct {
	Type            string         `json:"type"`
	Protocol        string         `json:"protocol"`
	ProtocolVersion string         `json:"protocolVersion"`
	ID              string         `json:"id,omitempty"`
	Command         string         `json:"command"`
	Success         bool           `json:"success"`
	Data            *responseData  `json:"data,omitempty"`
	Error           *protocolError `json:"error,omitempty"`
}

type event struct {
	Type            string            `json:"type"`
	Protocol        string            `json:"protocol"`
	ProtocolVersion string            `json:"protocolVersion"`
	Sequence        uint64            `json:"sequence"`
	Timestamp       time.Time         `json:"timestamp"`
	SessionID       string            `json:"sessionId"`
	TaskID          string            `json:"taskId,omitempty"`
	RunID           string            `json:"runId,omitempty"`
	TurnID          string            `json:"turnId,omitempty"`
	MessageID       string            `json:"messageId,omitempty"`
	Status          string            `json:"status,omitempty"`
	Message         *agent.Message    `json:"message,omitempty"`
	Delta           *agent.ModelDelta `json:"delta,omitempty"`
	Model           *modelObservation `json:"model,omitempty"`
	ToolCall        *agent.ToolCall   `json:"toolCall,omitempty"`
	Output          string            `json:"output,omitempty"`
	DurationMS      *int64            `json:"durationMs,omitempty"`
	IsError         bool              `json:"isError,omitempty"`
	Error           string            `json:"error,omitempty"`
}

type modelObservation struct {
	Provider     string           `json:"provider"`
	Model        string           `json:"model"`
	ResponseID   string           `json:"responseId,omitempty"`
	StopReason   agent.StopReason `json:"stopReason"`
	Usage        *agent.Usage     `json:"usage,omitempty"`
	DurationMS   int64            `json:"durationMs"`
	FirstDeltaMS *int64           `json:"firstDeltaMs,omitempty"`
}
