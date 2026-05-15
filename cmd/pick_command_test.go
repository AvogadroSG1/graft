package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/poconnor/graft/internal/config"
	librarymock "github.com/poconnor/graft/internal/library/mock"
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/model"
	"github.com/poconnor/graft/internal/tui"
	"go.uber.org/mock/gomock"
)

func TestPickCommandWritesConfirmedSelectionToLock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core", URL: "https://example.com/core.git"}
	cfgPath := writePickConfig(t, lib)
	writePickLock(t, root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: lib.URL}}, MCPs: []lock.InstalledMCP{}})
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(lib).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1.0.0", SHA256: "hash-docs"}}}, nil)
	runner := func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		if ctx == nil {
			t.Fatal("runner context is nil")
		}
		if len(model.Items) != 1 || model.Items[0].Library != "core" || model.Items[0].Entry.Name != "docs" {
			t.Fatalf("picker items = %+v, want core/docs", model.Items)
		}
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
		model = next.(tui.PickModel)
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return next.(tui.PickModel), nil
	}

	cmd := newPickCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, runner)
	cmd.SetArgs([]string{"--targets", "codex"})
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got, err := (lock.FileStore{}).Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MCPs) != 1 || got.MCPs[0].Name != "docs" || got.MCPs[0].Target != "codex" || got.MCPs[0].DefinitionHash != "hash-docs" {
		t.Fatalf("saved lock = %+v, want confirmed docs selection", got)
	}
}

func TestPickCommandCancelLeavesLockUnchanged(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core", URL: "https://example.com/core.git"}
	cfgPath := writePickConfig(t, lib)
	original := lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: lib.URL}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "1.0.0", DefinitionHash: "hash-docs", Target: "both"}},
	}
	writePickLock(t, root, original)
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(lib).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1.0.0", SHA256: "hash-docs"}}}, nil)
	runner := func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		if !model.Selected["core/docs"] {
			t.Fatalf("preselected = %+v, want core/docs", model.Selected)
		}
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
		return next.(tui.PickModel), nil
	}

	cmd := newPickCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, runner)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got, err := (lock.FileStore{}).Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MCPs) != 1 || got.MCPs[0] != original.MCPs[0] {
		t.Fatalf("saved lock = %+v, want unchanged", got)
	}
}

func TestPickCommandPrintsDuplicateWarnings(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	core := config.Library{Name: "core"}
	team := config.Library{Name: "team"}
	cfgPath := writePickConfig(t, core, team)
	writePickLock(t, root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core"}, {Name: "team"}}, MCPs: []lock.InstalledMCP{}})
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(core).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs"}}}, nil)
	client.EXPECT().Index(team).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs"}}}, nil)
	runner := func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		return next.(tui.PickModel), nil
	}
	cmd := newPickCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, runner)
	var errOut bytes.Buffer
	cmd.SetErr(&errOut)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(errOut.String(), "warning: duplicate MCP name \"docs\" in libraries core and team") {
		t.Fatalf("stderr = %q, want duplicate warning", errOut.String())
	}
}

func TestPickCommandErrorsWhenNoLibrariesConfigured(t *testing.T) {
	t.Parallel()
	cmd := newPickCommandWithDeps(context.Background(), &appOptions{configPath: writePickConfig(t), root: t.TempDir()}, librarymock.NewMockClient(gomock.NewController(t)), nil)

	err := cmd.Execute()

	if err == nil || !strings.Contains(err.Error(), "no libraries configured") {
		t.Fatalf("Execute() error = %v, want no libraries configured", err)
	}
}

func TestPickCommandDefaultTargetsApplyToEverySelectedMCP(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core"}
	cfgPath := writePickConfig(t, lib)
	writePickLock(t, root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core"}}, MCPs: []lock.InstalledMCP{}})
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(lib).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs"}, {Name: "build"}}}, nil)
	runner := func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
		model = next.(tui.PickModel)
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
		model = next.(tui.PickModel)
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeySpace})
		model = next.(tui.PickModel)
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return next.(tui.PickModel), nil
	}

	cmd := newPickCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, runner)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got, err := (lock.FileStore{}).Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MCPs) != 2 || got.MCPs[0].Target != "both" || got.MCPs[1].Target != "both" {
		t.Fatalf("saved lock = %+v, want every selected MCP target both", got)
	}
}

func TestPickCommandRemovesUncheckedMCPs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core"}
	cfgPath := writePickConfig(t, lib)
	writePickLock(t, root, lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core"}},
		MCPs: []lock.InstalledMCP{
			{Name: "docs", Library: "core", Target: "both"},
			{Name: "build", Library: "core", Target: "both"},
		},
	})
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(lib).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs"}, {Name: "build"}}}, nil)
	runner := func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		if !model.Selected["core/docs"] || !model.Selected["core/build"] {
			t.Fatalf("preselected = %+v, want docs and build", model.Selected)
		}
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
		model = next.(tui.PickModel)
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return next.(tui.PickModel), nil
	}

	cmd := newPickCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, runner)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got, err := (lock.FileStore{}).Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MCPs) != 1 || got.MCPs[0].Name != "build" {
		t.Fatalf("saved lock = %+v, want docs removed and build kept", got)
	}
}

func TestPickCommandRejectsUnknownTargets(t *testing.T) {
	t.Parallel()
	cmd := newPickCommandWithDeps(context.Background(), &appOptions{configPath: writePickConfig(t, config.Library{Name: "core"}), root: t.TempDir()}, librarymock.NewMockClient(gomock.NewController(t)), nil)
	cmd.SetArgs([]string{"--targets", "bad"})

	err := cmd.Execute()

	if err == nil || !strings.Contains(err.Error(), "unknown target \"bad\"") {
		t.Fatalf("Execute() error = %v, want unknown target", err)
	}
}

func writePickConfig(t *testing.T, libs ...config.Library) string {
	t.Helper()
	path := t.TempDir() + "/config.json"
	if err := (config.FileStore{}).Save(path, config.Config{Libraries: libs}); err != nil {
		t.Fatal(err)
	}
	return path
}

func writePickLock(t *testing.T, root string, lk lock.Lock) {
	t.Helper()
	if err := (lock.FileStore{}).Save(root, lk); err != nil {
		t.Fatal(err)
	}
}
