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

	"github.com/poconnor/graft/internal/config"
	"github.com/poconnor/graft/internal/library"
	"github.com/poconnor/graft/internal/model"
)

func TestMCPImportPromptsForCollisionUseNew(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	if _, err := library.WriteDefinition(lib, model.Definition{Name: "docs", Version: "1.0.0", Command: "old"}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source.json")
	if err := os.WriteFile(source, []byte(`{"mcpServers":{"docs":{"command":"new","args":["serve"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCommand(context.Background())
	cmd.SetArgs([]string{"--config", cfgPath, "--root", root, "mcp", "import", "--from", source})
	cmd.SetIn(strings.NewReader("use-new\n"))
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	def := readAuthoringDefinition(t, lib, "docs")
	if def.Command != "new" || len(def.Args) != 1 || def.Args[0] != "serve" {
		t.Fatalf("definition = %+v, want imported replacement", def)
	}
	if !strings.Contains(out.String(), "imported docs") {
		t.Fatalf("stdout = %q, want imported docs", out.String())
	}
}

func TestMCPAddPromptsForFields(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	cmd := NewRootCommand(context.Background())
	cmd.SetArgs([]string{"--config", cfgPath, "--root", root, "mcp", "add", "docs"})
	cmd.SetIn(strings.NewReader("Documentation server\n1.2.3\n\nnpx\ndocs --stdio\ntools,docs\n"))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	def := readAuthoringDefinition(t, lib, "docs")
	if def.Description != "Documentation server" || def.Version != "1.2.3" || def.Command != "npx" {
		t.Fatalf("definition = %+v, want prompted fields", def)
	}
	if len(def.Args) != 2 || def.Args[0] != "docs" || def.Args[1] != "--stdio" {
		t.Fatalf("args = %+v, want split prompted args", def.Args)
	}
	if len(def.Tags) != 2 || def.Tags[0] != "tools" || def.Tags[1] != "docs" {
		t.Fatalf("tags = %+v, want prompted tags", def.Tags)
	}
}

func TestMCPEditRunsEditorAndReindexes(t *testing.T) {
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	if _, err := library.WriteDefinition(lib, model.Definition{Name: "docs", Version: "1.0.0", Command: "old"}); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(root, "editor.sh")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '%s\\n' '{\"name\":\"docs\",\"version\":\"2.0.0\",\"description\":\"edited\",\"tags\":[],\"command\":\"new\",\"args\":[],\"env\":{},\"headers\":{},\"adapters\":{}}' > \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GRAFT_EDITOR", editor)
	cmd := NewRootCommand(context.Background())
	cmd.SetArgs([]string{"--config", cfgPath, "--root", root, "mcp", "edit", "docs"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	def := readAuthoringDefinition(t, lib, "docs")
	if def.Command != "new" || def.Version != "2.0.0" {
		t.Fatalf("definition = %+v, want editor changes", def)
	}
	if _, err := os.Stat(filepath.Join(lib.CachePath, "library.json")); err != nil {
		t.Fatalf("library.json not reindexed: %v", err)
	}
}

func TestMCPImportCollisionKeepAndSkip(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		choice  string
		command string
	}{
		{name: "keep", choice: "keep\n", command: "old"},
		{name: "skip", choice: "skip\n", command: "old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			lib := testAuthoringLibrary(t, root)
			cfgPath := writeAuthoringConfig(t, root, lib)
			if _, err := library.WriteDefinition(lib, model.Definition{Name: "docs", Version: "1.0.0", Command: "old"}); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(root, "source.json")
			if err := os.WriteFile(source, []byte(`{"mcpServers":{"docs":{"command":"new"}}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := NewRootCommand(context.Background())
			cmd.SetArgs([]string{"--config", cfgPath, "--root", root, "mcp", "import", "--from", source})
			cmd.SetIn(strings.NewReader(tc.choice))
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if got := readAuthoringDefinition(t, lib, "docs").Command; got != tc.command {
				t.Fatalf("command = %q, want %q", got, tc.command)
			}
		})
	}
}

func TestMCPImportEditorCollisionPreservesExistingOnInvalidEdit(t *testing.T) {
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	if _, err := library.WriteDefinition(lib, model.Definition{Name: "docs", Version: "1.0.0", Command: "old"}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source.json")
	if err := os.WriteFile(source, []byte(`{"mcpServers":{"docs":{"command":"new"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(root, "bad-editor.sh")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '{bad json' > \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GRAFT_EDITOR", editor)
	cmd := NewRootCommand(context.Background())
	cmd.SetArgs([]string{"--config", cfgPath, "--root", root, "mcp", "import", "--from", source})
	cmd.SetIn(strings.NewReader("editor\n"))
	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want invalid editor JSON error")
	}
	if got := readAuthoringDefinition(t, lib, "docs").Command; got != "old" {
		t.Fatalf("command = %q, want existing definition preserved", got)
	}
}

func TestMCPImportEditorCollisionValidEditReplacesAfterValidation(t *testing.T) {
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	if _, err := library.WriteDefinition(lib, model.Definition{Name: "docs", Version: "1.0.0", Command: "old"}); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source.json")
	if err := os.WriteFile(source, []byte(`{"mcpServers":{"docs":{"command":"new"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(root, "editor.sh")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '%s\\n' '{\"name\":\"docs\",\"version\":\"3.0.0\",\"description\":\"edited\",\"tags\":[],\"command\":\"edited\",\"args\":[],\"env\":{},\"headers\":{},\"adapters\":{}}' > \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GRAFT_EDITOR", editor)
	cmd := NewRootCommand(context.Background())
	cmd.SetArgs([]string{"--config", cfgPath, "--root", root, "mcp", "import", "--from", source})
	cmd.SetIn(strings.NewReader("editor\n"))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	def := readAuthoringDefinition(t, lib, "docs")
	if def.Command != "edited" || def.Version != "3.0.0" {
		t.Fatalf("definition = %+v, want valid editor replacement", def)
	}
}

func TestMCPImportAuthPromptDeclineSkipsDefinition(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	source := filepath.Join(root, "source.json")
	if err := os.WriteFile(source, []byte(`{"mcpServers":{"docs":{"command":"npx","env":{"TOKEN":"secret"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := NewRootCommand(context.Background())
	cmd.SetArgs([]string{"--config", cfgPath, "--root", root, "mcp", "import", "--from", source})
	cmd.SetIn(strings.NewReader("n\n"))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib.CachePath, "mcps", "docs.json")); !os.IsNotExist(err) {
		t.Fatalf("docs.json exists or unexpected error: %v", err)
	}
}

func TestMCPAddFlagsRunNonInteractively(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	cmd := NewRootCommand(context.Background())
	cmd.SetArgs([]string{"--config", cfgPath, "--root", root, "mcp", "add", "docs", "--description", "Docs", "--version", "2.0.0", "--type", "stdio", "--command", "npx", "--args", "docs --stdio", "--tags", "tools,docs"})
	cmd.SetIn(strings.NewReader(""))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	def := readAuthoringDefinition(t, lib, "docs")
	if def.Version != "2.0.0" || def.Type != "stdio" || def.Command != "npx" || len(def.Args) != 2 || len(def.Tags) != 2 {
		t.Fatalf("definition = %+v, want noninteractive flag values", def)
	}
}

func TestMCPEditRejectsInvalidJSONAndNameMismatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "invalid-json", content: "{bad json"},
		{name: "name-mismatch", content: `{"name":"other","version":"1","command":"npx"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			lib := testAuthoringLibrary(t, root)
			cfgPath := writeAuthoringConfig(t, root, lib)
			if _, err := library.WriteDefinition(lib, model.Definition{Name: "docs", Version: "1.0.0", Command: "old"}); err != nil {
				t.Fatal(err)
			}
			editor := filepath.Join(root, "editor.sh")
			if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf '%s\\n' '"+tc.content+"' > \"$1\"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("GRAFT_EDITOR", editor)
			cmd := NewRootCommand(context.Background())
			cmd.SetArgs([]string{"--config", cfgPath, "--root", root, "mcp", "edit", "docs"})
			if err := cmd.Execute(); err == nil {
				t.Fatal("Execute() error = nil, want validation error")
			}
		})
	}
}

func TestMCPPushYesCommitsAndPushes(t *testing.T) {
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	remote := filepath.Join(root, "remote.git")
	runGit(t, root, "init", "--bare", remote)
	runGit(t, lib.CachePath, "init")
	runGit(t, lib.CachePath, "config", "user.name", "test")
	runGit(t, lib.CachePath, "config", "user.email", "test@example.invalid")
	runGit(t, lib.CachePath, "remote", "add", "origin", remote)
	if _, err := library.WriteDefinition(lib, model.Definition{Name: "docs", Version: "1.0.0", Command: "npx"}); err != nil {
		t.Fatal(err)
	}
	if _, err := (library.GitClient{}).Reindex(lib); err != nil {
		t.Fatal(err)
	}
	runGit(t, lib.CachePath, "add", ".")
	runGit(t, lib.CachePath, "commit", "-m", "initial")
	runGit(t, lib.CachePath, "push", "-u", "origin", "HEAD")
	if err := os.WriteFile(filepath.Join(lib.CachePath, "secret.txt"), []byte("do-not-publish"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, lib.CachePath, "add", "secret.txt")
	if _, err := library.WriteDefinitionFile(lib, model.Definition{Name: "docs", Version: "2.0.0", Command: "npx"}, true); err != nil {
		t.Fatal(err)
	}

	cmd := NewRootCommand(context.Background())
	cmd.SetArgs([]string{"--config", cfgPath, "--root", root, "mcp", "push", "--yes"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(out.String(), "diff --git") || !strings.Contains(out.String(), "commit") {
		t.Fatalf("stdout = %q, want diff and commit sha", out.String())
	}
	message := strings.TrimSpace(string(runGitOutput(t, lib.CachePath, "log", "-1", "--pretty=%s")))
	if message != "feat(mcps): update library definitions" {
		t.Fatalf("commit message = %q", message)
	}
	localHead := strings.TrimSpace(string(runGitOutput(t, lib.CachePath, "rev-parse", "HEAD")))
	remoteHead := strings.TrimSpace(string(runGitOutput(t, remote, "rev-parse", "HEAD")))
	if localHead == "" || localHead != remoteHead {
		t.Fatalf("local HEAD %q remote HEAD %q, want pushed commit", localHead, remoteHead)
	}
	cmdCheck := exec.Command("git", "-C", remote, "show", "HEAD:secret.txt")
	if err := cmdCheck.Run(); err == nil {
		t.Fatal("secret.txt was pushed; want push scoped to library-managed files")
	}
}

func testAuthoringLibrary(t *testing.T, root string) config.Library {
	t.Helper()
	path := filepath.Join(root, "core")
	if err := os.MkdirAll(filepath.Join(path, "mcps"), 0o755); err != nil {
		t.Fatal(err)
	}
	return config.Library{Name: "core", URL: path, CachePath: path, Default: true}
}

func writeAuthoringConfig(t *testing.T, root string, lib config.Library) string {
	t.Helper()
	path := filepath.Join(root, "config.json")
	if err := (config.FileStore{}).Save(path, config.Config{Libraries: []config.Library{lib}}); err != nil {
		t.Fatal(err)
	}
	return path
}

func readAuthoringDefinition(t *testing.T, lib config.Library, name string) model.Definition {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(lib.CachePath, "mcps", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var def model.Definition
	if err := json.Unmarshal(data, &def); err != nil {
		t.Fatal(err)
	}
	return def
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}
