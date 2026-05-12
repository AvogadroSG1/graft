package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		{name: "sync", args: []string{"sync", "extra"}},
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
