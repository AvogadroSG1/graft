package sync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/poconnor/graft/internal/config"
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/model"
	"github.com/poconnor/graft/internal/render"
)

func TestApplyMarksPendingInputWhenMigrationRequiresInput(t *testing.T) {
	t.Parallel()
	lib := testLibrary(t)
	writeMigration(t, lib.CachePath, "docs", map[string]any{
		"from": "1",
		"to":   "2",
		"steps": []map[string]any{
			{"type": "require_input", "path": "env.token"},
		},
	})
	adapter := &recordingAdapter{}
	result := Apply(
		lock.Lock{MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "1", DefinitionHash: "old", Target: "claude"}}},
		config.Config{Libraries: []config.Library{lib}},
		fakeClient{def: model.Definition{Name: "docs", Version: "2", Command: "npx"}, hash: "new"},
		[]render.AdapterByName{{Name: "claude", Adapter: adapter}},
	)
	if len(result.Skipped) != 1 || result.Skipped[0] != "docs" {
		t.Fatalf("Apply() skipped = %+v, want docs", result.Skipped)
	}
	if adapter.rendered {
		t.Fatal("Apply() rendered MCP with unresolved input")
	}
	if len(result.Lock.MCPs) != 1 || !result.Lock.MCPs[0].PendingInput {
		t.Fatalf("Apply() lock = %+v, want pending input", result.Lock)
	}
}

func TestApplyRunsAutomaticMigrationStepsBeforeRender(t *testing.T) {
	t.Parallel()
	lib := testLibrary(t)
	writeMigration(t, lib.CachePath, "docs", map[string]any{
		"from": "1",
		"to":   "2",
		"steps": []map[string]any{
			{"type": "set_default", "path": "url", "value": "https://example.com/mcp"},
		},
	})
	adapter := &recordingAdapter{}
	result := Apply(
		lock.Lock{MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "1", DefinitionHash: "old", Target: "claude", PendingInput: true}}},
		config.Config{Libraries: []config.Library{lib}},
		fakeClient{def: model.Definition{Name: "docs", Version: "2", Type: "http"}, hash: "new"},
		[]render.AdapterByName{{Name: "claude", Adapter: adapter}},
	)
	if len(result.Succeeded) != 1 || result.Succeeded[0] != "docs" {
		t.Fatalf("Apply() succeeded = %+v, want docs", result.Succeeded)
	}
	if !adapter.rendered || adapter.def.URL != "https://example.com/mcp" {
		t.Fatalf("rendered definition = %+v, want migrated URL", adapter.def)
	}
	if got := result.Lock.MCPs[0]; got.Version != "2" || got.DefinitionHash != "new" || got.PendingInput {
		t.Fatalf("Apply() lock MCP = %+v, want updated non-pending lock", got)
	}
}

func TestApplyRendersBothTargets(t *testing.T) {
	t.Parallel()
	lib := testLibrary(t)
	claude := &recordingAdapter{}
	codex := &recordingAdapter{}
	result := Apply(
		lock.Lock{MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "2", DefinitionHash: "old", Target: "both"}}},
		config.Config{Libraries: []config.Library{lib}},
		fakeClient{def: model.Definition{Name: "docs", Version: "2", Command: "npx"}, hash: "new"},
		[]render.AdapterByName{{Name: "claude", Adapter: claude}, {Name: "codex", Adapter: codex}},
	)
	if len(result.Succeeded) != 1 || result.Succeeded[0] != "docs" {
		t.Fatalf("Apply() succeeded = %+v, want docs", result.Succeeded)
	}
	if !claude.rendered || !codex.rendered {
		t.Fatalf("rendered targets: claude=%v codex=%v, want both", claude.rendered, codex.rendered)
	}
}

func TestApplyRollsBackBothTargetsWhenLaterRenderFails(t *testing.T) {
	t.Parallel()
	lib := testLibrary(t)
	claude := &recordingAdapter{rendered: true, def: model.Definition{Name: "previous"}}
	codex := &recordingAdapter{err: errRenderFailed}
	result := Apply(
		lock.Lock{MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "2", DefinitionHash: "old", Target: "both"}}},
		config.Config{Libraries: []config.Library{lib}},
		fakeClient{def: model.Definition{Name: "docs", Version: "2", Command: "npx"}, hash: "new"},
		[]render.AdapterByName{{Name: "claude", Adapter: claude}, {Name: "codex", Adapter: codex}},
	)
	if len(result.Failed) != 1 || result.Failed[0] != "docs" {
		t.Fatalf("Apply() failed = %+v, want docs", result.Failed)
	}
	if claude.def.Name != "previous" {
		t.Fatalf("claude definition = %+v, want rollback to previous", claude.def)
	}
	if result.Lock.MCPs[0].DefinitionHash != "old" {
		t.Fatalf("lock hash = %q, want stale hash after failed render", result.Lock.MCPs[0].DefinitionHash)
	}
}

func TestApplyReportsRollbackFailure(t *testing.T) {
	t.Parallel()
	lib := testLibrary(t)
	claude := &recordingAdapter{restoreErr: errRestoreFailed}
	codex := &recordingAdapter{err: errRenderFailed}
	result := Apply(
		lock.Lock{MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "2", DefinitionHash: "old", Target: "both"}}},
		config.Config{Libraries: []config.Library{lib}},
		fakeClient{def: model.Definition{Name: "docs", Version: "2", Command: "npx"}, hash: "new"},
		[]render.AdapterByName{{Name: "claude", Adapter: claude}, {Name: "codex", Adapter: codex}},
	)
	if len(result.Failed) != 1 || result.Failed[0] != "docs" {
		t.Fatalf("Apply() failed = %+v, want docs", result.Failed)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "rollback: restore failed") {
		t.Fatalf("Apply() errors = %+v, want rollback failure detail", result.Errors)
	}
}

type fakeClient struct {
	def  model.Definition
	hash string
}

func (c fakeClient) Add(context.Context, config.Library) error {
	return nil
}

func (c fakeClient) Pull(context.Context, config.Library) (string, error) {
	return "", nil
}

func (c fakeClient) Index(config.Library) (model.LibraryIndex, error) {
	return model.LibraryIndex{}, nil
}

func (c fakeClient) Definition(config.Library, string) (model.Definition, string, error) {
	return c.def, c.hash, nil
}

func (c fakeClient) Reindex(config.Library) (model.LibraryIndex, error) {
	return model.LibraryIndex{}, nil
}

type recordingAdapter struct {
	rendered   bool
	def        model.Definition
	err        error
	restoreErr error
}

func (a *recordingAdapter) Render(def model.Definition) error {
	if a.err != nil {
		return a.err
	}
	a.rendered = true
	a.def = def
	return nil
}

func (a *recordingAdapter) Snapshot() (any, error) {
	return a.def, nil
}

func (a *recordingAdapter) Restore(snapshot any) error {
	if a.restoreErr != nil {
		return a.restoreErr
	}
	a.def = snapshot.(model.Definition)
	return nil
}

func (a *recordingAdapter) Remove(string) error {
	return nil
}

func (a *recordingAdapter) TargetFile() string {
	return ""
}

var errRenderFailed = errors.New("render failed")
var errRestoreFailed = errors.New("restore failed")

func testLibrary(t *testing.T) config.Library {
	t.Helper()
	return config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: t.TempDir()}
}

func writeMigration(t *testing.T, root, name string, body map[string]any) {
	t.Helper()
	dir := filepath.Join(root, "migrations", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, body["from"].(string)+"-to-"+body["to"].(string)+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
