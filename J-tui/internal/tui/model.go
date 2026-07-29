// Package tui implements the terminal-specific consumer of J-agent events.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/z-chenhao/J/J-agent/agent"
	"github.com/z-chenhao/J/J-tui/internal/observe"
)

const maxInputHeight = 5

type runner interface {
	Run(context.Context, string, observe.Handler) (agent.RunResult, error)
}

type itemKind uint8

const (
	itemUser itemKind = iota
	itemAssistant
	itemTool
	itemError
)

type transcriptItem struct {
	kind           itemKind
	label          string
	text           string
	status         string
	id             string
	reasoning      string
	toolName       string
	toolArguments  string
	toolOutput     string
	toolError      string
	rendered       string
	renderWidth    int
	reasoningView  string
	reasoningWidth int
}

type eventMsg struct {
	event observe.Event
}

type runDoneMsg struct {
	err error
}

type runMetrics struct {
	turns         int
	modelDuration time.Duration
	firstDelta    *time.Duration
	usage         *agent.Usage
	usageComplete bool
}

// Model is one full-screen J-agent conversation.
type Model struct {
	ctx      context.Context
	runner   runner
	provider string
	model    string
	session  string

	input    textarea.Model
	viewport viewport.Model
	spinner  spinner.Model
	width    int
	height   int

	items            []transcriptItem
	activeMessages   map[string]int
	activeTools      map[string]int
	reasoningBytes   int
	status           string
	runMetrics       runMetrics
	subagentMetrics  map[string]runMetrics
	running          bool
	cancel           context.CancelFunc
	events           chan tea.Msg
	initialPrompt    string
	followOutput     bool
	toolsExpanded    bool
	thinkingExpanded bool
	isDark           bool
	styles           styles
}

// New constructs a TUI that renders one runner conversation.
func New(
	ctx context.Context,
	runner runner,
	provider, model, initialPrompt, session string,
) Model {
	if ctx == nil {
		ctx = context.Background()
	}
	const defaultDarkBackground = true
	input := textarea.New()
	input.Placeholder = "Ask J anything"
	input.Prompt = "› "
	input.CharLimit = 32 * 1024
	input.DynamicHeight = true
	input.MinHeight = 1
	input.MaxHeight = maxInputHeight
	input.SetStyles(textarea.DefaultStyles(defaultDarkBackground))
	input.ShowLineNumbers = false
	input.Focus()

	view := viewport.New(viewport.WithWidth(80), viewport.WithHeight(16))
	view.KeyMap.PageUp.SetKeys("pgup")
	view.KeyMap.PageDown.SetKeys("pgdown")
	view.KeyMap.HalfPageUp.Unbind()
	view.KeyMap.HalfPageDown.Unbind()
	view.KeyMap.Up.SetKeys("alt+up")
	view.KeyMap.Down.SetKeys("alt+down")
	view.KeyMap.Left.Unbind()
	view.KeyMap.Right.Unbind()

	progress := spinner.New()
	progress.Spinner = spinner.Dot
	styleSet := newStyles(defaultDarkBackground)
	progress.Style = styleSet.accent

	result := Model{
		ctx:              ctx,
		runner:           runner,
		provider:         provider,
		model:            model,
		input:            input,
		viewport:         view,
		spinner:          progress,
		width:            80,
		height:           24,
		activeMessages:   make(map[string]int),
		activeTools:      make(map[string]int),
		subagentMetrics:  make(map[string]runMetrics),
		status:           "ready",
		initialPrompt:    strings.TrimSpace(initialPrompt),
		followOutput:     true,
		thinkingExpanded: true,
		isDark:           defaultDarkBackground,
		styles:           styleSet,
	}
	result.session = session
	result.resize()
	result.syncViewport()
	return result
}

// Init starts cursor blinking and optionally submits the initial prompt.
func (m Model) Init() tea.Cmd {
	if m.initialPrompt != "" {
		return tea.Batch(tea.RequestBackgroundColor, textarea.Blink, func() tea.Msg {
			return submitMsg(m.initialPrompt)
		})
	}
	return tea.Batch(tea.RequestBackgroundColor, textarea.Blink)
}

type submitMsg string

// Update applies keyboard, window, and J-agent lifecycle events.
func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.BackgroundColorMsg:
		m.applyBackground(msg.IsDark())
		m.syncViewport()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = max(msg.Width, 24)
		m.height = max(msg.Height, 10)
		m.resize()
		m.syncViewport()
		return m, nil

	case tea.KeyPressMsg:
		if key.Matches(
			msg,
			m.viewport.KeyMap.PageUp,
			m.viewport.KeyMap.PageDown,
			m.viewport.KeyMap.Up,
			m.viewport.KeyMap.Down,
		) {
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			m.followOutput = m.viewport.AtBottom()
			return m, cmd
		}
		switch msg.String() {
		case "ctrl+c":
			if m.cancel != nil {
				m.cancel()
			}
			return m, tea.Quit
		case "esc":
			if m.running && m.cancel != nil {
				m.status = "canceling"
				m.cancel()
			}
			return m, nil
		case "ctrl+o":
			m.toolsExpanded = !m.toolsExpanded
			m.syncViewport()
			return m, nil
		case "ctrl+t":
			m.thinkingExpanded = !m.thinkingExpanded
			m.syncViewport()
			return m, nil
		case "home":
			m.viewport.GotoTop()
			m.followOutput = false
			return m, nil
		case "end":
			m.viewport.GotoBottom()
			m.followOutput = true
			return m, nil
		case "enter":
			if m.running {
				return m, nil
			}
			prompt := strings.TrimSpace(m.input.Value())
			if prompt == "" {
				return m, nil
			}
			return m.submit(prompt)
		}

	case submitMsg:
		if !m.running && strings.TrimSpace(string(msg)) != "" {
			return m.submit(string(msg))
		}
		return m, nil

	case spinner.TickMsg:
		if !m.running {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case eventMsg:
		m.applyObservedEvent(msg.event)
		m.syncViewport()
		return m, waitForEvent(m.events)

	case runDoneMsg:
		m.running = false
		m.cancel = nil
		m.events = nil
		if errors.Is(msg.err, context.Canceled) {
			m.status = "canceled"
		} else if msg.err != nil {
			m.status = "failed"
			if !m.hasTerminalError(msg.err.Error()) {
				m.items = append(m.items, transcriptItem{
					kind: itemError, label: "error", text: msg.err.Error(),
				})
			}
		} else {
			m.status = "ready"
		}
		m.activeMessages = make(map[string]int)
		m.syncViewport()
		return m, nil
	}

	var cmd tea.Cmd
	previousHeight := m.input.Height()
	m.input, cmd = m.input.Update(message)
	m.resize()
	if m.input.Height() != previousHeight {
		m.syncViewport()
	}
	return m, cmd
}

func (m Model) submit(prompt string) (tea.Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(m.ctx)
	m.cancel = cancel
	m.running = true
	m.status = "starting"
	m.runMetrics = runMetrics{}
	m.reasoningBytes = 0
	m.activeMessages = make(map[string]int)
	m.activeTools = make(map[string]int)
	m.subagentMetrics = make(map[string]runMetrics)
	m.followOutput = true
	m.input.Reset()
	m.resize()
	m.items = append(m.items, transcriptItem{
		kind: itemUser, label: "you", text: strings.TrimSpace(prompt),
	})
	m.events = make(chan tea.Msg, 256)
	m.syncViewport()
	return m, tea.Batch(startRun(ctx, m.runner, prompt, m.events), m.spinner.Tick)
}

func startRun(ctx context.Context, runner runner, prompt string, events chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		go func() {
			_, err := runner.Run(ctx, prompt, func(event observe.Event) {
				select {
				case events <- eventMsg{event: event}:
				case <-ctx.Done():
				}
			})
			select {
			case events <- runDoneMsg{err: err}:
			case <-ctx.Done():
				events <- runDoneMsg{err: err}
			}
		}()
		return <-events
	}
}

func waitForEvent(events <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-events
	}
}

func (m *Model) applyEvent(event agent.Event) {
	m.applyObservedEvent(observe.Event{Runtime: event})
}

func (m *Model) applyObservedEvent(observed observe.Event) {
	event := observed.Runtime
	source := observed.Subagent
	label := "j"
	if source != "" {
		label = source
	}
	switch event.Type {
	case agent.EventAgentStarted:
		m.status = scopedStatus(source, "running")
	case agent.EventTurnStarted:
		m.status = scopedStatus(source, "thinking")
	case agent.EventMessageStarted:
		m.status = scopedStatus(source, "streaming")
		m.items = append(m.items, transcriptItem{
			kind: itemAssistant, label: label, status: "streaming",
		})
		m.activeMessages[source] = len(m.items) - 1
	case agent.EventMessageDelta:
		if event.Delta == nil {
			return
		}
		activeMessage, active := m.activeMessages[source]
		switch event.Delta.Type {
		case agent.DeltaText:
			m.status = scopedStatus(source, "responding")
			if active {
				m.items[activeMessage].text += event.Delta.Delta
				m.items[activeMessage].renderWidth = 0
			}
		case agent.DeltaReasoning:
			m.status = scopedStatus(source, "thinking")
			m.reasoningBytes += len(event.Delta.Delta)
			if active {
				m.items[activeMessage].reasoning += event.Delta.Delta
				m.items[activeMessage].reasoningWidth = 0
			}
		case agent.DeltaToolCall:
			m.status = scopedStatus(source, "preparing tool")
		}
	case agent.EventMessageCompleted:
		activeMessage, active := m.activeMessages[source]
		if active {
			if event.Message != nil && m.items[activeMessage].text == "" {
				m.items[activeMessage].text = event.Message.Text()
				m.items[activeMessage].renderWidth = 0
			}
			if event.Message != nil &&
				m.items[activeMessage].reasoning == "" {
				m.items[activeMessage].reasoning = messageReasoning(*event.Message)
				m.items[activeMessage].reasoningWidth = 0
			}
			m.items[activeMessage].status = ""
			if m.items[activeMessage].text == "" &&
				strings.TrimSpace(m.items[activeMessage].reasoning) == "" {
				m.items = append(m.items[:activeMessage], m.items[activeMessage+1:]...)
			}
		}
		delete(m.activeMessages, source)
		m.status = scopedStatus(source, "running")
	case agent.EventMessageFailed:
		if m.status == "canceling" {
			if activeMessage, active := m.activeMessages[source]; active {
				m.items[activeMessage].status = "canceled"
				delete(m.activeMessages, source)
			}
			return
		}
		m.finishMessageWithError(source, event.Error)
	case agent.EventToolStarted:
		if event.ToolCall == nil {
			return
		}
		toolName := event.ToolCall.Name
		if source != "" {
			toolName = source + " › " + toolName
		}
		item := transcriptItem{
			kind:          itemTool,
			label:         "tool",
			status:        "running",
			id:            event.ToolCall.ID,
			toolName:      toolName,
			toolArguments: string(event.ToolCall.Arguments),
		}
		m.items = append(m.items, item)
		m.activeTools[scopedID(source, event.ToolCall.ID)] = len(m.items) - 1
		m.status = scopedStatus(source, "tool "+event.ToolCall.Name)
	case agent.EventToolCompleted:
		m.finishTool(source, event)
	case agent.EventTurnCompleted:
		if event.Model != nil {
			if source == "" {
				m.runMetrics.add(*event.Model)
			} else {
				metrics := m.subagentMetrics[source]
				metrics.add(*event.Model)
				m.subagentMetrics[source] = metrics
			}
		}
		m.status = scopedStatus(source, "running")
	case agent.EventTurnFailed:
		if m.status != "canceling" {
			m.status = scopedStatus(source, "failed")
		}
	case agent.EventAgentCompleted:
		m.status = scopedStatus(source, "completed")
	case agent.EventAgentFailed:
		if m.status == "canceling" {
			return
		}
		m.status = scopedStatus(source, "failed")
		if event.Error != "" && !m.hasTerminalError(event.Error) {
			m.items = append(m.items, transcriptItem{
				kind:  itemError,
				label: "error",
				text:  scopedError(source, event.Error),
			})
		}
	}
}

func scopedStatus(subagent, status string) string {
	if subagent == "" {
		return status
	}
	return "subagent " + subagent + " " + status
}

func scopedError(subagent, message string) string {
	if subagent == "" {
		return message
	}
	return "subagent " + subagent + ": " + message
}

func scopedID(subagent, id string) string {
	return subagent + "\x00" + id
}

func messageReasoning(message agent.Message) string {
	var blocks []string
	for _, content := range message.Content {
		if content.Type == agent.ContentReasoning &&
			strings.TrimSpace(content.Text) != "" {
			blocks = append(blocks, content.Text)
		}
	}
	return strings.Join(blocks, "\n\n")
}

func (metrics *runMetrics) add(observation agent.ModelObservation) {
	firstTurn := metrics.turns == 0
	metrics.turns++
	metrics.modelDuration += observation.Duration
	if metrics.firstDelta == nil && observation.FirstDelta != nil {
		value := *observation.FirstDelta
		metrics.firstDelta = &value
	}

	if firstTurn {
		metrics.usageComplete = observation.Usage != nil
		metrics.usage = cloneObservedUsage(observation.Usage)
		return
	}
	if !metrics.usageComplete {
		return
	}
	if observation.Usage == nil {
		metrics.usage = nil
		metrics.usageComplete = false
		return
	}
	addObservedUsage(metrics.usage, observation.Usage)
}

func cloneObservedUsage(usage *agent.Usage) *agent.Usage {
	if usage == nil {
		return nil
	}
	cloned := *usage
	if usage.CachedInputTokens != nil {
		value := *usage.CachedInputTokens
		cloned.CachedInputTokens = &value
	}
	if usage.ReasoningTokens != nil {
		value := *usage.ReasoningTokens
		cloned.ReasoningTokens = &value
	}
	return &cloned
}

func addObservedUsage(total, value *agent.Usage) {
	total.InputTokens += value.InputTokens
	total.OutputTokens += value.OutputTokens
	total.TotalTokens += value.TotalTokens
	total.CachedInputTokens = addObservedTokenCount(
		total.CachedInputTokens,
		value.CachedInputTokens,
	)
	total.ReasoningTokens = addObservedTokenCount(
		total.ReasoningTokens,
		value.ReasoningTokens,
	)
}

func addObservedTokenCount(total, value *int64) *int64 {
	if total == nil || value == nil {
		return nil
	}
	sum := *total + *value
	return &sum
}

func (m *Model) finishMessageWithError(source, message string) {
	if activeMessage, active := m.activeMessages[source]; active {
		m.items[activeMessage].status = "failed"
		if m.items[activeMessage].text == "" {
			m.items[activeMessage].text = message
		}
		delete(m.activeMessages, source)
	}
	m.status = scopedStatus(source, "failed")
}

func (m *Model) finishTool(source string, event agent.Event) {
	if event.ToolCall == nil {
		return
	}
	key := scopedID(source, event.ToolCall.ID)
	index, ok := m.activeTools[key]
	if !ok {
		toolName := event.ToolCall.Name
		if source != "" {
			toolName = source + " › " + toolName
		}
		m.items = append(m.items, transcriptItem{
			kind:          itemTool,
			label:         "tool",
			id:            event.ToolCall.ID,
			toolName:      toolName,
			toolArguments: string(event.ToolCall.Arguments),
		})
		index = len(m.items) - 1
	}
	if event.IsError {
		m.items[index].status = "failed"
		m.items[index].toolError = event.Error
		m.items[index].toolOutput = event.Output
	} else {
		m.items[index].status = formatDuration(event.Duration)
		m.items[index].toolOutput = event.Output
	}
	delete(m.activeTools, key)
	m.status = scopedStatus(source, "running")
}

func (m Model) hasTerminalError(message string) bool {
	for index := len(m.items) - 1; index >= 0; index-- {
		if m.items[index].kind == itemError && m.items[index].text == message {
			return true
		}
	}
	return false
}

func (m *Model) resize() {
	contentWidth := max(m.width-4, 20)
	m.input.SetWidth(max(contentWidth-4, 12))
	viewportHeight := max(m.height-m.input.Height()-6, 1)
	m.viewport.SetWidth(contentWidth)
	m.viewport.SetHeight(viewportHeight)
}

func (m *Model) syncViewport() {
	offset := m.viewport.YOffset()
	wasAtBottom := m.viewport.AtBottom()
	m.ensureRendered()
	m.viewport.SetContent(m.renderTranscript())
	if m.followOutput || wasAtBottom {
		m.viewport.GotoBottom()
		m.followOutput = true
		return
	}
	m.viewport.SetYOffset(offset)
}

func (m *Model) ensureRendered() {
	for index := range m.items {
		item := &m.items[index]
		if item.kind != itemUser && item.kind != itemAssistant {
			continue
		}
		width := max(m.viewport.Width()-2, 20)
		if item.kind == itemUser {
			width = max(m.viewport.Width()-6, 20)
		}
		if item.renderWidth != width || (item.rendered == "" && item.text != "") {
			item.rendered = m.renderMarkdown(item.text, width)
			item.renderWidth = width
		}
		if item.reasoningWidth != width ||
			(item.reasoningView == "" && item.reasoning != "") {
			item.reasoningView = m.renderReasoning(item.reasoning, width)
			item.reasoningWidth = width
		}
	}
}

func (m *Model) applyBackground(isDark bool) {
	if m.isDark == isDark {
		return
	}
	m.isDark = isDark
	m.styles = newStyles(isDark)
	m.spinner.Style = m.styles.accent
	m.input.SetStyles(textarea.DefaultStyles(isDark))
	for index := range m.items {
		m.items[index].rendered = ""
		m.items[index].renderWidth = 0
		m.items[index].reasoningView = ""
		m.items[index].reasoningWidth = 0
	}
}

func compact(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

func formatDuration(duration time.Duration) string {
	if duration <= 0 {
		return "done"
	}
	if duration < time.Second {
		return fmt.Sprintf("%dms", duration.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", duration.Seconds())
}
