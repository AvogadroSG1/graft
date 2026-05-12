package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyHandlesAutoPendingInput(t *testing.T) {
	t.Parallel()
	doc := map[string]any{"old": "value"}
	pending, err := Apply(doc, []Step{
		{Type: "rename", From: "old", To: "new"},
		{Type: "set_default", Path: "runtime", Value: "node"},
		{Type: "require_input", Path: "token"},
	}, true, map[string]string{})
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !pending {
		t.Fatal("Apply() pending = false, want true")
	}
	if doc["new"] != "value" || doc["runtime"] != "node" {
		t.Fatalf("Apply() doc = %+v, want migrated values", doc)
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
