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
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/model"
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
	if got := strings.TrimSpace(out.String()); got != "pending_input" {
		t.Fatalf("status output = %q, want pending_input", got)
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
