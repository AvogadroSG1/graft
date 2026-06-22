package tui

import (
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/poconnor/graft/internal/model"
)

// validVarName matches a standard environment-variable name.
var validVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// PlaceholderItem describes a single ${...} placeholder that needs resolving for
// a newly-installed MCP.
type PlaceholderItem struct {
	MCP        string // mcp name
	Scope      string // "env" or "header"
	Key        string // the env/header key, e.g. "GITHUB_TOKEN"
	DefaultVar string // default reference name inside ${...}, e.g. "GITHUB_TOKEN"
}

// PlaceholderModel is the Bubbletea model that walks the user through each
// placeholder token. For each item the user types a new reference name (becoming
// ${NAME}) or hits enter to keep the default ${DefaultVar}. Cancel with esc or
// ctrl+c. The text input is hand-rolled to avoid a charmbracelet/bubbles
// dependency, matching the style of PickModel.
type PlaceholderModel struct {
	Items     []PlaceholderItem
	Index     int
	Buffer    string
	Values    map[int]string // resolved ${...} value per item index
	Done      bool
	Confirmed bool
	errMsg    string
}

// NewPlaceholderModel creates a PlaceholderModel for the given items.
func NewPlaceholderModel(items []PlaceholderItem) PlaceholderModel {
	return PlaceholderModel{Items: items, Values: map[int]string{}}
}

func (m PlaceholderModel) Init() tea.Cmd {
	return nil
}

func (m PlaceholderModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		m.Done = true
		m.Confirmed = false
		return m, tea.Quit
	case tea.KeyBackspace, tea.KeyDelete:
		if m.Buffer != "" {
			runes := []rune(m.Buffer)
			m.Buffer = string(runes[:len(runes)-1])
		}
		return m, nil
	case tea.KeyEnter:
		return m.commit()
	case tea.KeySpace:
		// Variable names contain no spaces; ignore.
		return m, nil
	case tea.KeyRunes:
		for _, r := range key.Runes {
			if !isSpace(r) {
				m.Buffer += string(r)
			}
		}
		m.errMsg = ""
		return m, nil
	}
	return m, nil
}

// commit resolves the current item from the buffer, advancing on success.
func (m PlaceholderModel) commit() (tea.Model, tea.Cmd) {
	if m.Index >= len(m.Items) {
		m.Done = true
		m.Confirmed = true
		return m, tea.Quit
	}
	typed := strings.TrimSpace(m.Buffer)
	var name string
	if typed == "" {
		// Accept default.
		name = m.Items[m.Index].DefaultVar
	} else {
		name = normalizeVarName(typed)
		if !validVarName.MatchString(name) {
			m.errMsg = "invalid variable name; use letters, digits, underscore (must not start with a digit)"
			return m, nil
		}
	}
	m.Values[m.Index] = "${" + name + "}"
	m.Index++
	m.Buffer = ""
	m.errMsg = ""
	if m.Index >= len(m.Items) {
		m.Done = true
		m.Confirmed = true
		return m, tea.Quit
	}
	return m, nil
}

// Results returns the resolved overrides keyed by MCP name. A cancelled or
// unfinished model returns an empty map.
func (m PlaceholderModel) Results() map[string]model.PlaceholderOverrides {
	out := map[string]model.PlaceholderOverrides{}
	if !m.Confirmed {
		return out
	}
	for idx, item := range m.Items {
		value, ok := m.Values[idx]
		if !ok {
			continue
		}
		ov := out[item.MCP]
		switch item.Scope {
		case "header":
			if ov.Headers == nil {
				ov.Headers = map[string]string{}
			}
			ov.Headers[item.Key] = value
		default:
			if ov.Env == nil {
				ov.Env = map[string]string{}
			}
			ov.Env[item.Key] = value
		}
		out[item.MCP] = ov
	}
	return out
}

func (m PlaceholderModel) View() string {
	title := lipgloss.NewStyle().Bold(true).Render("Resolve placeholder variables")
	if m.Index >= len(m.Items) {
		return title
	}
	item := m.Items[m.Index]
	rows := []string{
		title,
		fmt.Sprintf("Token %d/%d", m.Index+1, len(m.Items)),
		fmt.Sprintf("%s (%s) %s", item.MCP, item.Scope, item.Key),
		fmt.Sprintf("[default: ${%s}] > %s", item.DefaultVar, m.Buffer),
	}
	if m.errMsg != "" {
		rows = append(rows, lipgloss.NewStyle().Faint(true).Render(m.errMsg))
	}
	rows = append(rows, lipgloss.NewStyle().Faint(true).Render("enter to accept default · esc to cancel"))
	return strings.Join(rows, "\n")
}

// normalizeVarName strips a surrounding ${...} wrapper if the user typed one,
// so both "FOO" and "${FOO}" resolve to "FOO".
func normalizeVarName(s string) string {
	if strings.HasPrefix(s, "${") && strings.HasSuffix(s, "}") && len(s) > 3 {
		return s[2 : len(s)-1]
	}
	return s
}

func isSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r'
}
