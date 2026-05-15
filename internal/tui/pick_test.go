package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/poconnor/graft/internal/model"
)

func TestPickModelConfirmsAndReturnsCompositeSelectedResults(t *testing.T) {
	t.Parallel()
	items := []PickItem{
		{Library: "core", Entry: model.IndexEntry{Name: "docs", Description: "Core docs"}},
		{Library: "tools", Entry: model.IndexEntry{Name: "build", Description: "Build tools"}},
	}
	picker := NewPickModel(items, []string{"core/docs"})

	next, _ := picker.Update(tea.KeyMsg{Type: tea.KeyEnter})
	confirmed := next.(PickModel)

	if !confirmed.Confirmed {
		t.Fatalf("Confirmed = false, want true after enter")
	}
	results := confirmed.Results()
	if len(results) != 1 || results[0].Library != "core" || results[0].Entry.Name != "docs" {
		t.Fatalf("Results() = %+v, want only core/docs", results)
	}
}

func TestPickModelQuitDoesNotConfirmOrReturnResults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		key  tea.KeyMsg
	}{
		{name: "q", key: tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}},
		{name: "ctrl+c", key: tea.KeyMsg{Type: tea.KeyCtrlC}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			picker := NewPickModel([]PickItem{
				{Library: "core", Entry: model.IndexEntry{Name: "docs"}},
			}, []string{"core/docs"})
			picker.Confirmed = true

			next, _ := picker.Update(tt.key)
			quit := next.(PickModel)

			if quit.Confirmed {
				t.Fatalf("Confirmed = true, want false after %s", tt.name)
			}
			if got := quit.Results(); len(got) != 0 {
				t.Fatalf("Results() = %+v, want none after %s", got, tt.name)
			}
		})
	}
}

func TestPickModelUsesLibraryNameInSelectionKey(t *testing.T) {
	t.Parallel()
	items := []PickItem{
		{Library: "core", Entry: model.IndexEntry{Name: "docs", Description: "Core docs"}},
		{Library: "team", Entry: model.IndexEntry{Name: "docs", Description: "Team docs"}},
	}
	picker := NewPickModel(items, []string{"core/docs"})
	next, _ := picker.Update(tea.KeyMsg{Type: tea.KeyDown})
	picker = next.(PickModel)
	next, _ = picker.Update(tea.KeyMsg{Type: tea.KeySpace})
	picker = next.(PickModel)
	next, _ = picker.Update(tea.KeyMsg{Type: tea.KeyEnter})
	confirmed := next.(PickModel)

	results := confirmed.Results()
	if len(results) != 2 {
		t.Fatalf("Results() length = %d, want independent selections for same MCP name", len(results))
	}
	got := map[string]bool{}
	for _, item := range results {
		got[item.Library+"/"+item.Entry.Name] = true
	}
	if !got["core/docs"] || !got["team/docs"] {
		t.Fatalf("Results() = %+v, want core/docs and team/docs", results)
	}
	if !confirmed.Selected["core/docs"] || !confirmed.Selected["team/docs"] {
		t.Fatalf("Selected = %+v, want composite keys for both libraries", confirmed.Selected)
	}
}

func TestPickModelToggleInitializesSelectedMap(t *testing.T) {
	t.Parallel()
	picker := PickModel{Items: []PickItem{
		{Library: "core", Entry: model.IndexEntry{Name: "docs"}},
	}}

	next, _ := picker.Update(tea.KeyMsg{Type: tea.KeySpace})
	toggled := next.(PickModel)

	if !toggled.Selected["core/docs"] {
		t.Fatalf("Selected = %+v, want core/docs toggled without panic", toggled.Selected)
	}
}

func TestPickModelViewIncludesLibraryPrefix(t *testing.T) {
	t.Parallel()
	picker := NewPickModel([]PickItem{
		{Library: "core", Entry: model.IndexEntry{Name: "docs", Description: "Core docs"}},
	}, nil)

	if got := picker.View(); !strings.Contains(got, "core: Core docs") {
		t.Fatalf("View() = %q, want library-prefixed description", got)
	}
}
