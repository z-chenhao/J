package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type styles struct {
	accentColor   color.Color
	mutedColor    color.Color
	toolColor     color.Color
	errorColor    color.Color
	userBG        color.Color
	toolPendingBG color.Color
	toolSuccessBG color.Color
	toolErrorBG   color.Color

	accent   lipgloss.Style
	muted    lipgloss.Style
	tool     lipgloss.Style
	err      lipgloss.Style
	success  lipgloss.Style
	thinking lipgloss.Style
}

func newStyles(isDark bool) styles {
	lightDark := lipgloss.LightDark(isDark)
	result := styles{
		accentColor: lightDark(lipgloss.Color("#5A4FCF"), lipgloss.Color("#A99CFF")),
		mutedColor:  lightDark(lipgloss.Color("#6B7280"), lipgloss.Color("#777C85")),
		toolColor:   lightDark(lipgloss.Color("#8A5800"), lipgloss.Color("#E5B567")),
		errorColor:  lightDark(lipgloss.Color("#B42318"), lipgloss.Color("#FF7B72")),
		userBG:      lightDark(lipgloss.Color("#ECEEF4"), lipgloss.Color("#252832")),
		toolPendingBG: lightDark(
			lipgloss.Color("#FFF4D6"),
			lipgloss.Color("#332B1B"),
		),
		toolSuccessBG: lightDark(
			lipgloss.Color("#EAF7ED"),
			lipgloss.Color("#1E3025"),
		),
		toolErrorBG: lightDark(
			lipgloss.Color("#FDECEC"),
			lipgloss.Color("#3A2222"),
		),
	}
	successColor := lightDark(lipgloss.Color("#237A3B"), lipgloss.Color("#7EE787"))
	result.accent = lipgloss.NewStyle().Foreground(result.accentColor).Bold(true)
	result.muted = lipgloss.NewStyle().Foreground(result.mutedColor)
	result.tool = lipgloss.NewStyle().Foreground(result.toolColor).Bold(true)
	result.err = lipgloss.NewStyle().Foreground(result.errorColor)
	result.success = lipgloss.NewStyle().Foreground(successColor)
	result.thinking = lipgloss.NewStyle().Foreground(result.mutedColor).Italic(true)
	return result
}

// View renders a compact transcript, editor, and runtime footer.
func (m Model) View() tea.View {
	header := m.styles.accent.Render("J") + "  " +
		m.styles.muted.Render(safeTerminalText(m.provider+"/"+m.model))

	status := m.footer()
	if m.running {
		status = m.spinner.View() + " " + status
	}
	status = compact(status, max(m.width-4, 20))
	help := m.help()

	input := m.inputBorderStyle().Render(m.input.View())
	footer := lipgloss.JoinVertical(
		lipgloss.Left,
		m.styles.muted.Render(status),
		m.styles.muted.Render(help),
	)

	view := tea.NewView(lipgloss.JoinVertical(
		lipgloss.Left,
		lipgloss.NewStyle().Padding(0, 2).Render(header),
		lipgloss.NewStyle().Padding(1, 2, 0, 2).Render(m.viewport.View()),
		lipgloss.NewStyle().Padding(0, 2).Render(input),
		lipgloss.NewStyle().Padding(0, 2).Render(footer),
	))
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	return view
}

func (m Model) help() string {
	candidates := []string{
		"enter send  ctrl+j newline  pgup/pgdn scroll  ctrl+o tools  esc cancel  ctrl+c quit",
		"enter send  pgup/pgdn scroll  ctrl+o tools  esc cancel  ctrl+c quit",
		"enter send  pgup/pgdn scroll  ctrl+o tools  esc cancel",
		"enter send  ctrl+o tools  esc cancel",
	}
	if m.running {
		candidates = []string{
			"pgup/pgdn scroll  ctrl+o tools  esc cancel  ctrl+c quit",
			"pgup/pgdn scroll  ctrl+o tools  esc cancel",
			"ctrl+o tools  esc cancel",
		}
	}
	available := max(m.width-4, 20)
	for _, candidate := range candidates {
		if lipgloss.Width(candidate) <= available {
			return candidate
		}
	}
	return compact(candidates[len(candidates)-1], available)
}

func (m Model) renderTranscript() string {
	if len(m.items) == 0 {
		return m.styles.muted.Render("One conversation. Events stay visible as the runtime advances.")
	}

	blocks := make([]string, 0, len(m.items))
	for _, item := range m.items {
		switch item.kind {
		case itemUser:
			content := item.rendered
			if content == "" {
				content = item.text
			}
			blocks = append(blocks, lipgloss.NewStyle().
				Background(m.styles.userBG).
				Padding(0, 1).
				Width(max(m.viewport.Width()-4, 1)).
				Render(content))
		case itemAssistant:
			var sections []string
			if item.reasoning {
				sections = append(sections, m.styles.thinking.Render("Thinking…"))
			}
			if strings.TrimSpace(item.text) != "" {
				content := item.rendered
				if content == "" {
					content = item.text
				}
				sections = append(sections, content)
			}
			if len(sections) == 0 {
				sections = append(sections, m.styles.muted.Render("…"))
			}
			blocks = append(blocks, strings.Join(sections, "\n\n"))
		case itemTool:
			blocks = append(blocks, m.renderTool(item))
		case itemError:
			blocks = append(blocks, m.styles.err.Render("Error: "+safeTerminalText(item.text)))
		}
	}
	return strings.Join(blocks, "\n\n")
}

func (m Model) renderMarkdown(text string, width int) string {
	text = safeTerminalText(text)
	markdownStyle := "light"
	if m.isDark {
		markdownStyle = "dark"
	}
	renderer, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle(markdownStyle),
		glamour.WithWordWrap(max(width, 20)),
	)
	if err != nil {
		return text
	}
	rendered, err := renderer.Render(text)
	if err != nil {
		return text
	}
	return strings.Trim(rendered, "\n")
}

func (m Model) renderTool(item transcriptItem) string {
	background := m.styles.toolSuccessBG
	status := item.status
	statusStyle := m.styles.success
	switch status {
	case "running", "preparing":
		background = m.styles.toolPendingBG
		statusStyle = m.styles.tool
	case "failed":
		background = m.styles.toolErrorBG
		statusStyle = m.styles.err
	}
	if status == "" {
		status = "done"
	}

	name := safeTerminalText(item.toolName)
	if name == "" {
		name = "unknown"
	}
	title := m.styles.tool.Render(name) + "  " + statusStyle.Render(status)
	arguments := prettyJSON(safeTerminalText(item.toolArguments))
	result := safeTerminalText(item.toolOutput)
	toolError := safeTerminalText(item.toolError)

	lines := []string{title}
	if m.toolsExpanded {
		if arguments != "" {
			lines = append(lines, m.styles.muted.Render("arguments"), arguments)
		}
		if result != "" {
			lines = append(lines, m.styles.muted.Render("output"), strings.TrimRight(result, "\n"))
		}
		if toolError != "" {
			lines = append(lines, m.styles.muted.Render("error"), strings.TrimRight(toolError, "\n"))
		}
	} else {
		if arguments != "" {
			lines = append(lines, m.styles.muted.Render(compact(arguments, max(m.viewport.Width()-12, 24))))
		}
		if result != "" {
			lines = append(lines, "↳ "+compact(result, max(m.viewport.Width()-10, 24)))
		} else if toolError != "" {
			lines = append(lines, "↳ "+compact(toolError, max(m.viewport.Width()-10, 24)))
		}
	}

	return lipgloss.NewStyle().
		Background(background).
		Padding(0, 1).
		Width(max(m.viewport.Width()-4, 1)).
		Render(strings.Join(lines, "\n"))
}

func (m Model) inputBorderStyle() lipgloss.Style {
	borderColor := m.styles.mutedColor
	switch {
	case m.status == "failed":
		borderColor = m.styles.errorColor
	case strings.HasPrefix(m.status, "tool") || m.status == "preparing tool":
		borderColor = m.styles.toolColor
	case m.running:
		borderColor = m.styles.accentColor
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1)
}

func (m Model) footer() string {
	parts := []string{m.status}
	if observation := m.lastModel; observation != nil {
		parts = append(parts, formatDuration(observation.Duration))
		if observation.FirstDelta != nil {
			parts = append(parts, "ttft "+formatDuration(*observation.FirstDelta))
		}
		if observation.Usage != nil {
			parts = append(parts, fmt.Sprintf("%d tokens", observation.Usage.TotalTokens))
			if cached := observation.Usage.CachedInputTokens; cached != nil {
				missed := max(observation.Usage.InputTokens-*cached, 0)
				parts = append(
					parts,
					fmt.Sprintf("cache %d hit / %d miss", *cached, missed),
				)
			}
		}
	}
	return strings.Join(parts, "  ·  ")
}

func prettyJSON(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !json.Valid([]byte(value)) {
		return value
	}
	var output bytes.Buffer
	if err := json.Indent(&output, []byte(value), "", "  "); err != nil {
		return value
	}
	return output.String()
}

func safeTerminalText(value string) string {
	value = ansi.Strip(value)
	return strings.Map(func(char rune) rune {
		if char == '\t' || char == '\n' || !unicode.IsControl(char) {
			return char
		}
		return -1
	}, value)
}
