package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/poconnor/graft/internal/config"
	librarymock "github.com/poconnor/graft/internal/library/mock"
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/model"
	"github.com/poconnor/graft/internal/render"
	"go.uber.org/mock/gomock"
)

func TestSyncCommandPullsAndProcessesNamedMCP(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: t.TempDir()}
	cfgPath := writeSyncConfig(t, lib)
	writeSyncLock(t, root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: lib.URL}}, MCPs: []lock.InstalledMCP{
		{Name: "docs", Library: "core", Version: "2", DefinitionHash: "old-docs", Target: "claude"},
		{Name: "build", Library: "core", Version: "1", DefinitionHash: "old-build", Target: "claude"},
	}})
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Pull(gomock.Any(), lib).Return("commit-sha", nil)
	client.EXPECT().Definition(lib, "docs").Return(model.Definition{Name: "docs", Version: "2", Command: "npx"}, "new-docs", nil)
	adapter := &syncRecordingAdapter{}
	command := newSyncCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, []render.AdapterByName{{Name: "claude", Adapter: adapter}})
	command.SetArgs([]string{"docs"})
	var out bytes.Buffer
	command.SetOut(&out)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if adapter.def.Name != "docs" {
		t.Fatalf("rendered = %+v, want docs only", adapter.def)
	}
	var decoded struct {
		Succeeded []string `json:"succeeded"`
	}
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Succeeded) != 1 || decoded.Succeeded[0] != "docs" {
		t.Fatalf("output = %s, want docs succeeded", out.String())
	}
}

func TestSyncCommandNoPullSkipsLibraryPull(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: t.TempDir()}
	cfgPath := writeSyncConfig(t, lib)
	writeSyncLock(t, root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: lib.URL}}, MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "2", DefinitionHash: "old", Target: "claude"}}})
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Definition(lib, "docs").Return(model.Definition{Name: "docs", Version: "2", Command: "npx"}, "new", nil)
	command := newSyncCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, []render.AdapterByName{{Name: "claude", Adapter: &syncRecordingAdapter{}}})
	command.SetArgs([]string{"--no-pull"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestSyncCommandRegistersUnknownLockLibraryBeforePull(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfgPath := writeSyncConfig(t)
	url := "https://example.com/core.git"
	writeSyncLock(t, root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: url}}, MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "2", DefinitionHash: "old", Target: "claude"}}})
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Add(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, lib config.Library) error {
		if lib.Name != "core" || lib.URL != url || lib.CachePath == "" {
			t.Fatalf("Add lib = %+v, want populated core library", lib)
		}
		return nil
	})
	client.EXPECT().Pull(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, lib config.Library) (string, error) {
		if lib.Name != "core" || lib.URL != url || lib.CachePath == "" {
			t.Fatalf("Pull lib = %+v, want registered core library", lib)
		}
		return "commit-sha", nil
	})
	client.EXPECT().Definition(gomock.Any(), "docs").DoAndReturn(func(lib config.Library, name string) (model.Definition, string, error) {
		if lib.Name != "core" || name != "docs" {
			t.Fatalf("Definition(%+v, %q), want core docs", lib, name)
		}
		return model.Definition{Name: "docs", Version: "2", Command: "npx"}, "new", nil
	})
	adapter := &syncRecordingAdapter{}
	command := newSyncCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, []render.AdapterByName{{Name: "claude", Adapter: adapter}})
	command.SetIn(strings.NewReader("\n"))

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if adapter.def.Name != "docs" {
		t.Fatalf("rendered = %+v, want docs", adapter.def)
	}
}

func TestSyncCommandPullsOnlyLibrariesForSelectedMCPs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	core := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: t.TempDir()}
	extra := config.Library{Name: "extra", URL: "https://example.com/extra.git", CachePath: t.TempDir()}
	cfgPath := writeSyncConfig(t, core, extra)
	writeSyncLock(t, root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: core.URL}, {Name: "extra", URL: extra.URL}}, MCPs: []lock.InstalledMCP{
		{Name: "docs", Library: "core", Version: "2", DefinitionHash: "old-docs", Target: "claude"},
		{Name: "build", Library: "extra", Version: "1", DefinitionHash: "old-build", Target: "claude"},
	}})
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Pull(gomock.Any(), core).Return("commit-sha", nil)
	client.EXPECT().Definition(core, "docs").Return(model.Definition{Name: "docs", Version: "2", Command: "npx"}, "new-docs", nil)
	adapter := &syncRecordingAdapter{}
	command := newSyncCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, []render.AdapterByName{{Name: "claude", Adapter: adapter}})
	command.SetArgs([]string{"docs"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if adapter.def.Name != "docs" {
		t.Fatalf("rendered = %+v, want docs only", adapter.def)
	}
}

func TestSyncCommandRejectsUnknownNamedMCPBeforePull(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: t.TempDir()}
	cfgPath := writeSyncConfig(t, lib)
	writeSyncLock(t, root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: lib.URL}}, MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "1", DefinitionHash: "old", Target: "claude"}}})
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	command := newSyncCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, []render.AdapterByName{{Name: "claude", Adapter: &syncRecordingAdapter{}}})
	command.SetArgs([]string{"missing"})

	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("Execute() error = %v, want missing MCP error", err)
	}
}

func TestSyncCommandNoOpDoesNotPullOrWriteMissingLock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: t.TempDir()}
	cfgPath := writeSyncConfig(t, lib)
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	command := newSyncCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, []render.AdapterByName{{Name: "claude", Adapter: &syncRecordingAdapter{}}})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	_, err := os.Stat(filepath.Join(root, lock.Filename))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock stat error = %v, want not exist", err)
	}
}

func TestSyncCommandDefaultAdaptersUseConfiguredRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: t.TempDir()}
	cfgPath := writeSyncConfig(t, lib)
	writeSyncLock(t, root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: lib.URL}}, MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "2", DefinitionHash: "old", Target: "both"}}})
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Definition(lib, "docs").Return(model.Definition{Name: "docs", Version: "2", Command: "npx", Args: []string{"docs"}}, "new", nil)
	command := newSyncCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath, root: root}, client, nil)
	command.SetArgs([]string{"--no-pull"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	claudeConfig, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatalf("read Claude config: %v", err)
	}
	if !bytes.Contains(claudeConfig, []byte("docs")) {
		t.Fatalf("Claude config = %s, want docs", string(claudeConfig))
	}
	codexConfig, err := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("read Codex config: %v", err)
	}
	if !bytes.Contains(codexConfig, []byte("docs")) {
		t.Fatalf("Codex config = %s, want docs", string(codexConfig))
	}
}

func writeSyncConfig(t *testing.T, libs ...config.Library) string {
	t.Helper()
	path := t.TempDir() + "/config.json"
	if err := (config.FileStore{}).Save(path, config.Config{Libraries: libs}); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeSyncLock(t *testing.T, root string, lk lock.Lock) {
	t.Helper()
	if err := (lock.FileStore{}).Save(root, lk); err != nil {
		t.Fatal(err)
	}
}

type syncRecordingAdapter struct {
	def model.Definition
}

func (a *syncRecordingAdapter) Render(def model.Definition) error { a.def = def; return nil }
func (a *syncRecordingAdapter) Remove(string) error               { return nil }
func (a *syncRecordingAdapter) TargetFile() string                { return "" }
