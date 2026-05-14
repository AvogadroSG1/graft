package claudecfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/poconnor/graft/internal/render"
)

func TestLoadGroupsGlobalLocalAndProjectStdioMCPs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "claude.json")
	content := `{
  "mcpServers": {
    "global-docs": {
      "command": "npx",
      "args": ["docs"],
      "env": {"TOKEN": "secret-token"}
    }
  },
  "projects": {
    "/work/api": {
      "mcpServers": {
        "local-db": {
          "command": "uvx",
          "args": ["db"],
          "env": {"PASSWORD": "secret-password"}
        }
      }
    }
  }
}`
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	projectRoot := filepath.Join(root, "project")
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	projectConfig := `{"mcpServers":{"project-search":{"command":"node","args":["search.js"],"env":{"API_KEY":"secret-key"}}}}`
	if err := os.WriteFile(filepath.Join(projectRoot, ".mcp.json"), []byte(projectConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	groups, err := Load(source, projectRoot)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(groups) != 3 {
		t.Fatalf("Load() returned %d groups, want 3: %+v", len(groups), groups)
	}
	if groups[0].Scope != ScopeGlobal || groups[0].Name != "global" || groups[0].MCPs[0].Name != "global-docs" {
		t.Fatalf("global group = %+v", groups[0])
	}
	if got := groups[0].MCPs[0].Definition.Env["TOKEN"]; got != "${TOKEN}" {
		t.Fatalf("global TOKEN = %q, want placeholder", got)
	}
	if groups[1].Scope != ScopeLocal || groups[1].Name != "/work/api" || groups[1].MCPs[0].Name != "local-db" {
		t.Fatalf("local group = %+v", groups[1])
	}
	if got := groups[1].MCPs[0].Definition.Env["PASSWORD"]; got != "${PASSWORD}" {
		t.Fatalf("local PASSWORD = %q, want placeholder", got)
	}
	if groups[2].Scope != ScopeProject || groups[2].Name != ".mcp.json" || groups[2].MCPs[0].Name != "project-search" {
		t.Fatalf("project group = %+v", groups[2])
	}
	if got := groups[2].MCPs[0].Definition.Env["API_KEY"]; got != "${API_KEY}" {
		t.Fatalf("project API_KEY = %q, want placeholder", got)
	}
}

func TestLoadSkipsCommandlessServersWithoutExplicitRemoteType(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "claude.json")
	content := `{"mcpServers":{"http-docs":{"url":"https://example.com/mcp"}}}`
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	groups, err := Load(source, root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("Load() = %+v, want commandless MCP skipped", groups)
	}
}

func TestLoadParsesHTTPMCPsAndRedactsHeaders(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "claude.json")
	content := `{
  "mcpServers": {
    "remote": {
      "type": "http",
      "url": "https://example.com/mcp",
      "headers": {"Authorization": "Bearer secret"}
    }
  }
}`
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	groups, err := Load(source, root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(groups) != 1 || len(groups[0].MCPs) != 1 {
		t.Fatalf("Load() = %+v, want one HTTP MCP", groups)
	}
	def := groups[0].MCPs[0].Definition
	if def.Type != "http" || def.URL != "https://example.com/mcp" {
		t.Fatalf("definition transport = (%q, %q), want HTTP URL", def.Type, def.URL)
	}
	if def.Headers["Authorization"] != "${Authorization}" {
		t.Fatalf("headers = %+v, want placeholder", def.Headers)
	}
}

func TestLoadParsesRemoteMCPsAtAllScopes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "claude.json")
	content := `{
  "mcpServers": {
    "global-http": {
      "type": "http",
      "url": "https://example.com/global",
      "headers": {"Authorization": "Bearer global-secret"}
    }
  },
  "projects": {
    "/work/api": {
      "mcpServers": {
        "local-sse": {
          "type": "sse",
          "url": "https://example.com/local",
          "headers": {"X-API-Key": "local-secret"}
        }
      }
    }
  }
}`
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	projectConfig := `{"mcpServers":{"project-http":{"type":"http","url":"https://example.com/project","headers":{"Authorization":"Bearer project-secret"}}}}`
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(projectConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	groups, err := Load(source, root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := map[string]struct {
		transport string
		url       string
		headerKey string
		headerVal string
	}{
		"global-http":  {transport: "http", url: "https://example.com/global", headerKey: "Authorization", headerVal: "${Authorization}"},
		"local-sse":    {transport: "sse", url: "https://example.com/local", headerKey: "X-API-Key", headerVal: "${X-API-Key}"},
		"project-http": {transport: "http", url: "https://example.com/project", headerKey: "Authorization", headerVal: "${Authorization}"},
	}
	for _, group := range groups {
		for _, mcp := range group.MCPs {
			expected, ok := want[mcp.Name]
			if !ok {
				t.Fatalf("unexpected MCP %s in groups %+v", mcp.Name, groups)
			}
			delete(want, mcp.Name)
			def := mcp.Definition
			if def.Type != expected.transport || def.URL != expected.url {
				t.Fatalf("%s transport = (%q, %q), want (%q, %q)", mcp.Name, def.Type, def.URL, expected.transport, expected.url)
			}
			if def.Headers[expected.headerKey] != expected.headerVal {
				t.Fatalf("%s headers = %+v, want %s", mcp.Name, def.Headers, expected.headerVal)
			}
			data, err := json.Marshal(def)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), "secret") {
				t.Fatalf("%s leaked raw secret in definition: %s", mcp.Name, data)
			}
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing remote MCPs: %+v", want)
	}
}

func TestLoadHTTPRoundTripRendersClaudeAndCodex(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source := filepath.Join(root, "claude.json")
	content := `{"mcpServers":{"remote":{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer secret"}}}}`
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	groups, err := Load(source, root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	def := groups[0].MCPs[0].Definition
	if err := render.NewClaudeAdapter(root).Render(def); err != nil {
		t.Fatalf("Claude Render() error = %v", err)
	}
	if err := render.NewCodexAdapter(root).Render(def); err != nil {
		t.Fatalf("Codex Render() error = %v", err)
	}
	claudeData, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var claudeDoc struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
			Command string            `json:"command"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(claudeData, &claudeDoc); err != nil {
		t.Fatalf("parse Claude output: %v", err)
	}
	claudeServer := claudeDoc.MCPServers["remote"]
	if claudeServer.Type != "http" || claudeServer.URL != "https://example.com/mcp" || claudeServer.Headers["Authorization"] != "${Authorization}" || claudeServer.Command != "" {
		t.Fatalf("Claude output = %+v, want redacted HTTP transport only", claudeServer)
	}
	codexData, err := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var codexDoc struct {
		MCPServers map[string]struct {
			Type    string            `toml:"type"`
			URL     string            `toml:"url"`
			Headers map[string]string `toml:"headers"`
			Command string            `toml:"command"`
		} `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(string(codexData), &codexDoc); err != nil {
		t.Fatalf("parse Codex output: %v", err)
	}
	codexServer := codexDoc.MCPServers["remote"]
	if codexServer.Type != "http" || codexServer.URL != "https://example.com/mcp" || codexServer.Command != "" || codexServer.Headers != nil {
		t.Fatalf("Codex output = %+v, want HTTP type/url only", codexServer)
	}
}

func TestDefaultPathUsesClaudeConfigDirBeforeHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath() error = %v", err)
	}
	want := filepath.Join(dir, "claude.json")
	if got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}
