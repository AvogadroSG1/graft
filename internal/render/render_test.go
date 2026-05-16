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
	if !strings.Contains(codex, "Authorization") {
		t.Fatalf("Codex render missing HTTP headers: %s", codex)
	}
	if strings.Contains(codex, "should-not-render") || strings.Contains(codex, "command") {
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
	if err := os.WriteFile(path, []byte("model = \"gpt-5\"\napproval_policy = \"never\"\n\n[mcp_servers.existing]\ntype = \"http\"\nurl = \"https://existing.example/mcp\"\nheaders = { Authorization = \"${TOKEN}\" }\ncustom_field = \"keep-me\"\n"), 0o600); err != nil {
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
			Type    string            `toml:"type"`
			URL     string            `toml:"url"`
			Command string            `toml:"command"`
			Headers map[string]string `toml:"headers"`
			Custom  string            `toml:"custom_field"`
		} `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(string(data), &doc); err != nil {
		t.Fatalf("parse rendered TOML: %v", err)
	}
	if doc.MCPServers["existing"].Type != "http" || doc.MCPServers["existing"].Headers["Authorization"] != "${TOKEN}" || doc.MCPServers["existing"].Custom != "keep-me" {
		t.Fatalf("existing MCP fields missing after render: %+v", doc.MCPServers["existing"])
	}
	if doc.MCPServers["remote"].Type != "http" || doc.MCPServers["remote"].URL != "https://example.com/mcp" {
		t.Fatalf("remote MCP missing transport fields: %+v", doc.MCPServers["remote"])
	}
}

func TestClaudeAdapterPreservesUnrelatedMCPFields(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, ".mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"existing":{"command":"manual","customField":"keep-me","headers":{"Authorization":"${TOKEN}"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	adapter := NewClaudeAdapter(root)
	if err := adapter.Render(model.Definition{Name: "docs", Command: "npx"}); err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if err := adapter.Remove("docs"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	data := string(mustReadRenderFile(t, path))
	if strings.Contains(data, "docs") || !strings.Contains(data, "customField") || !strings.Contains(data, "keep-me") || !strings.Contains(data, "Authorization") {
		t.Fatalf("Claude config = %s, want unrelated MCP fields preserved and managed docs removed", data)
	}
}

func TestAdaptersUseTargetSpecificOverrides(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	def := model.Definition{
		Name:    "docs",
		Command: "base-command",
		Args:    []string{"base"},
		Adapters: map[string]model.AdapterConfig{
			"claude": {Command: "claude-command", Args: []string{"claude"}, Env: map[string]string{"CLAUDE_ENV": "1"}},
			"codex":  {Command: "codex-command", Args: []string{"codex"}, Env: map[string]string{"CODEX_ENV": "1"}},
		},
	}
	if err := NewClaudeAdapter(root).Render(def); err != nil {
		t.Fatalf("Claude Render() error = %v", err)
	}
	claudeData := string(mustReadRenderFile(t, filepath.Join(root, ".mcp.json")))
	if !strings.Contains(claudeData, "claude-command") || strings.Contains(claudeData, "codex-command") || !strings.Contains(claudeData, "CLAUDE_ENV") {
		t.Fatalf("Claude render did not use claude override: %s", claudeData)
	}
	if err := NewCodexAdapter(root).Render(def); err != nil {
		t.Fatalf("Codex Render() error = %v", err)
	}
	codexData := string(mustReadRenderFile(t, filepath.Join(root, ".codex", "config.toml")))
	if !strings.Contains(codexData, "codex-command") || strings.Contains(codexData, "claude-command") || !strings.Contains(codexData, "CODEX_ENV") {
		t.Fatalf("Codex render did not use codex override: %s", codexData)
	}
}

func TestAdaptersRefuseUnmanagedOverwriteAndPreserveOnRemove(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	claudePath := filepath.Join(root, ".mcp.json")
	if err := os.WriteFile(claudePath, []byte(`{"mcpServers":{"docs":{"command":"manual"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	claude := NewClaudeAdapter(root)
	if err := claude.Render(model.Definition{Name: "docs", Command: "npx"}); err == nil {
		t.Fatal("Claude Render() error = nil, want unmanaged overwrite refusal")
	}
	if err := claude.Remove("docs"); err != nil {
		t.Fatalf("Claude Remove() error = %v", err)
	}
	if data := string(mustReadRenderFile(t, claudePath)); !strings.Contains(data, "manual") {
		t.Fatalf("Claude Remove() changed unmanaged entry: %s", data)
	}

	codexPath := filepath.Join(root, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(codexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte("[mcp_servers.docs]\ncommand = \"manual\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	codex := NewCodexAdapter(root)
	if err := codex.Render(model.Definition{Name: "docs", Command: "npx"}); err == nil {
		t.Fatal("Codex Render() error = nil, want unmanaged overwrite refusal")
	}
	if err := codex.Remove("docs"); err != nil {
		t.Fatalf("Codex Remove() error = %v", err)
	}
	if data := string(mustReadRenderFile(t, codexPath)); !strings.Contains(data, "manual") {
		t.Fatalf("Codex Remove() changed unmanaged entry: %s", data)
	}
}

func TestCodexAdapterRemoveManagedEntryPreservesRootSettings(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	path := filepath.Join(root, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model = \"gpt-5\"\n\n[mcp_servers.existing]\ntype = \"http\"\nheaders = { Authorization = \"${TOKEN}\" }\n\n[mcp_servers.docs]\ncommand = \"npx\"\n_graft_managed = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := NewCodexAdapter(root).Remove("docs"); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	data := string(mustReadRenderFile(t, path))
	if strings.Contains(data, "docs") || !strings.Contains(data, `model = "gpt-5"`) || !strings.Contains(data, "Authorization") {
		t.Fatalf("Codex Remove() = %s, want managed docs removed and unrelated settings preserved", data)
	}
}

func mustReadRenderFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
