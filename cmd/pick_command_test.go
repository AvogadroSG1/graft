package cmd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
	client.EXPECT().Definition(lib, "docs").Return(model.Definition{Name: "docs", Version: "1.0.0", Command: "npx", Args: []string{"docs"}}, "hash-docs", nil)
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
	if data, err := os.ReadFile(filepath.Join(root, ".codex", "config.toml")); err != nil || !strings.Contains(string(data), "docs") {
		t.Fatalf("codex config = %q, %v; want rendered docs", data, err)
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
	client.EXPECT().Index(lib).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", SHA256: "hash-docs"}, {Name: "build", SHA256: "hash-build"}}}, nil)
	client.EXPECT().Definition(lib, "docs").Return(model.Definition{Name: "docs", Command: "npx"}, "hash-docs", nil)
	client.EXPECT().Definition(lib, "build").Return(model.Definition{Name: "build", Command: "npx"}, "hash-build", nil)
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
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"docs":{"command":"npx","_graft_managed":true},"build":{"command":"npx","_graft_managed":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
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
	data, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "docs") || !strings.Contains(string(data), "build") {
		t.Fatalf("claude config = %s, want docs removed and build kept", data)
	}
}

func TestPickCommandConfirmedUnchangedSelectionDoesNotFetchDefinition(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core"}
	cfgPath := writePickConfig(t, lib)
	original := lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "1", Target: "both", DefinitionHash: "hash-docs", PendingInput: true}},
	}
	writePickLock(t, root, original)
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"docs":{"command":"npx","_graft_managed":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(lib).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1", SHA256: "hash-docs"}}}, nil)
	runner := func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		if !model.Selected["core/docs"] {
			t.Fatalf("preselected = %+v, want core/docs", model.Selected)
		}
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
		t.Fatalf("lock = %+v, want unchanged selection preserved", got)
	}
}

func TestPickCommandRollsBackRenderedFilesOnLockSaveFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core"}
	cfgPath := writePickConfig(t, lib)
	writePickLock(t, root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core"}}, MCPs: []lock.InstalledMCP{}})
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(lib).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1", SHA256: "hash-docs"}}}, nil)
	client.EXPECT().Definition(lib, "docs").Return(model.Definition{Name: "docs", Version: "1", Command: "npx"}, "hash-docs", nil)
	runner := func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		if err := os.Remove(filepath.Join(root, "graft.lock")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(root, "graft.lock"), 0o700); err != nil {
			t.Fatal(err)
		}
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
		model = next.(tui.PickModel)
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return next.(tui.PickModel), nil
	}

	cmd := newPickCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, runner)
	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want lock save failure")
	}
	if _, statErr := os.Stat(filepath.Join(root, ".mcp.json")); !os.IsNotExist(statErr) {
		t.Fatalf("claude config stat error = %v, want rollback to remove rendered config", statErr)
	}
}

func TestPickCommandRollsBackRenderedFilesOnSideEffectFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core"}
	cfgPath := writePickConfig(t, lib)
	original := lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core"}},
		MCPs: []lock.InstalledMCP{
			{Name: "docs", Library: "core", Target: "both", DefinitionHash: "hash-docs"},
			{Name: "build", Library: "core", Target: "both", DefinitionHash: "old-build"},
		},
	}
	writePickLock(t, root, original)
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"docs":{"command":"npx","_graft_managed":true},"build":{"command":"npx","_graft_managed":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(lib).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", SHA256: "hash-docs"}, {Name: "build", SHA256: "new-build"}}}, nil)
	client.EXPECT().Definition(lib, "build").Return(model.Definition{}, "", errors.New("definition failed"))
	runner := func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
		model = next.(tui.PickModel)
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return next.(tui.PickModel), nil
	}

	cmd := newPickCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, runner)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "definition failed") {
		t.Fatalf("Execute() error = %v, want definition failure", err)
	}
	got, loadErr := (lock.FileStore{}).Load(root)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(got.MCPs) != len(original.MCPs) {
		t.Fatalf("lock = %+v, want original lock after failed side effects", got)
	}
	data, readErr := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !strings.Contains(string(data), "docs") || !strings.Contains(string(data), "build") {
		t.Fatalf("claude config = %s, want rollback to docs and build", data)
	}
}

func TestPickCommandRejectsAuthSensitiveDefinitionWithoutRendering(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core"}
	cfgPath := writePickConfig(t, lib)
	original := lock.Lock{Libraries: []lock.LibraryRef{{Name: "core"}}, MCPs: []lock.InstalledMCP{}}
	writePickLock(t, root, original)
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(lib).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", SHA256: "hash-docs"}}}, nil)
	client.EXPECT().Definition(lib, "docs").Return(model.Definition{Name: "docs", Command: "npx", Env: map[string]string{"API_KEY": "${API_KEY}"}}, "hash-docs", nil)
	runner := func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
		model = next.(tui.PickModel)
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return next.(tui.PickModel), nil
	}

	cmd := newPickCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, runner)
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "auth warning") {
		t.Fatalf("Execute() error = %v, want auth warning", err)
	}
	got, loadErr := (lock.FileStore{}).Load(root)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(got.MCPs) != 0 {
		t.Fatalf("lock = %+v, want original empty lock", got)
	}
	if _, statErr := os.Stat(filepath.Join(root, ".mcp.json")); !os.IsNotExist(statErr) {
		t.Fatalf("claude config stat error = %v, want no rendered config", statErr)
	}
}

func TestPickCommandUsesConfiguredLibrariesWhenLockHasNone(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core", URL: "https://example.com/core.git"}
	cfgPath := writePickConfig(t, lib)
	writePickLock(t, root, lock.Lock{Libraries: []lock.LibraryRef{}, MCPs: []lock.InstalledMCP{}})
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(lib).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1", SHA256: "hash-docs"}}}, nil)
	client.EXPECT().Definition(lib, "docs").Return(model.Definition{Name: "docs", Version: "1", Command: "npx"}, "hash-docs", nil)
	runner := func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
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
	if len(got.Libraries) != 1 || got.Libraries[0].Name != "core" || len(got.MCPs) != 1 {
		t.Fatalf("lock = %+v, want configured library adopted and docs selected", got)
	}
}

func TestPickCommandUsesConfiguredLibrariesMissingFromNonEmptyLock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	core := config.Library{Name: "core", URL: "https://example.com/core.git"}
	team := config.Library{Name: "team", URL: "https://example.com/team.git"}
	cfgPath := writePickConfig(t, core, team)
	writePickLock(t, root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: core.URL}}, MCPs: []lock.InstalledMCP{}})
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(core).Return(model.LibraryIndex{MCPs: []model.IndexEntry{}}, nil)
	client.EXPECT().Index(team).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "deploy", Version: "1", SHA256: "hash-deploy"}}}, nil)
	client.EXPECT().Definition(team, "deploy").Return(model.Definition{Name: "deploy", Version: "1", Command: "npx"}, "hash-deploy", nil)
	runner := func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		if len(model.Items) != 1 || model.Items[0].Library != "team" {
			t.Fatalf("items = %+v, want team deploy selectable", model.Items)
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
	if len(got.Libraries) != 2 || got.Libraries[1].Name != "team" || len(got.MCPs) != 1 || got.MCPs[0].Library != "team" {
		t.Fatalf("lock = %+v, want missing configured library adopted and selected", got)
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
