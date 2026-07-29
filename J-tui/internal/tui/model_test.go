package tui

import (
	"context"
	"image/color"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/z-chenhao/J/J-agent/agent"
	"github.com/z-chenhao/J/J-tui/internal/observe"
)

func TestApplyEventTracksStreamingReasoningAndTools(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "", "")

	model.applyEvent(agent.Event{Type: agent.EventAgentStarted})
	model.applyEvent(agent.Event{Type: agent.EventTurnStarted})
	model.applyEvent(agent.Event{Type: agent.EventMessageStarted})
	model.applyEvent(agent.Event{
		Type:  agent.EventMessageDelta,
		Delta: &agent.ModelDelta{Type: agent.DeltaReasoning, Delta: "consider"},
	})
	model.applyEvent(agent.Event{
		Type:  agent.EventMessageDelta,
		Delta: &agent.ModelDelta{Type: agent.DeltaText, Delta: "I will check."},
	})
	assistant := agent.TextMessage(agent.RoleAssistant, "I will check.")
	model.applyEvent(agent.Event{Type: agent.EventMessageCompleted, Message: &assistant})

	call := agent.ToolCall{ID: "call-1", Name: "probe"}
	model.applyEvent(agent.Event{Type: agent.EventToolStarted, ToolCall: &call})
	model.applyEvent(agent.Event{
		Type:     agent.EventToolCompleted,
		ToolCall: &call,
		Output:   "tool output",
		Duration: 12 * time.Millisecond,
	})
	observation := agent.ModelObservation{
		Provider:   "ollama",
		Model:      "qwen",
		Duration:   40 * time.Millisecond,
		FirstDelta: durationPointer(7 * time.Millisecond),
		Usage: &agent.Usage{
			InputTokens:       8,
			OutputTokens:      1,
			TotalTokens:       9,
			CachedInputTokens: int64Pointer(3),
		},
	}
	model.applyEvent(agent.Event{Type: agent.EventTurnCompleted, Model: &observation})
	model.applyEvent(agent.Event{Type: agent.EventAgentCompleted})

	if model.reasoningBytes != len("consider") {
		t.Fatalf("reasoning bytes = %d", model.reasoningBytes)
	}
	if len(model.items) != 2 {
		t.Fatalf("items = %#v", model.items)
	}
	if model.items[0].kind != itemAssistant || model.items[0].text != "I will check." {
		t.Fatalf("assistant item = %#v", model.items[0])
	}
	if model.items[0].reasoning != "consider" {
		t.Fatalf("assistant reasoning was not retained: %#v", model.items[0])
	}
	if model.items[1].kind != itemTool || model.items[1].status != "12ms" ||
		model.items[1].toolOutput != "tool output" {
		t.Fatalf("tool item = %#v", model.items[1])
	}
	if footer := model.footer(); !strings.Contains(footer, "9 tokens") {
		t.Fatalf("footer = %q", footer)
	}
	if footer := model.footer(); !strings.Contains(footer, "ttft 7ms") {
		t.Fatalf("footer = %q", footer)
	}
	if footer := model.footer(); !strings.Contains(footer, "cache 3 hit / 5 miss") {
		t.Fatalf("footer = %q", footer)
	}
}

func TestApplyObservedEventKeepsSubagentOutputAndUsageDistinct(t *testing.T) {
	model := New(context.Background(), nil, "openai", "root", "", "")
	rootCached := int64(4)
	model.applyEvent(agent.Event{
		Type: agent.EventTurnCompleted,
		Model: &agent.ModelObservation{Usage: &agent.Usage{
			InputTokens:       8,
			OutputTokens:      2,
			TotalTokens:       10,
			CachedInputTokens: &rootCached,
		}},
	})

	source := "research"
	model.applyObservedEvent(observe.Event{
		Subagent: source,
		Runtime:  agent.Event{Type: agent.EventMessageStarted},
	})
	model.applyObservedEvent(observe.Event{
		Subagent: source,
		Runtime: agent.Event{
			Type: agent.EventMessageDelta,
			Delta: &agent.ModelDelta{
				Type:  agent.DeltaReasoning,
				Delta: "inspect sources",
			},
		},
	})
	model.applyObservedEvent(observe.Event{
		Subagent: source,
		Runtime: agent.Event{
			Type:  agent.EventMessageDelta,
			Delta: &agent.ModelDelta{Type: agent.DeltaText, Delta: "found it"},
		},
	})
	message := agent.TextMessage(agent.RoleAssistant, "found it")
	model.applyObservedEvent(observe.Event{
		Subagent: source,
		Runtime: agent.Event{
			Type:    agent.EventMessageCompleted,
			Message: &message,
		},
	})
	call := agent.ToolCall{ID: "same-id", Name: "tavily_search"}
	model.applyObservedEvent(observe.Event{
		Subagent: source,
		Runtime: agent.Event{
			Type:     agent.EventToolStarted,
			ToolCall: &call,
		},
	})
	model.applyObservedEvent(observe.Event{
		Subagent: source,
		Runtime: agent.Event{
			Type:     agent.EventToolCompleted,
			ToolCall: &call,
			Output:   "source",
		},
	})
	model.applyObservedEvent(observe.Event{
		Subagent: source,
		Runtime: agent.Event{
			Type: agent.EventTurnCompleted,
			Model: &agent.ModelObservation{Usage: &agent.Usage{
				InputTokens:  3,
				OutputTokens: 1,
				TotalTokens:  4,
			}},
		},
	})

	if len(model.items) != 2 {
		t.Fatalf("items=%#v", model.items)
	}
	if got := model.items[0]; got.label != source ||
		got.reasoning != "inspect sources" || got.text != "found it" {
		t.Fatalf("subagent message=%#v", got)
	}
	if got := model.items[1]; got.toolName != "research › tavily_search" ||
		got.toolOutput != "source" {
		t.Fatalf("subagent tool=%#v", got)
	}
	footer := model.footer()
	if !strings.Contains(footer, "10 tokens") ||
		!strings.Contains(footer, "cache 4 hit / 4 miss") ||
		!strings.Contains(footer, "subagents 1 turns / 4 tokens") {
		t.Fatalf("footer=%q", footer)
	}
}

func TestFooterAggregatesCompleteRunUsage(t *testing.T) {
	model := New(context.Background(), nil, "deepseek", "deepseek-chat", "", "")
	firstCached := int64(7)
	secondCached := int64(12)
	firstDelta := 5 * time.Millisecond
	secondDelta := 3 * time.Millisecond
	model.applyEvent(agent.Event{
		Type: agent.EventTurnCompleted,
		Model: &agent.ModelObservation{
			Duration:   20 * time.Millisecond,
			FirstDelta: &firstDelta,
			Usage: &agent.Usage{
				InputTokens:       10,
				OutputTokens:      2,
				TotalTokens:       12,
				CachedInputTokens: &firstCached,
			},
		},
	})
	model.applyEvent(agent.Event{
		Type: agent.EventTurnCompleted,
		Model: &agent.ModelObservation{
			Duration:   30 * time.Millisecond,
			FirstDelta: &secondDelta,
			Usage: &agent.Usage{
				InputTokens:       20,
				OutputTokens:      3,
				TotalTokens:       23,
				CachedInputTokens: &secondCached,
			},
		},
	})

	footer := model.footer()
	for _, expected := range []string{
		"50ms",
		"ttft 5ms",
		"35 tokens",
		"cache 19 hit / 11 miss",
	} {
		if !strings.Contains(footer, expected) {
			t.Fatalf("footer does not contain %q: %q", expected, footer)
		}
	}
}

func TestFooterDoesNotPresentPartialCacheBreakdown(t *testing.T) {
	model := New(context.Background(), nil, "deepseek", "deepseek-chat", "", "")
	cached := int64(7)
	model.applyEvent(agent.Event{
		Type: agent.EventTurnCompleted,
		Model: &agent.ModelObservation{Usage: &agent.Usage{
			InputTokens:       10,
			OutputTokens:      2,
			TotalTokens:       12,
			CachedInputTokens: &cached,
		}},
	})
	model.applyEvent(agent.Event{
		Type: agent.EventTurnCompleted,
		Model: &agent.ModelObservation{Usage: &agent.Usage{
			InputTokens:  20,
			OutputTokens: 3,
			TotalTokens:  23,
		}},
	})

	footer := model.footer()
	if !strings.Contains(footer, "35 tokens") || strings.Contains(footer, "cache ") {
		t.Fatalf("footer presents partial cache usage: %q", footer)
	}
}

func TestFooterDoesNotPresentPartialRunUsage(t *testing.T) {
	model := New(context.Background(), nil, "deepseek", "deepseek-chat", "", "")
	model.applyEvent(agent.Event{
		Type: agent.EventTurnCompleted,
		Model: &agent.ModelObservation{Usage: &agent.Usage{
			InputTokens:  10,
			OutputTokens: 2,
			TotalTokens:  12,
		}},
	})
	model.applyEvent(agent.Event{
		Type:  agent.EventTurnCompleted,
		Model: &agent.ModelObservation{},
	})

	if footer := model.footer(); strings.Contains(footer, "tokens") {
		t.Fatalf("footer presents partial run usage: %q", footer)
	}
}

func durationPointer(value time.Duration) *time.Duration {
	return &value
}

func int64Pointer(value int64) *int64 {
	return &value
}

func TestApplyEventRemovesEmptyToolCallMessage(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "", "")
	model.applyEvent(agent.Event{Type: agent.EventMessageStarted})
	message := agent.Message{
		Role: agent.RoleAssistant,
		Content: []agent.Content{{
			Type: agent.ContentToolCall,
			ToolCall: &agent.ToolCall{
				ID: "call-1", Name: "probe",
			},
		}},
	}
	model.applyEvent(agent.Event{Type: agent.EventMessageCompleted, Message: &message})
	if len(model.items) != 0 {
		t.Fatalf("items = %#v", model.items)
	}
}

func TestApplyEventRetainsCompletedReasoningWithoutDeltas(t *testing.T) {
	model := New(context.Background(), nil, "openai", "non-streaming", "", "")
	model.applyEvent(agent.Event{Type: agent.EventMessageStarted})
	message := agent.Message{
		Role: agent.RoleAssistant,
		Content: []agent.Content{
			{Type: agent.ContentReasoning, Text: "Inspect the evidence."},
			{Type: agent.ContentText, Text: "Complete."},
		},
	}
	model.applyEvent(agent.Event{Type: agent.EventMessageCompleted, Message: &message})
	if len(model.items) != 1 ||
		model.items[0].reasoning != "Inspect the evidence." ||
		model.items[0].text != "Complete." {
		t.Fatalf("items=%#v", model.items)
	}
}

func TestApplyEventShowsToolFailure(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "", "")
	call := agent.ToolCall{ID: "call-1", Name: "probe"}
	model.applyEvent(agent.Event{Type: agent.EventToolStarted, ToolCall: &call})
	model.applyEvent(agent.Event{
		Type:     agent.EventToolCompleted,
		ToolCall: &call,
		IsError:  true,
		Error:    "probe failed",
	})
	if len(model.items) != 1 || model.items[0].status != "failed" ||
		model.items[0].toolError != "probe failed" {
		t.Fatalf("items = %#v", model.items)
	}
}

func TestApplyEventShowsTerminalFailureOnce(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "", "")
	model.applyEvent(agent.Event{Type: agent.EventAgentFailed, Error: "boom"})
	model.applyEvent(agent.Event{Type: agent.EventAgentFailed, Error: "boom"})
	if len(model.items) != 1 {
		t.Fatalf("items = %#v", model.items)
	}
}

func TestRunDoneClassifiesCancellation(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "", "")
	model.running = true
	model.events = make(chan tea.Msg)
	updated, _ := model.Update(runDoneMsg{err: context.Canceled})
	result := updated.(Model)
	if result.running || result.status != "canceled" {
		t.Fatalf("running = %v, status = %q", result.running, result.status)
	}
}

func TestCancellationDoesNotRenderProviderFailure(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "", "")
	model.status = "canceling"
	model.applyEvent(agent.Event{
		Type:  agent.EventAgentFailed,
		Error: `call ollama: context canceled`,
	})
	if len(model.items) != 0 || model.status != "canceling" {
		t.Fatalf("items = %#v, status = %q", model.items, model.status)
	}
}

func TestViewIncludesModelAndControls(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "", "session-123")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	rendered := updated.(Model).View()
	if rendered.MouseMode != tea.MouseModeNone {
		t.Fatalf("mouse mode=%v, want terminal-native selection", rendered.MouseMode)
	}
	view := rendered.Content
	for _, text := range []string{
		"ollama/qwen",
		"session session-123",
		"enter send",
		"alt+↑/↓ scroll",
		"ctrl+t thinking",
		"ctrl+o tools",
		"╭",
	} {
		if !strings.Contains(view, text) {
			t.Fatalf("view does not contain %q:\n%s", text, view)
		}
	}
	if prompts := strings.Count(ansi.Strip(view), "›"); prompts != 1 {
		t.Fatalf("empty editor prompts = %d:\n%s", prompts, view)
	}
}

func TestViewFitsTerminal(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{
		{Width: 48, Height: 18},
		{Width: 80, Height: 24},
		{Width: 120, Height: 36},
	} {
		model := New(context.Background(), nil, "deepseek", "deepseek-v4-flash", "", "")
		model.items = append(model.items,
			transcriptItem{kind: itemUser, text: "render this message"},
			transcriptItem{
				kind:      itemAssistant,
				reasoning: strings.Repeat("inspect evidence carefully ", 8),
				text:      "## Result\n\n- one\n- two",
			},
			transcriptItem{
				kind:          itemTool,
				toolName:      "probe",
				toolArguments: `{"value":"ok"}`,
				toolOutput:    "complete",
				status:        "12ms",
			},
		)
		updated, _ := model.Update(size)
		view := updated.(Model).View().Content
		if height := lipgloss.Height(view); height > size.Height {
			t.Fatalf("%dx%d view height = %d", size.Width, size.Height, height)
		}
		for lineNumber, line := range strings.Split(view, "\n") {
			if width := lipgloss.Width(line); width > size.Width {
				t.Fatalf(
					"%dx%d line %d width = %d:\n%s",
					size.Width,
					size.Height,
					lineNumber+1,
					width,
					view,
				)
			}
		}
	}
}

func TestBackgroundColorSelectsStylesWithoutRendererProbe(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "", "")
	model.items = append(model.items, transcriptItem{
		kind:           itemAssistant,
		text:           "cached",
		reasoning:      "inspect",
		rendered:       "old rendering",
		renderWidth:    40,
		reasoningView:  "old reasoning",
		reasoningWidth: 40,
	})
	darkAccent := model.styles.accentColor

	model.applyBackground(tea.BackgroundColorMsg{
		Color: color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff},
	}.IsDark())

	if model.isDark {
		t.Fatal("light terminal background was classified as dark")
	}
	if model.styles.accentColor == darkAccent {
		t.Fatal("light terminal background retained the dark accent")
	}
	if model.items[0].rendered != "" || model.items[0].renderWidth != 0 ||
		model.items[0].reasoningView != "" || model.items[0].reasoningWidth != 0 {
		t.Fatalf("cached rendering was not invalidated: %#v", model.items[0])
	}
}

func TestReasoningIsVisibleByDefaultAndCtrlTTogglesIt(t *testing.T) {
	model := New(context.Background(), nil, "deepseek", "deepseek-chat", "", "")
	model.items = append(model.items, transcriptItem{
		kind:      itemAssistant,
		reasoning: "First inspect the available evidence.",
		text:      "Final answer.",
	})
	model.syncViewport()

	expanded := ansi.Strip(model.renderTranscript())
	if !strings.Contains(expanded, "First inspect the available evidence.") ||
		!strings.Contains(expanded, "Final answer.") ||
		strings.Contains(expanded, "Thinking…") {
		t.Fatalf("expanded transcript:\n%s", expanded)
	}

	updated, _ := model.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	model = updated.(Model)
	collapsed := ansi.Strip(model.renderTranscript())
	if strings.Contains(collapsed, "First inspect the available evidence.") ||
		!strings.Contains(collapsed, "Thinking…") ||
		!strings.Contains(collapsed, "Final answer.") {
		t.Fatalf("collapsed transcript:\n%s", collapsed)
	}

	updated, _ = model.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	model = updated.(Model)
	if expandedAgain := ansi.Strip(model.renderTranscript()); !strings.Contains(
		expandedAgain,
		"First inspect the available evidence.",
	) {
		t.Fatalf("re-expanded transcript:\n%s", expandedAgain)
	}
}

func TestInputHeightGrowsWithContent(t *testing.T) {
	model := New(context.Background(), nil, "deepseek", "deepseek-v4-flash", "", "")
	if model.input.Height() != 1 {
		t.Fatalf("empty input height = %d", model.input.Height())
	}

	model.input.SetValue("one\ntwo\nthree")
	model.resize()
	if model.input.Height() != 3 {
		t.Fatalf("multiline input height = %d", model.input.Height())
	}

	model.input.SetValue(strings.Repeat("宽", 1000))
	model.resize()
	if model.input.Height() != maxInputHeight {
		t.Fatalf("large input height = %d", model.input.Height())
	}

	model.input.Reset()
	model.resize()
	if model.input.Height() != 1 {
		t.Fatalf("reset input height = %d", model.input.Height())
	}
}

func TestMarkdownAndToolRendering(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "", "")
	rendered := model.renderMarkdown("**bold**\n\n```go\nfmt.Println(\"ok\")\n```", 60)
	plain := ansi.Strip(rendered)
	if !strings.Contains(plain, "bold") ||
		!strings.Contains(plain, "fmt.Println") {
		t.Fatalf("unexpected markdown output:\n%s", rendered)
	}

	item := transcriptItem{
		kind:          itemTool,
		toolName:      "probe",
		toolArguments: `{"value":"ok"}`,
		toolOutput:    "first line\nsecond line",
		status:        "12ms",
	}
	collapsed := model.renderTool(item)
	if !strings.Contains(collapsed, "probe") || !strings.Contains(collapsed, "first line second line") {
		t.Fatalf("collapsed tool:\n%s", collapsed)
	}
	model.toolsExpanded = true
	expanded := model.renderTool(item)
	if !strings.Contains(expanded, `"value": "ok"`) ||
		!strings.Contains(expanded, "first line") ||
		!strings.Contains(expanded, "second line") {
		t.Fatalf("expanded tool:\n%s", expanded)
	}

	failed := model.renderTool(transcriptItem{
		kind:       itemTool,
		toolName:   "bash",
		toolOutput: "partial output",
		toolError:  "exit status 1",
		status:     "failed",
	})
	if !strings.Contains(failed, "partial output") || !strings.Contains(failed, "exit status 1") {
		t.Fatalf("failed tool omitted output or error:\n%s", failed)
	}
}

func TestTerminalTextRemovesControlSequences(t *testing.T) {
	input := "\x1b]11;rgb:2828/2c2c/3434\x07visible\x1b[31m red\x1b[0m\u009b"
	output := safeTerminalText(input)
	if output != "visible red" {
		t.Fatalf("safeTerminalText()=%q", output)
	}
}

func TestSyncViewportPreservesManualScroll(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "", "")
	model.width = 80
	model.height = 12
	model.resize()
	for index := 0; index < 20; index++ {
		model.items = append(model.items, transcriptItem{
			kind: itemUser,
			text: "message with enough content to occupy one line",
		})
	}
	model.syncViewport()
	if !model.viewport.AtBottom() {
		t.Fatal("viewport did not initially follow output")
	}

	model.viewport.GotoTop()
	model.followOutput = false
	model.items = append(model.items, transcriptItem{kind: itemAssistant, text: "new output"})
	model.syncViewport()
	if model.viewport.YOffset() != 0 {
		t.Fatalf("manual scroll moved to %d", model.viewport.YOffset())
	}
}

func TestTranscriptScrollKeysUseViewportKeyMap(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "", "")
	model.viewport.SetHeight(4)
	model.viewport.SetContent(strings.Repeat("line\n", 20))
	model.viewport.GotoBottom()
	bottom := model.viewport.YOffset()

	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt})
	model = updated.(Model)
	if model.viewport.YOffset() != bottom-1 {
		t.Fatalf("alt+up offset=%d, want %d", model.viewport.YOffset(), bottom-1)
	}
	if model.followOutput {
		t.Fatal("alt+up should stop following output")
	}

	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModAlt})
	model = updated.(Model)
	if model.viewport.YOffset() != bottom {
		t.Fatalf("alt+down offset=%d, want %d", model.viewport.YOffset(), bottom)
	}
	if !model.followOutput {
		t.Fatal("alt+down at bottom should resume following output")
	}
}

func TestEditorRetainsCtrlUDeleteBinding(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "", "")
	model.input.SetValue("draft")

	updated, _ := model.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	model = updated.(Model)
	if model.input.Value() != "" {
		t.Fatalf("ctrl+u left input=%q", model.input.Value())
	}
}
