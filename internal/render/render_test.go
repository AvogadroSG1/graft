package render

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/poconnor/graft/internal/model"
)

func TestClaudeAdapterRenderAndRemoveManagedEntry(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	adapter := NewClaudeAdapter(root)
	def := model.Definition{Name: "docs", Command: "npx", Args: []string{"docs-server"}, Env: map[string]string{"TOKEN": "${TOKEN}"}}
	if err := adapter.Render(def); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "_graft_managed") {
		t.Fatalf("rendered config missing managed marker: %s", data)
	}
	if err := adapter.Remove("docs"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	data, err = os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "docs-server") {
		t.Fatalf("Remove() left managed entry: %s", data)
	}
}

func TestCodexAdapterRender(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	err := NewCodexAdapter(root).Render(model.Definition{Name: "docs", Command: "uvx", Args: []string{"docs"}, Env: map[string]string{}})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "[mcp_servers.docs]") {
		t.Fatalf("rendered config = %s, want docs section", data)
	}
}
