package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLibraryMigrateFromClaudeDryRunDoesNotWriteOrPrompt(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	source := filepath.Join(root, "claude.json")
	content := `{
  "mcpServers": {"global-docs": {"command": "npx", "args": ["docs"]}},
  "projects": {"/work/api": {"mcpServers": {"local-db": {"command": "uvx", "args": ["db"]}}}}
}`
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	command := NewRootCommand(context.Background())
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetArgs([]string{"--config", configPath, "--root", root, "library", "migrate-from-claude", "imported", "--from", source, "--dry-run"})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out.String(), "global-docs\tglobal\twould import") {
		t.Fatalf("dry-run output missing global import: %s", out.String())
	}
	if !strings.Contains(out.String(), "local-db\t/work/api\twould prompt") {
		t.Fatalf("dry-run output missing local prompt: %s", out.String())
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote config path: %v", err)
	}
	cachePath := filepath.Join(root, "cache", "graft", "libraries", "imported")
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote cache path: %v", err)
	}
}

func TestLibraryMigrateFromClaudeCreatesLibraryAndRegistersIt(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	configPath := filepath.Join(root, "config.json")
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
	command := NewRootCommand(context.Background())
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetIn(strings.NewReader("y\n"))
	command.SetArgs([]string{"--config", configPath, "--root", root, "library", "migrate-from-claude", "imported", "--from", source})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	cachePath := filepath.Join(root, "cache", "graft", "libraries", "imported")
	definitionPath := filepath.Join(cachePath, "mcps", "global-docs.json")
	data, err := os.ReadFile(definitionPath)
	if err != nil {
		t.Fatalf("read definition: %v", err)
	}
	if strings.Contains(string(data), "secret-token") || !strings.Contains(string(data), `"TOKEN": "${TOKEN}"`) {
		t.Fatalf("definition did not redact env secret: %s", data)
	}
	localPath := filepath.Join(cachePath, "mcps", "local-db.json")
	if _, err := os.Stat(localPath); err != nil {
		t.Fatalf("approved local definition missing: %v", err)
	}
	logOut, err := exec.Command("git", "-C", cachePath, "log", "--oneline", "-1").Output()
	if err != nil {
		t.Fatalf("read git log: %v", err)
	}
	if !strings.Contains(string(logOut), "Initial import from ~/.claude.json") {
		t.Fatalf("git log = %s", logOut)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var cfg struct {
		Libraries []struct {
			Name      string `json:"name"`
			CachePath string `json:"cache_path"`
			Default   bool   `json:"default"`
		} `json:"libraries"`
	}
	if err := json.Unmarshal(configData, &cfg); err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if len(cfg.Libraries) != 1 || cfg.Libraries[0].Name != "imported" || !cfg.Libraries[0].Default {
		t.Fatalf("config = %+v, want imported as default", cfg)
	}
}

func TestLibraryMigrateFromClaudeForceWipesExistingCache(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	configPath := filepath.Join(root, "config.json")
	source := filepath.Join(root, "claude.json")
	if err := os.WriteFile(source, []byte(`{"mcpServers":{"docs":{"command":"npx","args":["docs"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(root, "cache", "graft", "libraries", "imported")
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(cachePath, "stale.txt")
	if err := os.WriteFile(stalePath, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	command := NewRootCommand(context.Background())
	command.SetArgs([]string{"--config", configPath, "--root", root, "library", "migrate-from-claude", "imported", "--from", source})
	if err := command.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want existing cache failure")
	}

	command = NewRootCommand(context.Background())
	command.SetArgs([]string{"--config", configPath, "--root", root, "library", "migrate-from-claude", "imported", "--from", source, "--force"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() with --force error = %v", err)
	}
	if _, err := os.Stat(stalePath); !os.IsNotExist(err) {
		t.Fatalf("--force left stale file: %v", err)
	}
}

func TestLibraryMigrateFromClaudeRejectsUnsafeLibraryNameBeforeForceDelete(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	configPath := filepath.Join(root, "config.json")
	source := filepath.Join(root, "claude.json")
	if err := os.WriteFile(source, []byte(`{"mcpServers":{"docs":{"command":"npx","args":["docs"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sentinelDir := filepath.Join(root, "cache", "graft", "escape")
	if err := os.MkdirAll(sentinelDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(sentinelDir, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	command := NewRootCommand(context.Background())
	command.SetArgs([]string{"--config", configPath, "--root", root, "library", "migrate-from-claude", "../escape", "--from", source, "--force"})
	if err := command.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want unsafe library name rejected")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("unsafe library name removed sentinel: %v", err)
	}
}

func TestLibraryMigrateFromClaudePreservesExistingDefault(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	configPath := filepath.Join(root, "config.json")
	source := filepath.Join(root, "claude.json")
	if err := os.WriteFile(source, []byte(`{"mcpServers":{"docs":{"command":"npx","args":["docs"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(root, "cache", "graft", "libraries", "imported")
	if err := os.MkdirAll(cachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	configContent := `{"libraries":[{"name":"imported","url":"` + cachePath + `","cache_path":"` + cachePath + `","default":true}]}`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatal(err)
	}

	command := NewRootCommand(context.Background())
	command.SetArgs([]string{"--config", configPath, "--root", root, "library", "migrate-from-claude", "imported", "--from", source, "--force"})
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(configData), `"default": true`) {
		t.Fatalf("config did not preserve default flag: %s", configData)
	}
}

func TestLibraryMigrateFromClaudeDuplicateWarnsOnStderrAndKeepsGlobal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	configPath := filepath.Join(root, "config.json")
	source := filepath.Join(root, "claude.json")
	content := `{
  "mcpServers": {"docs": {"command": "npx", "args": ["global"]}},
  "projects": {"/work/api": {"mcpServers": {"docs": {"command": "uvx", "args": ["local"]}}}}
}`
	if err := os.WriteFile(source, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	command := NewRootCommand(context.Background())
	var out bytes.Buffer
	var errOut bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&errOut)
	command.SetArgs([]string{"--config", configPath, "--root", root, "library", "migrate-from-claude", "imported", "--from", source})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(out.String(), "warning") {
		t.Fatalf("stdout contains warning: %s", out.String())
	}
	if !strings.Contains(errOut.String(), "warning: skipping duplicate MCP docs from /work/api") {
		t.Fatalf("stderr missing duplicate warning: %s", errOut.String())
	}
	data, err := os.ReadFile(filepath.Join(root, "cache", "graft", "libraries", "imported", "mcps", "docs.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"global"`) || strings.Contains(string(data), `"local"`) {
		t.Fatalf("duplicate did not preserve global definition: %s", data)
	}
}

func TestLibraryMigrateFromClaudeProjectPromptApproveAllOnStderr(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	configPath := filepath.Join(root, "config.json")
	source := filepath.Join(root, "claude.json")
	if err := os.WriteFile(source, []byte(`{"projects":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	projectConfig := `{"mcpServers":{"project-one":{"command":"node","args":["one"]},"project-two":{"command":"node","args":["two"]}}}`
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(projectConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	command := NewRootCommand(context.Background())
	var out bytes.Buffer
	var errOut bytes.Buffer
	command.SetOut(&out)
	command.SetErr(&errOut)
	command.SetIn(strings.NewReader("a\n"))
	command.SetArgs([]string{"--config", configPath, "--root", root, "library", "migrate-from-claude", "imported", "--from", source})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if strings.Contains(out.String(), "[y/n/a]") {
		t.Fatalf("stdout contains prompt: %s", out.String())
	}
	if !strings.Contains(errOut.String(), "Import project-one from .mcp.json? [y/n/a]") {
		t.Fatalf("stderr missing project prompt: %s", errOut.String())
	}
	for _, name := range []string{"project-one", "project-two"} {
		if _, err := os.Stat(filepath.Join(root, "cache", "graft", "libraries", "imported", "mcps", name+".json")); err != nil {
			t.Fatalf("%s not imported after approve-all: %v", name, err)
		}
	}
}

func TestLibraryMigrateFromClaudeIncludesRemoteMCPsInApprovalFlow(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	configPath := filepath.Join(root, "config.json")
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
	command := NewRootCommand(context.Background())
	var errOut bytes.Buffer
	command.SetErr(&errOut)
	command.SetIn(strings.NewReader("a\na\n"))
	command.SetArgs([]string{"--config", configPath, "--root", root, "library", "migrate-from-claude", "imported", "--from", source})

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(errOut.String(), "Import local-sse from /work/api? [y/n/a]") || !strings.Contains(errOut.String(), "Import project-http from .mcp.json? [y/n/a]") {
		t.Fatalf("stderr missing remote prompts: %s", errOut.String())
	}
	cachePath := filepath.Join(root, "cache", "graft", "libraries", "imported", "mcps")
	for _, name := range []string{"global-http", "local-sse", "project-http"} {
		data, err := os.ReadFile(filepath.Join(cachePath, name+".json"))
		if err != nil {
			t.Fatalf("read %s definition: %v", name, err)
		}
		if strings.Contains(string(data), "secret") {
			t.Fatalf("%s definition leaked raw secret: %s", name, data)
		}
		if !strings.Contains(string(data), `"url": "https://example.com/`) {
			t.Fatalf("%s definition missing URL: %s", name, data)
		}
	}
}
