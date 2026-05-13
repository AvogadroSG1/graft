// Package tui provides the interactive Bubbletea model for "graft pick".
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/poconnor/graft/internal/model"
)

// PickModel is the Bubbletea model for the interactive MCP picker.
// Navigate with arrow keys or j/k; toggle selection with space; confirm with enter; quit with q or ctrl+c.
type PickModel struct {
	Items    []model.IndexEntry
	Selected map[string]bool
	Cursor   int
	Done     bool
}

// NewPickModel creates a PickModel with items pre-populated and the names in selected
// already checked.
func NewPickModel(items []model.IndexEntry, selected []string) PickModel {
	state := map[string]bool{}
	for _, name := range selected {
		state[name] = true
	}
	return PickModel{Items: items, Selected: state}
}

func (m PickModel) Init() tea.Cmd {
	return nil
}

func (m PickModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "ctrl+c", "q":
		m.Done = true
		return m, tea.Quit
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
		}
	case "down", "j":
		if m.Cursor < len(m.Items)-1 {
			m.Cursor++
		}
	case " ":
		if len(m.Items) > 0 {
			name := m.Items[m.Cursor].Name
			m.Selected[name] = !m.Selected[name]
		}
	case "enter":
		m.Done = true
		return m, tea.Quit
	}
	return m, nil
}

func (m PickModel) View() string {
	title := lipgloss.NewStyle().Bold(true).Render("Select MCPs")
	rows := []string{title}
	for idx, item := range m.Items {
		cursor := " "
		if idx == m.Cursor {
			cursor = ">"
		}
		check := "[ ]"
		if m.Selected[item.Name] {
			check = "[x]"
		}
		rows = append(rows, fmt.Sprintf("%s %s %s %s", cursor, check, item.Name, item.Description))
	}
	return strings.Join(rows, "\n")
}
