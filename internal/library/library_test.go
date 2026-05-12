package library

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/poconnor/graft/internal/config"
	"github.com/poconnor/graft/internal/model"
)

func TestImportFileClaudeJSON(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "mcp.json")
	content := `{"mcpServers":{"docs":{"command":"npx","args":["docs"],"env":{"TOKEN":"${TOKEN}"}}}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	defs, err := ImportFile(path)
	if err != nil {
		t.Fatalf("ImportFile() error = %v", err)
	}
	if len(defs) != 1 || defs[0].Name != "docs" || defs[0].Adapters["claude"].Command != "npx" {
		t.Fatalf("ImportFile() = %+v, want docs Claude definition", defs)
	}
}

func TestWriteDefinitionRejectsTraversalAndCollision(t *testing.T) {
	t.Parallel()
	lib := config.Library{Name: "core", CachePath: t.TempDir()}
	if _, err := WriteDefinition(lib, model.Definition{Name: "../bad"}); err == nil {
		t.Fatal("WriteDefinition() traversal error = nil")
	}
	def := model.Definition{Name: "docs", Version: "1.0.0", Command: "npx"}
	if _, err := WriteDefinition(lib, def); err != nil {
		t.Fatalf("WriteDefinition() error = %v", err)
	}
	if _, err := WriteDefinition(lib, def); err == nil {
		t.Fatal("WriteDefinition() collision error = nil")
	}
}

func TestImportFileCodexTOML(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	content := "[mcp_servers.docs]\ncommand = \"uvx\"\nargs = [\"docs\"]\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	defs, err := ImportFile(path)
	if err != nil {
		t.Fatalf("ImportFile() error = %v", err)
	}
	if len(defs) != 1 || defs[0].Name != "docs" || defs[0].Adapters["codex"].Command != "uvx" {
		t.Fatalf("ImportFile() = %+v, want docs Codex definition", defs)
	}
}
