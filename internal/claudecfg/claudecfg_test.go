package claudecfg

import (
	"os"
	"path/filepath"
	"testing"
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

func TestLoadSkipsCommandlessServersInStdioOnlyMode(t *testing.T) {
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

func TestDefaultPathUsesClaudeConfigDirBeforeHome(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/claude-config")
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath() error = %v", err)
	}
	if got != "/tmp/claude-config/claude.json" {
		t.Fatalf("DefaultPath() = %q", got)
	}
}
