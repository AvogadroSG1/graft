package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/poconnor/graft/internal/library"
	"github.com/poconnor/graft/internal/model"
)

func runAddInteractive(t *testing.T, root, cfgPath, stdin string) (string, error) {
	t.Helper()
	cmd := NewRootCommand(context.Background())
	cmd.SetArgs([]string{"--config", cfgPath, "--root", root, "add-interactive"})
	cmd.SetIn(strings.NewReader(stdin))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	return out.String(), err
}

func TestAddInteractiveStdioHappyPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	// Name, Description, Version (blank -> default), Type (stdio), Command,
	// Args, Env (blank ends), Tags.
	stdin := "docs\nDocs server\n\nstdio\nnpx\n@acme/docs --stdio\n\ndocs,search\n"

	out, err := runAddInteractive(t, root, cfgPath, stdin)
	if err != nil {
		t.Fatalf("Execute() error = %v\nout=%s", err, out)
	}
	def := readAuthoringDefinition(t, lib, "docs")
	if def.Version != "0.1.0" {
		t.Fatalf("version = %q, want default 0.1.0", def.Version)
	}
	if def.Description != "Docs server" {
		t.Fatalf("description = %q, want Docs server", def.Description)
	}
	if def.Command != "npx" {
		t.Fatalf("command = %q, want npx", def.Command)
	}
	if strings.Join(def.Args, " ") != "@acme/docs --stdio" {
		t.Fatalf("args = %v, want [@acme/docs --stdio]", def.Args)
	}
	if strings.Join(def.Tags, ",") != "docs,search" {
		t.Fatalf("tags = %v, want [docs search]", def.Tags)
	}
	if !strings.Contains(out, "added docs") {
		t.Fatalf("stdout = %q, want added docs", out)
	}
}

func TestAddInteractiveHTTPPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	// Name, Description, Version, Type (http), URL, Header, blank, Env blank, Tags blank.
	stdin := "remote\n\n1.2.0\nhttp\nhttps://example.com/mcp\nX-Trace=on\n\n\n\n"

	if _, err := runAddInteractive(t, root, cfgPath, stdin); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	def := readAuthoringDefinition(t, lib, "remote")
	if def.Type != "http" {
		t.Fatalf("type = %q, want http", def.Type)
	}
	if def.URL != "https://example.com/mcp" {
		t.Fatalf("url = %q, want https://example.com/mcp", def.URL)
	}
	if def.Headers["X-Trace"] != "on" {
		t.Fatalf("headers = %v, want X-Trace=on", def.Headers)
	}
}

func TestAddInteractiveRedactsSecretEnv(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	// stdio with an env secret and a benign env var.
	stdin := "docs\n\n\nstdio\nnpx\n\nAPI_TOKEN=sk-real\nLOG_LEVEL=debug\n\n\n"

	out, err := runAddInteractive(t, root, cfgPath, stdin)
	if err != nil {
		t.Fatalf("Execute() error = %v\nout=%s", err, out)
	}
	def := readAuthoringDefinition(t, lib, "docs")
	if def.Env["API_TOKEN"] != "${API_TOKEN}" {
		t.Fatalf("API_TOKEN = %q, want redacted ${API_TOKEN}", def.Env["API_TOKEN"])
	}
	if def.Env["LOG_LEVEL"] != "debug" {
		t.Fatalf("LOG_LEVEL = %q, want literal debug", def.Env["LOG_LEVEL"])
	}
	if !strings.Contains(out, "redacted secret(s)") || !strings.Contains(out, "API_TOKEN") {
		t.Fatalf("stdout = %q, want redaction report for API_TOKEN", out)
	}
}

func TestAddInteractiveInvalidNameRecovers(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	// First name is blank (re-prompt), then a valid one.
	stdin := "\nvalid-name\n\n\nstdio\nnpx\n\n\n\n"

	out, err := runAddInteractive(t, root, cfgPath, stdin)
	if err != nil {
		t.Fatalf("Execute() error = %v\nout=%s", err, out)
	}
	if def := readAuthoringDefinition(t, lib, "valid-name"); def.Command != "npx" {
		t.Fatalf("command = %q, want npx", def.Command)
	}
	if !strings.Contains(out, "name is required") {
		t.Fatalf("stdout = %q, want name-required re-prompt", out)
	}
}

func TestAddInteractiveCollisionOverwrite(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	if _, err := library.WriteDefinition(lib, model.Definition{Name: "docs", Version: "1.0.0", Command: "old"}); err != nil {
		t.Fatal(err)
	}

	// Answering N to the overwrite prompt aborts without changing the file.
	declineStdin := "docs\n\n\nstdio\nnew\n\n\n\nN\n"
	if _, err := runAddInteractive(t, root, cfgPath, declineStdin); err == nil {
		t.Fatal("Execute() declined overwrite error = nil")
	}
	if def := readAuthoringDefinition(t, lib, "docs"); def.Command != "old" {
		t.Fatalf("command = %q, want unchanged old", def.Command)
	}

	// Answering y overwrites and reindexes.
	acceptStdin := "docs\n\n\nstdio\nnew\n\n\n\ny\n"
	if _, err := runAddInteractive(t, root, cfgPath, acceptStdin); err != nil {
		t.Fatalf("Execute() accept overwrite error = %v", err)
	}
	if def := readAuthoringDefinition(t, lib, "docs"); def.Command != "new" {
		t.Fatalf("command = %q, want overwritten new", def.Command)
	}
}
