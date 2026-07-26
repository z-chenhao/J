package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/z-chenhao/J/agent"
)

type runner interface {
	Run(context.Context, string, agent.EventHandler) (agent.Message, error)
	History() []agent.Message
	Reset()
}

type taskState struct {
	ID          string
	RunID       string
	Status      string
	Prompt      string
	Output      string
	Error       string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt time.Time
}

func (t taskState) snapshot() taskSnapshot {
	snapshot := taskSnapshot{
		ID:        t.ID,
		RunID:     t.RunID,
		Status:    t.Status,
		Prompt:    t.Prompt,
		Output:    t.Output,
		Error:     t.Error,
		CreatedAt: t.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt: t.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if !t.CompletedAt.IsZero() {
		snapshot.CompletedAt = t.CompletedAt.UTC().Format(time.RFC3339Nano)
	}
	return snapshot
}

// Runtime owns the reference JSONL task queue. It is intentionally internal;
// embedders customize J through the public agent package.
type Runtime struct {
	runner runner
	out    io.Writer

	mu             sync.Mutex
	sessionID      string
	taskCounter    uint64
	runCounter     uint64
	turnCounter    uint64
	messageCounter uint64
	eventSequence  uint64
	tasks          map[string]*taskState
	queue          []string
	workerRunning  bool
	activeTaskID   string
	activeRunID    string
	activeTurnID   string
	cancelActive   context.CancelFunc

	writerMu sync.Mutex
	errMu    sync.Mutex
	writeErr error
	wg       sync.WaitGroup
}

// New creates a reference runtime around an injected agent.
func New(runner runner, out io.Writer) (*Runtime, error) {
	if runner == nil {
		return nil, errors.New("runner is required")
	}
	if out == nil {
		return nil, errors.New("output writer is required")
	}
	return &Runtime{
		runner:    runner,
		out:       out,
		sessionID: "session-1",
		tasks:     make(map[string]*taskState),
	}, nil
}

// Handle processes one typed protocol command.
func (r *Runtime) Handle(cmd command) {
	cmd.ID = strings.TrimSpace(cmd.ID)
	cmd.Type = strings.TrimSpace(strings.ToLower(cmd.Type))

	switch cmd.Type {
	case commandSubmit:
		prompt := strings.TrimSpace(cmd.Message)
		if prompt == "" {
			r.emitFailure(cmd, codeMessageNeeded, "message is required")
			return
		}
		taskID, startWorker := r.enqueue(prompt)
		r.emitSuccess(cmd, &responseData{TaskID: taskID})
		r.emitTaskEvent("task.queued", taskID, taskQueued, "")
		if startWorker {
			r.launchWorker()
		}

	case commandCancel:
		taskID := strings.TrimSpace(cmd.TaskID)
		if taskID == "" {
			r.emitFailure(cmd, codeTaskNeeded, "taskId is required")
			return
		}
		status, ok := r.cancel(taskID)
		if !ok {
			r.emitFailure(cmd, codeTaskNotFound, "task not found")
			return
		}
		if status != taskCanceled && status != taskRunning {
			r.emitFailure(cmd, codeTaskTerminal, "task is already terminal")
			return
		}
		r.emitSuccess(cmd, &responseData{TaskID: taskID})
		if status == taskCanceled {
			r.emitTaskEvent("task.canceled", taskID, taskCanceled, "")
		}

	case commandTask:
		taskID := strings.TrimSpace(cmd.TaskID)
		if taskID == "" {
			r.emitFailure(cmd, codeTaskNeeded, "taskId is required")
			return
		}
		task, ok := r.task(taskID)
		if !ok {
			r.emitFailure(cmd, codeTaskNotFound, "task not found")
			return
		}
		r.emitSuccess(cmd, &responseData{Task: &task})

	case commandState:
		state := r.state()
		r.emitSuccess(cmd, &responseData{State: &state})

	case commandMessages:
		r.emitSuccess(cmd, &responseData{Messages: r.runner.History()})

	case commandReset:
		if !r.reset() {
			r.emitFailure(cmd, codeRuntimeBusy, "runtime must be idle before reset")
			return
		}
		r.emitSuccess(cmd, nil)
		r.emit(event{Type: "session.reset"})

	default:
		r.emitFailure(cmd, codeInvalidCommand, "unsupported command")
	}
}

func (r *Runtime) enqueue(prompt string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.taskCounter++
	taskID := fmt.Sprintf("task-%06d", r.taskCounter)
	now := time.Now()
	r.tasks[taskID] = &taskState{
		ID:        taskID,
		Status:    taskQueued,
		Prompt:    prompt,
		CreatedAt: now,
		UpdatedAt: now,
	}
	r.queue = append(r.queue, taskID)
	startWorker := !r.workerRunning
	if startWorker {
		r.workerRunning = true
	}
	return taskID, startWorker
}

func (r *Runtime) launchWorker() {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for r.runNext() {
		}
	}()
}

func (r *Runtime) runNext() bool {
	r.mu.Lock()
	if len(r.queue) == 0 {
		r.workerRunning = false
		r.mu.Unlock()
		return false
	}

	taskID := r.queue[0]
	r.queue = r.queue[1:]
	task := r.tasks[taskID]
	r.runCounter++
	runID := fmt.Sprintf("run-%06d", r.runCounter)
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	task.RunID = runID
	task.Status = taskRunning
	task.UpdatedAt = now
	r.activeTaskID = taskID
	r.activeRunID = runID
	r.activeTurnID = ""
	r.cancelActive = cancel
	r.mu.Unlock()

	r.emitTaskEvent("task.started", taskID, taskRunning, "")
	result, runErr := r.runner.Run(ctx, task.Prompt, r.forwardAgentEvent)
	cancel()

	r.mu.Lock()
	finalStatus := taskCompleted
	finalError := ""
	if errors.Is(ctx.Err(), context.Canceled) && task.Status == taskCanceled {
		finalStatus = taskCanceled
	} else if runErr != nil {
		finalStatus = taskFailed
		finalError = runErr.Error()
	}
	task.Status = finalStatus
	task.Error = finalError
	if runErr == nil {
		task.Output = result.Content
	}
	task.UpdatedAt = time.Now()
	task.CompletedAt = task.UpdatedAt
	r.activeTaskID = ""
	r.activeRunID = ""
	r.activeTurnID = ""
	r.cancelActive = nil
	r.mu.Unlock()

	switch finalStatus {
	case taskCompleted:
		r.emitTaskEvent("task.completed", taskID, finalStatus, "")
	case taskCanceled:
		r.emitTaskEvent("task.canceled", taskID, finalStatus, "")
	case taskFailed:
		r.emitTaskEvent("task.failed", taskID, finalStatus, finalError)
	}
	return true
}

func (r *Runtime) forwardAgentEvent(agentEvent agent.Event) {
	wireEvent := event{
		Type:     string(agentEvent.Type),
		Message:  agentEvent.Message,
		ToolCall: agentEvent.ToolCall,
		Output:   agentEvent.Output,
		IsError:  agentEvent.IsError,
		Error:    agentEvent.Error,
	}

	r.mu.Lock()
	switch agentEvent.Type {
	case agent.EventTurnStarted:
		r.turnCounter++
		r.activeTurnID = fmt.Sprintf("turn-%06d", r.turnCounter)
	case agent.EventTurnCompleted:
		wireEvent.TurnID = r.activeTurnID
		r.activeTurnID = ""
	case agent.EventMessageCreated:
		r.messageCounter++
		wireEvent.MessageID = fmt.Sprintf("message-%06d", r.messageCounter)
	}
	r.mu.Unlock()
	r.emit(wireEvent)
}

func (r *Runtime) cancel(taskID string) (string, bool) {
	r.mu.Lock()
	task, ok := r.tasks[taskID]
	if !ok {
		r.mu.Unlock()
		return "", false
	}
	switch task.Status {
	case taskQueued:
		task.Status = taskCanceled
		task.UpdatedAt = time.Now()
		task.CompletedAt = task.UpdatedAt
		for i, queuedID := range r.queue {
			if queuedID == taskID {
				r.queue = append(r.queue[:i], r.queue[i+1:]...)
				break
			}
		}
		r.mu.Unlock()
		return taskCanceled, true
	case taskRunning:
		task.Status = taskCanceled
		task.UpdatedAt = time.Now()
		cancel := r.cancelActive
		r.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return taskRunning, true
	default:
		status := task.Status
		r.mu.Unlock()
		return status, true
	}
}

func (r *Runtime) task(taskID string) (taskSnapshot, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[taskID]
	if !ok {
		return taskSnapshot{}, false
	}
	return task.snapshot(), true
}

func (r *Runtime) state() stateSnapshot {
	messages := r.runner.History()
	r.mu.Lock()
	defer r.mu.Unlock()

	tasks := make([]taskSnapshot, 0, len(r.tasks))
	for _, task := range r.tasks {
		tasks = append(tasks, task.snapshot())
	}
	sort.Slice(tasks, func(i, j int) bool {
		return tasks[i].ID < tasks[j].ID
	})
	return stateSnapshot{
		SessionID:    r.sessionID,
		Running:      r.activeTaskID != "",
		ActiveTaskID: r.activeTaskID,
		QueuedTasks:  len(r.queue),
		MessageCount: len(messages),
		Tasks:        tasks,
	}
}

func (r *Runtime) reset() bool {
	r.mu.Lock()
	if r.activeTaskID != "" || len(r.queue) != 0 {
		r.mu.Unlock()
		return false
	}
	r.tasks = make(map[string]*taskState)
	r.taskCounter = 0
	r.runCounter = 0
	r.turnCounter = 0
	r.messageCounter = 0
	r.sessionID = fmt.Sprintf("session-%d", time.Now().UnixNano())
	r.mu.Unlock()
	r.runner.Reset()
	return true
}

func (r *Runtime) emitTaskEvent(eventType, taskID, status, errorMessage string) {
	task, _ := r.task(taskID)
	wireEvent := event{
		Type:   eventType,
		TaskID: taskID,
		RunID:  task.RunID,
		Status: status,
		Error:  errorMessage,
	}
	r.emit(wireEvent)
}

func (r *Runtime) emitSuccess(cmd command, data *responseData) {
	r.write(response{
		Type:            "response",
		Protocol:        protocolName,
		ProtocolVersion: protocolVersion,
		ID:              cmd.ID,
		Command:         cmd.Type,
		Success:         true,
		Data:            data,
	})
}

func (r *Runtime) emitFailure(cmd command, code, message string) {
	r.write(response{
		Type:            "response",
		Protocol:        protocolName,
		ProtocolVersion: protocolVersion,
		ID:              cmd.ID,
		Command:         cmd.Type,
		Success:         false,
		Error:           &protocolError{Code: code, Message: message},
	})
}

func (r *Runtime) emit(wireEvent event) {
	r.writerMu.Lock()
	defer r.writerMu.Unlock()

	r.mu.Lock()
	r.eventSequence++
	wireEvent.Protocol = protocolName
	wireEvent.ProtocolVersion = protocolVersion
	wireEvent.Sequence = r.eventSequence
	wireEvent.Timestamp = time.Now().UTC()
	wireEvent.SessionID = r.sessionID
	if wireEvent.TaskID == "" {
		wireEvent.TaskID = r.activeTaskID
	}
	if wireEvent.RunID == "" {
		wireEvent.RunID = r.activeRunID
	}
	if wireEvent.TurnID == "" {
		wireEvent.TurnID = r.activeTurnID
	}
	r.mu.Unlock()
	r.writeLocked(wireEvent)
}

func (r *Runtime) write(value any) {
	r.writerMu.Lock()
	defer r.writerMu.Unlock()
	r.writeLocked(value)
}

func (r *Runtime) writeLocked(value any) {
	err := json.NewEncoder(r.out).Encode(value)
	if err == nil {
		return
	}
	r.errMu.Lock()
	if r.writeErr == nil {
		r.writeErr = err
	}
	r.errMu.Unlock()
}

// Wait blocks until queued work settles.
func (r *Runtime) Wait() {
	r.wg.Wait()
}

// Err returns the first protocol output failure.
func (r *Runtime) Err() error {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	return r.writeErr
}
