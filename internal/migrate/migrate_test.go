package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyReturnsPendingInputWhenRequiredValueMissing(t *testing.T) {
	t.Parallel()
	for _, auto := range []bool{false, true} {
		t.Run(fmt.Sprintf("auto=%v", auto), func(t *testing.T) {
			doc := map[string]any{"old": "value"}
			pending, err := Apply(doc, []Step{
				{Type: "rename", From: "old", To: "new"},
				{Type: "set_default", Path: "runtime", Value: "node"},
				{Type: "require_input", Path: "token"},
			}, auto, map[string]string{})
			if !errors.Is(err, ErrPendingInput) {
				t.Fatalf("Apply() error = %v, want ErrPendingInput", err)
			}
			if !pending {
				t.Fatal("Apply() pending = false, want true")
			}
			if doc["new"] != "value" || doc["runtime"] != "node" {
				t.Fatalf("Apply() doc = %+v, want migrated values", doc)
			}
		})
	}
}

func TestChainReturnsEmptyWhenVersionsMatch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	chain, err := Chain(root, "docs", "2", "2")
	if err != nil {
		t.Fatalf("Chain() error = %v, want nil", err)
	}
	if len(chain) != 0 {
		t.Fatalf("Chain() len = %d, want 0", len(chain))
	}
}

func TestApplyRenamesNestedPathAndRemovesSource(t *testing.T) {
	t.Parallel()
	doc := map[string]any{"server": map[string]any{"cmd": "npx"}}
	pending, err := Apply(doc, []Step{{Type: "rename", From: "server.cmd", To: "command.name"}}, false, nil)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if pending {
		t.Fatal("Apply() pending = true, want false")
	}
	if _, ok := doc["server"].(map[string]any)["cmd"]; ok {
		t.Fatalf("Apply() left source path: %+v", doc)
	}
	command, ok := doc["command"].(map[string]any)
	if !ok || command["name"] != "npx" {
		t.Fatalf("Apply() doc = %+v, want nested destination", doc)
	}
}

func TestApplyRejectsRenameTargetCollision(t *testing.T) {
	t.Parallel()
	doc := map[string]any{"old": "value", "new": "existing"}
	_, err := Apply(doc, []Step{{Type: "rename", From: "old", To: "new"}}, false, nil)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("Apply() error = %v, want target collision", err)
	}
	if doc["old"] != "value" || doc["new"] != "existing" {
		t.Fatalf("Apply() mutated doc after collision: %+v", doc)
	}
}

func TestApplyRejectsRenameOverlappingPaths(t *testing.T) {
	t.Parallel()
	doc := map[string]any{"a": map[string]any{"b": "value"}}
	_, err := Apply(doc, []Step{{Type: "rename", From: "a", To: "a.b"}}, false, nil)
	if err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("Apply() error = %v, want overlapping path", err)
	}
}

func TestApplySetDefaultKeepsExistingAndCreatesNestedValue(t *testing.T) {
	t.Parallel()
	doc := map[string]any{"env": map[string]any{"token": "existing"}}
	_, err := Apply(doc, []Step{
		{Type: "set_default", Path: "env.token", Value: "new"},
		{Type: "set_default", Path: "env.region", Value: "us"},
	}, false, nil)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	env := doc["env"].(map[string]any)
	if env["token"] != "existing" || env["region"] != "us" {
		t.Fatalf("Apply() env = %+v, want existing token and new region", env)
	}
}

func TestApplyRejectsScalarParentPath(t *testing.T) {
	t.Parallel()
	doc := map[string]any{"env": "scalar"}
	_, err := Apply(doc, []Step{{Type: "set_default", Path: "env.token", Value: "secret"}}, false, nil)
	if err == nil || !strings.Contains(err.Error(), "non-object parent") {
		t.Fatalf("Apply() error = %v, want non-object parent", err)
	}
	if doc["env"] != "scalar" {
		t.Fatalf("Apply() mutated scalar parent: %+v", doc)
	}
}

func TestApplyWithInputPromptsForRequiredInputWhenAuto(t *testing.T) {
	t.Parallel()
	doc := map[string]any{}
	prompted := ""
	pending, err := ApplyWithInput(doc, []Step{
		{Type: "require_input", Path: "env.token"},
	}, true, nil, func(step Step) (any, error) {
		prompted = step.Path
		return "secret", nil
	})
	if err != nil {
		t.Fatalf("ApplyWithInput() error = %v", err)
	}
	if pending {
		t.Fatal("ApplyWithInput() pending = true, want false")
	}
	if prompted != "env.token" {
		t.Fatalf("prompted path = %q, want env.token", prompted)
	}
	env, ok := doc["env"].(map[string]any)
	if !ok || env["token"] != "secret" {
		t.Fatalf("ApplyWithInput() doc = %+v, want prompted nested value", doc)
	}
}

func TestApplyWithInputPromptsForRequiredInputWhenNotAuto(t *testing.T) {
	t.Parallel()
	doc := map[string]any{}
	pending, err := ApplyWithInput(doc, []Step{
		{Type: "require_input", Path: "env.token"},
	}, false, nil, func(step Step) (any, error) {
		return "secret", nil
	})
	if err != nil {
		t.Fatalf("ApplyWithInput() error = %v", err)
	}
	if pending {
		t.Fatal("ApplyWithInput() pending = true, want false")
	}
	env := doc["env"].(map[string]any)
	if env["token"] != "secret" {
		t.Fatalf("ApplyWithInput() env = %+v, want secret", env)
	}
}

func TestChainRejectsTraversalNameEvenWhenVersionsMatch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, err := Chain(root, "../docs", "2", "2")
	if err == nil || !strings.Contains(err.Error(), "invalid migration name") {
		t.Fatalf("Chain() error = %v, want invalid migration name", err)
	}
}

func TestChainReturnsOrderedMultiHopPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMigration(t, root, "docs", File{From: "1", To: "2"})
	writeMigration(t, root, "docs", File{From: "2", To: "3"})
	chain, err := Chain(root, "docs", "1", "3")
	if err != nil {
		t.Fatalf("Chain() error = %v", err)
	}
	if len(chain) != 2 || chain[0].From != "1" || chain[0].To != "2" || chain[1].From != "2" || chain[1].To != "3" {
		t.Fatalf("Chain() = %+v, want 1->2 then 2->3", chain)
	}
}

func TestChainIgnoresCyclicDeadBranchWhenValidPathExists(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMigration(t, root, "docs", File{From: "1", To: "2"})
	writeMigration(t, root, "docs", File{From: "2", To: "1"})
	writeMigration(t, root, "docs", File{From: "1", To: "3"})
	writeMigration(t, root, "docs", File{From: "3", To: "4"})
	chain, err := Chain(root, "docs", "1", "4")
	if err != nil {
		t.Fatalf("Chain() error = %v", err)
	}
	if len(chain) != 2 || chain[0].To != "3" || chain[1].To != "4" {
		t.Fatalf("Chain() = %+v, want 1->3 then 3->4", chain)
	}
}

func TestChainPrefersDirectEdgeToRequestedVersion(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMigration(t, root, "docs", File{From: "1", To: "2"})
	writeMigration(t, root, "docs", File{From: "1", To: "3"})
	chain, err := Chain(root, "docs", "1", "3")
	if err != nil {
		t.Fatalf("Chain() error = %v", err)
	}
	if len(chain) != 1 || chain[0].From != "1" || chain[0].To != "3" {
		t.Fatalf("Chain() = %+v, want direct 1->3 edge", chain)
	}
}

func TestChainRejectsBrokenPath(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeMigration(t, root, "docs", File{From: "1", To: "2"})
	_, err := Chain(root, "docs", "1", "3")
	if err == nil || !strings.Contains(err.Error(), "broken migration chain") {
		t.Fatalf("Chain() error = %v, want broken chain", err)
	}
}

func TestChainRejectsTraversalName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_, err := Chain(root, "../docs", "1", "2")
	if err == nil || !strings.Contains(err.Error(), "invalid migration name") {
		t.Fatalf("Chain() error = %v, want invalid migration name", err)
	}
}

func TestChainRejectsSymlinkedMigrationFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "migrations", "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(root, "external.json")
	data, err := json.Marshal(File{From: "1", To: "2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(external, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(dir, "1-to-2.json")); err != nil {
		t.Fatal(err)
	}
	_, err = Chain(root, "docs", "1", "2")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Chain() error = %v, want symlink rejection", err)
	}
}

func TestChainRejectsSymlinkedMigrationDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	externalDir := filepath.Join(root, "external")
	if err := os.MkdirAll(externalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeMigration(t, externalDir, ".", File{From: "1", To: "2"})
	migrationsDir := filepath.Join(root, "migrations")
	if err := os.MkdirAll(migrationsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(externalDir, "migrations", "."), filepath.Join(migrationsDir, "docs")); err != nil {
		t.Fatal(err)
	}
	_, err := Chain(root, "docs", "1", "2")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Chain() error = %v, want symlink rejection", err)
	}
}

func TestChainRejectsMalformedMigrationFilename(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "migrations", "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(File{From: "1", To: "2"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "1-2.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Chain(root, "docs", "1", "2")
	if err == nil || !strings.Contains(err.Error(), "filename") {
		t.Fatalf("Chain() error = %v, want filename validation", err)
	}
}

func TestChainRejectsMismatchedFilename(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "migrations", "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(File{From: "1", To: "3"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "1-to-2.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Chain(root, "docs", "1", "3")
	if err == nil || !strings.Contains(err.Error(), "filename") {
		t.Fatalf("Chain() error = %v, want filename mismatch", err)
	}
}

func TestChainRejectsCycles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "migrations", "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"from":"1","to":"1","steps":[]}`
	if err := os.WriteFile(filepath.Join(dir, "1-to-1.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Chain(root, "docs", "1", "2")
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("Chain() error = %v, want cycle", err)
	}
}

func writeMigration(t *testing.T, root, name string, file File) {
	t.Helper()
	dir := filepath.Join(root, "migrations", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, file.From+"-to-"+file.To+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
