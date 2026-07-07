// Package dashboard implements `kubewhy watch`: a live terminal view of
// cluster health that runs the watcher continuously and, the moment
// something looks broken, kicks off the same read-only investigation loop
// used by one-shot questions -- automatically, without anyone having to ask.
package dashboard

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/didiberman/kubewhy/internal/agent"
	"github.com/didiberman/kubewhy/internal/tools"
	"github.com/didiberman/kubewhy/internal/watcher"
)

type Config struct {
	APIKey    string
	Model     string
	Namespace string
	Interval  time.Duration
}

var (
	styleHealthy  = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	styleWarning  = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow
	styleBroken   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")) // red
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleHeader   = lipgloss.NewStyle().Bold(true).Underline(true)
	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	styleSelected = lipgloss.NewStyle().Bold(true).Underline(true)
	styleDetail   = lipgloss.NewStyle().Foreground(lipgloss.Color("7")).PaddingLeft(6)
)

type investigation struct {
	status string // "running", "done", "error"
	answer string
	err    error
}

type snapshotMsg struct {
	pods []watcher.PodHealth
	err  error
}

type investigationMsg struct {
	key    string
	answer string
	err    error
}

type model struct {
	ctx       context.Context
	client    *tools.Client
	namespace string
	interval  time.Duration
	apiKey    string
	llmModel  string

	pods           map[string]watcher.PodHealth
	investigations map[string]*investigation
	lastErr        error
	polls          int

	selectedKey string
	expanded    bool
	width       int
}

func Run(ctx context.Context, cfg Config) error {
	client, err := tools.LoadClient()
	if err != nil {
		return err
	}
	m := model{
		ctx:            ctx,
		client:         client,
		namespace:      cfg.Namespace,
		interval:       cfg.Interval,
		apiKey:         cfg.APIKey,
		llmModel:       cfg.Model,
		pods:           map[string]watcher.PodHealth{},
		investigations: map[string]*investigation{},
		width:          100,
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

func (m model) Init() tea.Cmd {
	return pollCmd(m.ctx, m.client, m.namespace, 0)
}

func pollCmd(ctx context.Context, client *tools.Client, namespace string, wait time.Duration) tea.Cmd {
	return func() tea.Msg {
		if wait > 0 {
			time.Sleep(wait)
		}
		pods, err := watcher.Snapshot(ctx, client.Core(), namespace)
		return snapshotMsg{pods: pods, err: err}
	}
}

func investigateCmd(ctx context.Context, key, namespace, name, apiKey, llmModel string) tea.Cmd {
	return func() tea.Msg {
		question := fmt.Sprintf("why is pod %s in namespace %s unhealthy?", name, namespace)
		answer, err := agent.Investigate(ctx, question, apiKey, llmModel, agent.SilentReporter{})
		return investigationMsg{key: key, answer: answer, err: err}
	}
}

// brokenSorted returns the broken pods in the same stable order used for
// rendering, so selection indices line up with what's on screen.
func (m model) brokenSorted() []watcher.PodHealth {
	var broken []watcher.PodHealth
	for _, p := range m.pods {
		if p.Status == watcher.StatusBroken {
			broken = append(broken, p)
		}
	}
	sort.Slice(broken, func(i, j int) bool { return broken[i].Key() < broken[j].Key() })
	return broken
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.moveSelection(-1)
			m.expanded = false
		case "down", "j":
			m.moveSelection(1)
			m.expanded = false
		case "enter", " ":
			if m.selectedKey != "" {
				m.expanded = !m.expanded
			}
		case "esc":
			m.expanded = false
		}

	case snapshotMsg:
		m.polls++
		if msg.err != nil {
			m.lastErr = msg.err
			return m, pollCmd(m.ctx, m.client, m.namespace, m.interval)
		}
		m.lastErr = nil
		newPods := make(map[string]watcher.PodHealth, len(msg.pods))
		var cmds []tea.Cmd
		for _, p := range msg.pods {
			newPods[p.Key()] = p
			if p.Status == watcher.StatusBroken {
				if _, exists := m.investigations[p.Key()]; !exists {
					m.investigations[p.Key()] = &investigation{status: "running"}
					cmds = append(cmds, investigateCmd(m.ctx, p.Key(), p.Namespace, p.Name, m.apiKey, m.llmModel))
				}
			} else {
				delete(m.investigations, p.Key())
			}
		}
		m.pods = newPods

		// Keep selection valid; default to the first broken pod so a single
		// broken pod is immediately selectable without pressing a key first.
		stillBroken := false
		for _, p := range m.brokenSorted() {
			if p.Key() == m.selectedKey {
				stillBroken = true
				break
			}
		}
		if !stillBroken {
			m.expanded = false
			if broken := m.brokenSorted(); len(broken) > 0 {
				m.selectedKey = broken[0].Key()
			} else {
				m.selectedKey = ""
			}
		}

		cmds = append(cmds, pollCmd(m.ctx, m.client, m.namespace, m.interval))
		return m, tea.Batch(cmds...)

	case investigationMsg:
		if inv, ok := m.investigations[msg.key]; ok {
			if msg.err != nil {
				inv.status, inv.err = "error", msg.err
			} else {
				inv.status, inv.answer = "done", msg.answer
			}
		}
	}
	return m, nil
}

func (m *model) moveSelection(delta int) {
	broken := m.brokenSorted()
	if len(broken) == 0 {
		m.selectedKey = ""
		return
	}
	idx := 0
	for i, p := range broken {
		if p.Key() == m.selectedKey {
			idx = i
			break
		}
	}
	idx = (idx + delta + len(broken)) % len(broken)
	m.selectedKey = broken[idx].Key()
}

// summarize pulls out a one-line summary of the model's answer. Models
// often open with a filler sentence ("Perfect, I now have enough
// evidence...") before the actual root cause, so prefer a line that
// actually names the root cause over blindly taking the first line.
func summarize(s string) string {
	lines := strings.Split(s, "\n")
	best := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if best == "" {
			best = line
		}
		if strings.Contains(strings.ToLower(line), "root cause") {
			best = strings.TrimLeft(line, "#*- ")
			break
		}
	}
	const max = 100
	if len(best) > max {
		best = best[:max-1] + "…"
	}
	return best
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString(styleTitle.Render("kubewhy watch") + styleDim.Render("  ·  read-only  ·  ↑/↓ select  ·  enter expand  ·  q quit") + "\n\n")

	var healthy, warning []watcher.PodHealth
	broken := m.brokenSorted()
	for _, p := range m.pods {
		switch p.Status {
		case watcher.StatusWarning:
			warning = append(warning, p)
		case watcher.StatusBroken:
			// already collected via brokenSorted
		default:
			healthy = append(healthy, p)
		}
	}
	sort.Slice(warning, func(i, j int) bool { return warning[i].Key() < warning[j].Key() })

	if len(broken) > 0 {
		b.WriteString(styleHeader.Render("BROKEN") + "\n")
		for _, p := range broken {
			selected := p.Key() == m.selectedKey
			cursor := "  "
			if selected {
				cursor = "▶ "
			}
			line := fmt.Sprintf("%s✗ %s/%s", cursor, p.Namespace, p.Name)
			if selected {
				b.WriteString(styleSelected.Render(styleBroken.Render(line)))
			} else {
				b.WriteString(styleBroken.Render(line))
			}
			b.WriteString(styleDim.Render(fmt.Sprintf("  (%s, %d restarts)\n", p.Reason, p.Restarts)))

			inv, ok := m.investigations[p.Key()]
			if !ok {
				continue
			}
			switch inv.status {
			case "running":
				b.WriteString(styleDim.Render("      investigating...\n"))
			case "error":
				b.WriteString(styleDim.Render("      investigation failed: "+inv.err.Error()) + "\n")
			case "done":
				if selected && m.expanded {
					wrapWidth := m.width - 8
					if wrapWidth < 20 {
						wrapWidth = 20
					}
					b.WriteString(styleDetail.Width(wrapWidth).Render(inv.answer) + "\n")
				} else {
					suffix := ""
					if selected {
						suffix = styleDim.Render("  (enter to expand)")
					}
					b.WriteString("      " + summarize(inv.answer) + suffix + "\n")
				}
			}
		}
		b.WriteString("\n")
	}

	if len(warning) > 0 {
		b.WriteString(styleHeader.Render("WARNING") + "\n")
		for _, p := range warning {
			b.WriteString(styleWarning.Render(fmt.Sprintf("  ! %s/%s", p.Namespace, p.Name)))
			b.WriteString(styleDim.Render(fmt.Sprintf("  (%s)\n", p.Reason)))
		}
		b.WriteString("\n")
	}

	b.WriteString(styleHealthy.Render(fmt.Sprintf("✓ %d pod(s) healthy", len(healthy))) + "\n")

	if m.lastErr != nil {
		b.WriteString("\n" + styleBroken.Render("poll error: "+m.lastErr.Error()) + "\n")
	}

	return b.String()
}
