package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/axelgar/opentree/pkg/config"
)

func updateAgentSelect(m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	agents := config.PredefinedAgents
	switch msg.String() {
	case "up", "k":
		if m.agentSel.cursor > 0 {
			m.agentSel.cursor--
		}
	case "down", "j":
		if m.agentSel.cursor < len(agents)-1 {
			m.agentSel.cursor++
		}
	case "enter":
		agent := agents[m.agentSel.cursor]
		m.cfg.Agent.Command = agent.Command
		if agent.Args != nil {
			m.cfg.Agent.Args = agent.Args
		} else {
			m.cfg.Agent.Args = []string{}
		}
		cfgPath := config.FindConfigFile()
		_ = config.Save(m.cfg, cfgPath)
		m.mode = ModeList
	case "esc", "q":
		m.mode = ModeList
	}
	return m, nil
}

func viewAgentSelect(m Model) string {
	var sb strings.Builder
	sb.WriteString(titleStyle.Render("Select Agent"))
	sb.WriteString("\n\n")
	for i, agent := range config.PredefinedAgents {
		cursor := "  "
		style := itemStyle
		if i == m.agentSel.cursor {
			cursor = "▶ "
			style = selectedItemStyle
		}

		name := agent.Name
		if agent.IsActive(m.cfg) {
			name += " (active)"
		}

		status := "not found"
		statusSt := lipgloss.NewStyle().Foreground(lipgloss.Color("#666"))
		if agent.IsInstalled() {
			status = "installed"
			statusSt = lipgloss.NewStyle().Foreground(lipgloss.Color("#2A9D8F"))
		}

		cmdStr := agent.Command
		if len(agent.Args) > 0 {
			cmdStr += " " + strings.Join(agent.Args, " ")
		}

		line := fmt.Sprintf("%s%-18s %-14s %s  %s",
			cursor, name, cmdStr, statusSt.Render(status), agent.Description)
		sb.WriteString(style.Render(line))
		sb.WriteString("\n")
	}
	sb.WriteString("\n")
	sb.WriteString(helpStyle.Render("↑/↓ navigate • Enter select • Esc cancel"))
	return appStyle.Render(sb.String())
}
