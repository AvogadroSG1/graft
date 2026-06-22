package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func typeRunes(m PlaceholderModel, s string) PlaceholderModel {
	for _, r := range s {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(PlaceholderModel)
	}
	return m
}

func TestPlaceholderModelAcceptsDefaultOnEmptyEnter(t *testing.T) {
	t.Parallel()
	m := NewPlaceholderModel([]PlaceholderItem{{MCP: "docs", Scope: "env", Key: "API_KEY", DefaultVar: "API_KEY"}})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(PlaceholderModel)
	if !m.Confirmed {
		t.Fatal("model not confirmed after answering single item")
	}
	got := m.Results()
	if got["docs"].Env["API_KEY"] != "${API_KEY}" {
		t.Fatalf("Results = %+v, want default ${API_KEY}", got)
	}
}

func TestPlaceholderModelTypedNameRemaps(t *testing.T) {
	t.Parallel()
	m := NewPlaceholderModel([]PlaceholderItem{{MCP: "docs", Scope: "header", Key: "Authorization", DefaultVar: "TOKEN"}})
	m = typeRunes(m, "MY_TOKEN")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(PlaceholderModel)
	if got := m.Results()["docs"].Headers["Authorization"]; got != "${MY_TOKEN}" {
		t.Fatalf("Results header = %q, want ${MY_TOKEN}", got)
	}
}

func TestPlaceholderModelNormalizesWrappedInput(t *testing.T) {
	t.Parallel()
	m := NewPlaceholderModel([]PlaceholderItem{{MCP: "docs", Scope: "env", Key: "API_KEY", DefaultVar: "API_KEY"}})
	m = typeRunes(m, "${FOO}")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(PlaceholderModel)
	if got := m.Results()["docs"].Env["API_KEY"]; got != "${FOO}" {
		t.Fatalf("Results = %q, want ${FOO} (no double wrap)", got)
	}
}

func TestPlaceholderModelRejectsInvalidNameThenAccepts(t *testing.T) {
	t.Parallel()
	m := NewPlaceholderModel([]PlaceholderItem{{MCP: "docs", Scope: "env", Key: "API_KEY", DefaultVar: "API_KEY"}})
	m = typeRunes(m, "1bad")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(PlaceholderModel)
	if m.Done {
		t.Fatal("model advanced past invalid name")
	}
	if m.errMsg == "" {
		t.Fatal("expected error message for invalid name")
	}
	// Backspace away the invalid input and type a valid name.
	for range "1bad" {
		next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
		m = next.(PlaceholderModel)
	}
	m = typeRunes(m, "GOOD")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(PlaceholderModel)
	if got := m.Results()["docs"].Env["API_KEY"]; got != "${GOOD}" {
		t.Fatalf("Results = %q, want ${GOOD}", got)
	}
}

func TestPlaceholderModelCancelReturnsNoResults(t *testing.T) {
	t.Parallel()
	m := NewPlaceholderModel([]PlaceholderItem{{MCP: "docs", Scope: "env", Key: "API_KEY", DefaultVar: "API_KEY"}})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(PlaceholderModel)
	if m.Confirmed {
		t.Fatal("model confirmed after cancel")
	}
	if len(m.Results()) != 0 {
		t.Fatalf("Results = %+v, want empty after cancel", m.Results())
	}
}

func TestPlaceholderModelWalksMultipleItems(t *testing.T) {
	t.Parallel()
	m := NewPlaceholderModel([]PlaceholderItem{
		{MCP: "docs", Scope: "env", Key: "A", DefaultVar: "A"},
		{MCP: "docs", Scope: "env", Key: "B", DefaultVar: "B"},
	})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(PlaceholderModel)
	if m.Done {
		t.Fatal("model finished before answering all items")
	}
	m = typeRunes(m, "BB")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(PlaceholderModel)
	got := m.Results()["docs"].Env
	if got["A"] != "${A}" || got["B"] != "${BB}" {
		t.Fatalf("Results = %+v, want A=${A} B=${BB}", got)
	}
}
