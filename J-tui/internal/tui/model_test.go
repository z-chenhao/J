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
)

func TestApplyEventTracksStreamingReasoningAndTools(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "")

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
	if !model.items[0].reasoning {
		t.Fatalf("assistant reasoning state was not retained: %#v", model.items[0])
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

func durationPointer(value time.Duration) *time.Duration {
	return &value
}

func int64Pointer(value int64) *int64 {
	return &value
}

func TestApplyEventRemovesEmptyToolCallMessage(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "")
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

func TestApplyEventShowsToolFailure(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "")
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
	model := New(context.Background(), nil, "ollama", "qwen", "")
	model.applyEvent(agent.Event{Type: agent.EventAgentFailed, Error: "boom"})
	model.applyEvent(agent.Event{Type: agent.EventAgentFailed, Error: "boom"})
	if len(model.items) != 1 {
		t.Fatalf("items = %#v", model.items)
	}
}

func TestRunDoneClassifiesCancellation(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "")
	model.running = true
	model.events = make(chan tea.Msg)
	updated, _ := model.Update(runDoneMsg{err: context.Canceled})
	result := updated.(Model)
	if result.running || result.status != "canceled" {
		t.Fatalf("running = %v, status = %q", result.running, result.status)
	}
}

func TestCancellationDoesNotRenderProviderFailure(t *testing.T) {
	model := New(context.Background(), nil, "ollama", "qwen", "")
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
	model := New(context.Background(), nil, "ollama", "qwen", "")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	rendered := updated.(Model).View()
	if rendered.MouseMode != tea.MouseModeNone {
		t.Fatalf("mouse mode=%v, want terminal-native selection", rendered.MouseMode)
	}
	view := rendered.Content
	for _, text := range []string{"ollama/qwen", "enter send", "ctrl+j newline", "ctrl+o tools", "╭"} {
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
		model := New(context.Background(), nil, "deepseek", "deepseek-v4-flash", "")
		model.items = append(model.items,
			transcriptItem{kind: itemUser, text: "render this message"},
			transcriptItem{kind: itemAssistant, text: "## Result\n\n- one\n- two"},
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
	model := New(context.Background(), nil, "ollama", "qwen", "")
	model.items = append(model.items, transcriptItem{
		kind:        itemAssistant,
		text:        "cached",
		rendered:    "old rendering",
		renderWidth: 40,
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
	if model.items[0].rendered != "" || model.items[0].renderWidth != 0 {
		t.Fatalf("cached rendering was not invalidated: %#v", model.items[0])
	}
}

func TestInputHeightGrowsWithContent(t *testing.T) {
	model := New(context.Background(), nil, "deepseek", "deepseek-v4-flash", "")
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
	model := New(context.Background(), nil, "ollama", "qwen", "")
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
	model := New(context.Background(), nil, "ollama", "qwen", "")
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
