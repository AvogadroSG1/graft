package render

import (
	"github.com/BurntSushi/toml"
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

func TestAdaptersRenderHTTPTransportWithoutStdioFields(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	def := model.Definition{
		Name:    "remote",
		Type:    "http",
		URL:     "https://example.com/mcp",
		Headers: map[string]string{"Authorization": "${AUTH_TOKEN}"},
		Command: "npx",
		Args:    []string{"should-not-render"},
		Env:     map[string]string{"TOKEN": "${TOKEN}"},
	}
	if err := NewClaudeAdapter(root).Render(def); err != nil {
		t.Fatalf("Claude Render() error = %v", err)
	}
	claudeData, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	claude := string(claudeData)
	if !strings.Contains(claude, `"type": "http"`) || !strings.Contains(claude, `"url": "https://example.com/mcp"`) {
		t.Fatalf("Claude render missing transport fields: %s", claude)
	}
	if strings.Contains(claude, "should-not-render") || strings.Contains(claude, `"command"`) {
		t.Fatalf("Claude render included stdio fields for HTTP server: %s", claude)
	}

	if err := NewCodexAdapter(root).Render(def); err != nil {
		t.Fatalf("Codex Render() error = %v", err)
	}
	codexData, err := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	codex := string(codexData)
	if !strings.Contains(codex, `type = "http"`) || !strings.Contains(codex, `url = "https://example.com/mcp"`) {
		t.Fatalf("Codex render missing transport fields: %s", codex)
	}
	if strings.Contains(codex, "should-not-render") || strings.Contains(codex, "command") || strings.Contains(codex, "headers") {
		t.Fatalf("Codex render included stdio fields for HTTP server: %s", codex)
	}
}

func TestCodexAdapterPreservesUnrelatedSettings(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model = \"gpt-5\"\napproval_policy = \"never\"\n\n[mcp_servers.existing]\ncommand = \"uvx\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := NewCodexAdapter(root).Render(model.Definition{Name: "remote", Type: "http", URL: "https://example.com/mcp"})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `model = "gpt-5"`) || !strings.Contains(string(data), `approval_policy = "never"`) {
		t.Fatalf("Codex render dropped root settings: %s", data)
	}
	var doc struct {
		MCPServers map[string]struct {
			Type    string `toml:"type"`
			URL     string `toml:"url"`
			Command string `toml:"command"`
		} `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(string(data), &doc); err != nil {
		t.Fatalf("parse rendered TOML: %v", err)
	}
	if doc.MCPServers["existing"].Command != "uvx" {
		t.Fatalf("existing MCP missing after render: %+v", doc.MCPServers)
	}
	if doc.MCPServers["remote"].Type != "http" || doc.MCPServers["remote"].URL != "https://example.com/mcp" {
		t.Fatalf("remote MCP missing transport fields: %+v", doc.MCPServers["remote"])
	}
}
