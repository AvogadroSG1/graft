package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/poconnor/graft/internal/config"
	"github.com/poconnor/graft/internal/library"
	librarymock "github.com/poconnor/graft/internal/library/mock"
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/model"
	statuspkg "github.com/poconnor/graft/internal/status"
	"go.uber.org/mock/gomock"
)

func TestInitCommandCreatesLockAndTargets(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "config.json")
	cmd := NewRootCommand(context.Background())
	cmd.SetArgs([]string{"--config", cfg, "--root", root, "init", "--targets", "both", "--yes"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, path := range []string{"graft.lock", ".mcp.json", ".codex/config.toml"} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Fatalf("%s missing: %v", path, err)
		}
	}
	if !strings.Contains(out.String(), "initialized graft") {
		t.Fatalf("output = %q, want initialization message", out.String())
	}
}

func TestCommandsRejectUnexpectedArgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
	}{
		{name: "init", args: []string{"init", "extra"}},
		{name: "library list", args: []string{"library", "list", "extra"}},
		{name: "mcp import", args: []string{"mcp", "import", "extra", "--from", "x.json"}},
		{name: "status", args: []string{"status", "extra"}},
		{name: "install hooks", args: []string{"install-hooks", "extra"}},
		{name: "pick", args: []string{"pick", "extra"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command := NewRootCommand(context.Background())
			command.SetArgs(tt.args)
			if err := command.Execute(); err == nil {
				t.Fatalf("Execute(%v) error = nil, want usage error", tt.args)
			}
		})
	}
}

func TestSyncCommandSavesPendingInputLock(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: t.TempDir(), Default: true}
	cfgPath := filepath.Join(root, "config.json")
	if err := (config.FileStore{}).Save(cfgPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		t.Fatal(err)
	}
	if err := (lock.FileStore{}).Save(root, lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: lib.URL}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "1", DefinitionHash: "old", Target: "claude"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := library.WriteDefinitionFile(lib, model.Definition{Name: "docs", Version: "2", Command: "npx"}, true); err != nil {
		t.Fatal(err)
	}
	writeCommandMigration(t, lib.CachePath, "docs", map[string]any{
		"from": "1",
		"to":   "2",
		"steps": []map[string]any{
			{"type": "require_input", "path": "env.token"},
		},
	})

	command := NewRootCommand(context.Background())
	command.SetArgs([]string{"--config", cfgPath, "--root", root, "sync", "--no-pull"})
	var out bytes.Buffer
	command.SetOut(&out)
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got, err := (lock.FileStore{}).Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.MCPs) != 1 || !got.MCPs[0].PendingInput {
		t.Fatalf("saved lock = %+v, want pending_input", got)
	}
}

func TestStatusCommandJSONUsesLibraryIndex(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core", URL: "url", CachePath: t.TempDir()}
	cfgPath := filepath.Join(root, "config.json")
	if err := (config.FileStore{}).Save(cfgPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"docs":{"command":"npx","_graft_managed":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (lock.FileStore{}).Save(root, lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", DefinitionHash: "old", Target: "claude"}},
	}); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(lib).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "2", SHA256: "new"}}}, nil)
	client.EXPECT().Definition(lib, "docs").Return(model.Definition{Name: "docs"}, "new", nil)
	command := newStatusCommandWithDeps(&appOptions{configPath: cfgPath, root: root}, client)
	command.SetArgs([]string{"--json"})
	var out bytes.Buffer
	command.SetOut(&out)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout = %q, want JSON: %v", out.String(), err)
	}
	if decoded["state"] != "drifted" {
		t.Fatalf("status JSON = %s, want drifted", out.String())
	}
}

func TestStatusQuietConfiguredExitsZero(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core", URL: "url", CachePath: t.TempDir()}
	cfgPath := filepath.Join(root, "config.json")
	if err := (config.FileStore{}).Save(cfgPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"docs":{"command":"npx","_graft_managed":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (lock.FileStore{}).Save(root, lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", DefinitionHash: "same", Target: "claude"}},
	}); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(lib).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1", SHA256: "same"}}}, nil)
	client.EXPECT().Definition(lib, "docs").Return(model.Definition{Name: "docs"}, "same", nil)
	command := newStatusCommandWithDeps(&appOptions{configPath: cfgPath, root: root}, client)
	command.SetArgs([]string{"--quiet"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v, want configured zero exit", err)
	}
}

func TestStatusCommandPrintsPendingInput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	if err := (config.FileStore{}).Save(cfgPath, config.Config{Libraries: []config.Library{{Name: "core", URL: "url"}}}); err != nil {
		t.Fatal(err)
	}
	if err := (lock.FileStore{}).Save(root, lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", PendingInput: true}},
	}); err != nil {
		t.Fatal(err)
	}

	command := NewRootCommand(context.Background())
	command.SetArgs([]string{"--config", cfgPath, "--root", root, "status"})
	var out bytes.Buffer
	command.SetOut(&out)
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{"STATE", "MCP", "INSTALLED", "LIBRARY", "pending_input", "docs", "graft sync"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status output = %q, want %q", got, want)
		}
	}
}

func TestStatusCommandPrintsDriftTableWithVersions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := config.Library{Name: "core", URL: "url", CachePath: t.TempDir()}
	cfgPath := filepath.Join(root, "config.json")
	if err := (config.FileStore{}).Save(cfgPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"docs":{"command":"npx","_graft_managed":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (lock.FileStore{}).Save(root, lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "1", DefinitionHash: "old", Target: "claude"}},
	}); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(lib).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "2", SHA256: "new"}}}, nil)
	client.EXPECT().Definition(lib, "docs").Return(model.Definition{Name: "docs"}, "new", nil)
	command := newStatusCommandWithDeps(&appOptions{configPath: cfgPath, root: root}, client)
	var out bytes.Buffer
	command.SetOut(&out)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{"STATE", "MCP", "INSTALLED", "LIBRARY", "drifted", "docs", "1", "2", "graft sync"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status output = %q, want %q", got, want)
		}
	}
}

func TestStatusRowStateMatchesExactMCPName(t *testing.T) {
	t.Parallel()
	result := statuspkg.Result{State: statuspkg.StateDrifted, Details: []string{"external edit: claude/docs is missing"}}
	mcp := lock.InstalledMCP{Name: "doc", Library: "core"}
	if got := statusRowState(result, nil, mcp); got != statuspkg.StateConfigured {
		t.Fatalf("statusRowState() = %s, want configured for prefix-only match", got)
	}
}

func TestStatusCommandPromptsToRegisterUnknownLockLibrary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	if err := (config.FileStore{}).Save(cfgPath, config.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := (lock.FileStore{}).Save(root, lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "https://example.com/core.git"}},
	}); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Add(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, lib config.Library) error {
		if lib.Name != "core" || lib.URL != "https://example.com/core.git" || lib.CachePath == "" {
			t.Fatalf("Add lib = %+v, want populated core library", lib)
		}
		return nil
	})
	command := newStatusCommandWithDeps(&appOptions{configPath: cfgPath, root: root}, client)
	command.SetIn(strings.NewReader("\n"))
	var errOut bytes.Buffer
	command.SetErr(&errOut)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(errOut.String(), "Register and clone") {
		t.Fatalf("stderr = %q, want registration prompt", errOut.String())
	}
	got, err := (config.FileStore{}).Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	lib, ok := got.Library("core")
	if !ok || lib.URL != "https://example.com/core.git" || lib.CachePath == "" {
		t.Fatalf("config library = %+v, %v; want registered core", lib, ok)
	}
}

func TestStatusCommandDeclinesUnknownLockLibraryWithManualCommand(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	if err := (config.FileStore{}).Save(cfgPath, config.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := (lock.FileStore{}).Save(root, lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "https://example.com/core.git"}},
	}); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	command := newStatusCommandWithDeps(&appOptions{configPath: cfgPath, root: root}, client)
	command.SetIn(strings.NewReader("n\n"))

	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "graft library add core https://example.com/core.git") {
		t.Fatalf("Execute() error = %v, want manual library add command", err)
	}
}

func TestStatusCommandEOFDeclinesUnknownLockLibraryWithoutClone(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	if err := (config.FileStore{}).Save(cfgPath, config.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := (lock.FileStore{}).Save(root, lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "https://example.com/core.git"}},
	}); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	command := newStatusCommandWithDeps(&appOptions{configPath: cfgPath, root: root}, client)
	command.SetIn(strings.NewReader(""))

	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "graft library add core https://example.com/core.git") {
		t.Fatalf("Execute() error = %v, want manual library add command", err)
	}
	got, err := (config.FileStore{}).Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Library("core"); ok {
		t.Fatalf("config = %+v, want no auto-registration on EOF", got)
	}
}

func TestStatusCommandUnknownLockLibraryWithoutURLFailsBeforePrompt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	if err := (config.FileStore{}).Save(cfgPath, config.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := (lock.FileStore{}).Save(root, lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core"}},
	}); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	command := newStatusCommandWithDeps(&appOptions{configPath: cfgPath, root: root}, client)
	var errOut bytes.Buffer
	command.SetErr(&errOut)

	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "does not include a URL") {
		t.Fatalf("Execute() error = %v, want missing URL error", err)
	}
	if strings.Contains(errOut.String(), "Register and clone") {
		t.Fatalf("stderr = %q, want no prompt", errOut.String())
	}
}

func TestStatusQuietDoesNotPromptOrCloneUnknownLockLibrary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	if err := (config.FileStore{}).Save(cfgPath, config.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := (lock.FileStore{}).Save(root, lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "https://example.com/core.git"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core"}},
	}); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	command := newStatusCommandWithDeps(&appOptions{configPath: cfgPath, root: root}, client)
	command.SetArgs([]string{"--quiet"})
	command.SetIn(strings.NewReader("\n"))
	var errOut bytes.Buffer
	command.SetErr(&errOut)

	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown_library") {
		t.Fatalf("Execute() error = %v, want unknown_library", err)
	}
	if strings.Contains(errOut.String(), "Register and clone") {
		t.Fatalf("stderr = %q, want quiet no prompt", errOut.String())
	}
}

func TestStatusJSONKeepsBootstrapPromptOffStdout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	if err := (config.FileStore{}).Save(cfgPath, config.Config{}); err != nil {
		t.Fatal(err)
	}
	if err := (lock.FileStore{}).Save(root, lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "https://example.com/core.git"}},
	}); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Add(gomock.Any(), gomock.Any()).Return(nil)
	command := newStatusCommandWithDeps(&appOptions{configPath: cfgPath, root: root}, client)
	command.SetArgs([]string{"--json"})
	command.SetIn(strings.NewReader("\n"))
	var out bytes.Buffer
	var errOut bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&errOut)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(out.String(), "Register and clone") {
		t.Fatalf("stdout = %q, want JSON only", out.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout = %q, want JSON: %v", out.String(), err)
	}
	if !strings.Contains(errOut.String(), "Register and clone") {
		t.Fatalf("stderr = %q, want prompt", errOut.String())
	}
}

func TestExitCodeClassifiesUsageErrors(t *testing.T) {
	t.Parallel()
	if got := ExitCode(&cmdError{text: "accepts 0 arg(s), received 1"}); got != 2 {
		t.Fatalf("ExitCode() = %d, want 2", got)
	}
}

type cmdError struct {
	text string
}

func (e *cmdError) Error() string {
	return e.text
}

func writeCommandMigration(t *testing.T, root, name string, body map[string]any) {
	t.Helper()
	dir := filepath.Join(root, "migrations", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, body["from"].(string)+"-to-"+body["to"].(string)+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
