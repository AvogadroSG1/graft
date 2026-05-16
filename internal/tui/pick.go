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
	Offset    int
	Width     int
	Height    int
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
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.Width = size.Width
		m.Height = size.Height
		return m.withCursorVisible(), nil
	}
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
		m = m.withCursorVisible()
	case "down", "j":
		if m.Cursor < len(m.Items)-1 {
			m.Cursor++
		}
		m = m.withCursorVisible()
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
	bodyLimit := m.bodyLineLimit()
	bodyRows := 0
	lastLibrary := ""
	for idx := m.Offset; idx < len(m.Items); idx++ {
		item := m.Items[idx]
		if item.Library != lastLibrary {
			if !appendWithinLimit(&rows, item.Library, bodyLimit, &bodyRows) {
				break
			}
			lastLibrary = item.Library
		}
		cursor := " "
		if idx == m.Cursor {
			cursor = ">"
		}
		check := "[ ]"
		if m.Selected[selectionKey(item)] {
			check = "[x]"
		}
		tags := ""
		if len(item.Entry.Tags) > 0 {
			tags = " [" + strings.Join(item.Entry.Tags, ",") + "]"
		}
		if !appendWithinLimit(&rows, fmt.Sprintf("%s %s %s%s %s: %s", cursor, check, item.Entry.Name, tags, item.Library, item.Entry.Description), bodyLimit, &bodyRows) {
			break
		}
	}
	return strings.Join(rows, "\n")
}

func appendWithinLimit(rows *[]string, row string, limit int, count *int) bool {
	if limit >= 0 && *count >= limit {
		return false
	}
	*rows = append(*rows, row)
	*count++
	return true
}

func (m PickModel) bodyLineLimit() int {
	if m.Height <= 0 {
		return -1
	}
	limit := m.Height - 1
	if limit < 0 {
		return 0
	}
	return limit
}

func (m PickModel) withCursorVisible() PickModel {
	if m.Cursor < m.Offset {
		m.Offset = m.Cursor
	}
	for m.Offset < m.Cursor && !m.cursorRendered() {
		m.Offset++
	}
	if m.Offset < 0 {
		m.Offset = 0
	}
	return m
}

func (m PickModel) cursorRendered() bool {
	if m.Height <= 0 {
		return true
	}
	bodyLimit := m.bodyLineLimit()
	bodyRows := 0
	lastLibrary := ""
	for idx := m.Offset; idx < len(m.Items); idx++ {
		item := m.Items[idx]
		if item.Library != lastLibrary {
			if bodyRows >= bodyLimit {
				return false
			}
			bodyRows++
			lastLibrary = item.Library
		}
		if bodyRows >= bodyLimit {
			return false
		}
		if idx == m.Cursor {
			return true
		}
		bodyRows++
	}
	return false
}

func selectionKey(item PickItem) string {
	return item.Library + "/" + item.Entry.Name
}
