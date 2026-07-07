package agent

import (
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var (
	styleTitle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	styleDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleAnswer = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
)

// RenderMarkdown renders the model's markdown-formatted answer (headers,
// **bold**, code fences) into a colorized terminal string instead of
// showing the raw markdown syntax as literal text. width <= 0 uses a
// sensible default.
func RenderMarkdown(s string, width int) string {
	if width <= 0 {
		width = 100
	}
	r, err := glamour.NewTermRenderer(glamour.WithAutoStyle(), glamour.WithWordWrap(width))
	if err != nil {
		return s
	}
	out, err := r.Render(s)
	if err != nil {
		return s
	}
	return out
}
