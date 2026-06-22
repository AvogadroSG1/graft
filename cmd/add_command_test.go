package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/poconnor/graft/internal/library"
	"github.com/poconnor/graft/internal/model"
)

func runAdd(t *testing.T, root, cfgPath, stdin string, extraArgs ...string) (string, error) {
	t.Helper()
	cmd := NewRootCommand(context.Background())
	args := append([]string{"--config", cfgPath, "--root", root, "add"}, extraArgs...)
	cmd.SetArgs(args)
	cmd.SetIn(strings.NewReader(stdin))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	err := cmd.Execute()
	return out.String(), err
}

func TestAddPastedWrappedRedactsLiteralSecret(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	stdin := `{"mcpServers":{"docs":{"command":"npx","args":["@acme/docs","--stdio"],"env":{"API_TOKEN":"sk-real","LOG_LEVEL":"debug"}}}}`

	out, err := runAdd(t, root, cfgPath, stdin)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	def := readAuthoringDefinition(t, lib, "docs")
	if def.Env["API_TOKEN"] != "${API_TOKEN}" {
		t.Fatalf("API_TOKEN = %q, want redacted ${API_TOKEN}", def.Env["API_TOKEN"])
	}
	if def.Env["LOG_LEVEL"] != "debug" {
		t.Fatalf("LOG_LEVEL = %q, want literal debug", def.Env["LOG_LEVEL"])
	}
	if !strings.Contains(out, "added docs") || !strings.Contains(out, "API_TOKEN") {
		t.Fatalf("stdout = %q, want added docs + redacted API_TOKEN", out)
	}
}

func TestAddKeepsExistingReference(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	stdin := `{"mcpServers":{"db":{"command":"db-mcp","env":{"DB_TOKEN":"${DB_TOKEN}"}}}}`

	out, err := runAdd(t, root, cfgPath, stdin)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	def := readAuthoringDefinition(t, lib, "db")
	if def.Env["DB_TOKEN"] != "${DB_TOKEN}" {
		t.Fatalf("DB_TOKEN = %q, want reference kept verbatim", def.Env["DB_TOKEN"])
	}
	if strings.Contains(out, "redacted") {
		t.Fatalf("stdout = %q, want no redaction reported for existing reference", out)
	}
}

func TestAddBareWithPositionalName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)

	if _, err := runAdd(t, root, cfgPath, `{"command":"foo","args":["--stdio"]}`, "foo"); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	def := readAuthoringDefinition(t, lib, "foo")
	if def.Command != "foo" {
		t.Fatalf("command = %q, want foo", def.Command)
	}
}

func TestAddBareWithoutNameErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)

	if _, err := runAdd(t, root, cfgPath, `{"command":"foo"}`); err == nil {
		t.Fatal("Execute() missing-name error = nil")
	}
}

func TestAddRejectsInvalid(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"stdio no command": `{"mcpServers":{"bad":{"args":["x"]}}}`,
		"unknown type":     `{"mcpServers":{"bad":{"type":"grpc","command":"x"}}}`,
		"remote no url":    `{"mcpServers":{"bad":{"type":"http"}}}`,
		"malformed":        `{not json`,
	}
	for name, stdin := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			lib := testAuthoringLibrary(t, root)
			cfgPath := writeAuthoringConfig(t, root, lib)
			if _, err := runAdd(t, root, cfgPath, stdin); err == nil {
				t.Fatalf("Execute() %s error = nil", name)
			}
			if _, err := os.Stat(filepath.Join(lib.CachePath, "mcps", "bad.json")); err == nil {
				t.Fatalf("%s wrote a definition; want nothing written", name)
			}
		})
	}
}

func TestAddCollisionRequiresForce(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	if _, err := library.WriteDefinition(lib, model.Definition{Name: "docs", Version: "1.0.0", Command: "old"}); err != nil {
		t.Fatal(err)
	}
	stdin := `{"mcpServers":{"docs":{"command":"new"}}}`

	if _, err := runAdd(t, root, cfgPath, stdin); err == nil {
		t.Fatal("Execute() collision without --force error = nil")
	}
	if def := readAuthoringDefinition(t, lib, "docs"); def.Command != "old" {
		t.Fatalf("command = %q, want unchanged old", def.Command)
	}

	if _, err := runAdd(t, root, cfgPath, stdin, "--force"); err != nil {
		t.Fatalf("Execute() with --force error = %v", err)
	}
	if def := readAuthoringDefinition(t, lib, "docs"); def.Command != "new" {
		t.Fatalf("command = %q, want overwritten new", def.Command)
	}
	indexData, err := os.ReadFile(filepath.Join(lib.CachePath, "library.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(indexData), "docs") {
		t.Fatalf("library.json = %q, want reindexed docs", string(indexData))
	}
}

func TestAddMultiCollisionWritesNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	// "two" already exists; a wrapped paste of {one, two} must fail without
	// writing "one" first (no partial state).
	if _, err := library.WriteDefinition(lib, model.Definition{Name: "two", Version: "1.0.0", Command: "old"}); err != nil {
		t.Fatal(err)
	}
	stdin := `{"mcpServers":{"one":{"command":"a"},"two":{"command":"b"}}}`

	if _, err := runAdd(t, root, cfgPath, stdin); err == nil {
		t.Fatal("Execute() multi-collision error = nil")
	}
	if _, err := os.Stat(filepath.Join(lib.CachePath, "mcps", "one.json")); err == nil {
		t.Fatal("one.json written despite collision on two; want nothing written")
	}
	if def := readAuthoringDefinition(t, lib, "two"); def.Command != "old" {
		t.Fatalf("two command = %q, want unchanged old", def.Command)
	}
}

func TestAddFromFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lib := testAuthoringLibrary(t, root)
	cfgPath := writeAuthoringConfig(t, root, lib)
	src := filepath.Join(root, "snippet.json")
	if err := os.WriteFile(src, []byte(`{"mcpServers":{"docs":{"command":"npx"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runAdd(t, root, cfgPath, "", "--file", src); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if def := readAuthoringDefinition(t, lib, "docs"); def.Command != "npx" {
		t.Fatalf("command = %q, want npx from file", def.Command)
	}
}
