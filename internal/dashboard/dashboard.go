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
	styleHealthy = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))  // green
	styleWarning = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow
	styleBroken  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("1")) // red
	styleDim     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleHeader  = lipgloss.NewStyle().Bold(true).Underline(true)
	styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
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

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "q" || msg.String() == "ctrl+c" {
			return m, tea.Quit
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
	b.WriteString(styleTitle.Render("kubewhy watch") + styleDim.Render("  ·  read-only  ·  press q to quit") + "\n\n")

	var healthy, warning, broken []watcher.PodHealth
	for _, p := range m.pods {
		switch p.Status {
		case watcher.StatusBroken:
			broken = append(broken, p)
		case watcher.StatusWarning:
			warning = append(warning, p)
		default:
			healthy = append(healthy, p)
		}
	}
	sortPods := func(s []watcher.PodHealth) {
		sort.Slice(s, func(i, j int) bool { return s[i].Key() < s[j].Key() })
	}
	sortPods(broken)
	sortPods(warning)

	if len(broken) > 0 {
		b.WriteString(styleHeader.Render("BROKEN") + "\n")
		for _, p := range broken {
			b.WriteString(styleBroken.Render(fmt.Sprintf("  ✗ %s/%s", p.Namespace, p.Name)))
			b.WriteString(styleDim.Render(fmt.Sprintf("  (%s, %d restarts)\n", p.Reason, p.Restarts)))
			if inv, ok := m.investigations[p.Key()]; ok {
				switch inv.status {
				case "running":
					b.WriteString(styleDim.Render("      investigating...\n"))
				case "error":
					b.WriteString(styleDim.Render("      investigation failed: "+inv.err.Error()) + "\n")
				case "done":
					b.WriteString("      " + summarize(inv.answer) + "\n")
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
