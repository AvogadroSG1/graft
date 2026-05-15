// Package tui provides the interactive Bubbletea model for "graft pick".
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/poconnor/graft/internal/model"
)

// PickItem carries an indexed MCP plus the library it came from.
type PickItem struct {
	Entry   model.IndexEntry
	Library string
}

// PickModel is the Bubbletea model for the interactive MCP picker.
// Navigate with arrow keys or j/k; toggle selection with space; confirm with enter; quit with q or ctrl+c.
type PickModel struct {
	Items     []PickItem
	Selected  map[string]bool
	Cursor    int
	Done      bool
	Confirmed bool
}

// NewPickModel creates a PickModel with items pre-populated and composite
// library/name keys in selected already checked.
func NewPickModel(items []PickItem, selected []string) PickModel {
	state := map[string]bool{}
	for _, key := range selected {
		state[key] = true
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
		m.Confirmed = false
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
			if m.Selected == nil {
				m.Selected = map[string]bool{}
			}
			key := selectionKey(m.Items[m.Cursor])
			m.Selected[key] = !m.Selected[key]
		}
	case "enter":
		m.Done = true
		m.Confirmed = true
		return m, tea.Quit
	}
	return m, nil
}

// Results returns the confirmed selected items. Quit/cancel returns no results.
func (m PickModel) Results() []PickItem {
	if !m.Confirmed {
		return []PickItem{}
	}
	results := []PickItem{}
	for _, item := range m.Items {
		if m.Selected[selectionKey(item)] {
			results = append(results, item)
		}
	}
	return results
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
		if m.Selected[selectionKey(item)] {
			check = "[x]"
		}
		rows = append(rows, fmt.Sprintf("%s %s %s %s: %s", cursor, check, item.Entry.Name, item.Library, item.Entry.Description))
	}
	return strings.Join(rows, "\n")
}

func selectionKey(item PickItem) string {
	return item.Library + "/" + item.Entry.Name
}
