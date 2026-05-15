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

func TestApplyFailsDefinitionErrorsInsteadOfSkipping(t *testing.T) {
	t.Parallel()
	lib := testLibrary(t)
	result := Apply(
		lock.Lock{MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "1", DefinitionHash: "old", Target: "claude"}}},
		config.Config{Libraries: []config.Library{lib}},
		fakeClient{err: errDefinitionFailed},
		[]render.AdapterByName{{Name: "claude", Adapter: &recordingAdapter{}}},
	)
	if len(result.Failed) != 1 || result.Failed[0] != "docs" {
		t.Fatalf("Apply() failed = %+v, want docs", result.Failed)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "definition") {
		t.Fatalf("Apply() errors = %+v, want definition failure detail", result.Errors)
	}
}

func TestApplySkipsCurrentAndOnlyProcessesNamedDrift(t *testing.T) {
	t.Parallel()
	lib := testLibrary(t)
	adapter := &recordingAdapter{}
	client := fakeClient{defs: map[string]definitionResult{
		"docs":  {def: model.Definition{Name: "docs", Version: "2", Command: "npx"}, hash: "new-docs"},
		"build": {def: model.Definition{Name: "build", Version: "1", Command: "npx"}, hash: "same-build"},
	}}
	result := ApplyWithOptions(
		lock.Lock{MCPs: []lock.InstalledMCP{
			{Name: "docs", Library: "core", Version: "2", DefinitionHash: "old-docs", Target: "claude"},
			{Name: "build", Library: "core", Version: "1", DefinitionHash: "same-build", Target: "claude"},
		}},
		config.Config{Libraries: []config.Library{lib}},
		client,
		[]render.AdapterByName{{Name: "claude", Adapter: adapter}},
		Options{Names: []string{"docs"}},
	)
	if len(result.Succeeded) != 1 || result.Succeeded[0] != "docs" {
		t.Fatalf("Apply() succeeded = %+v, want docs", result.Succeeded)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "build" {
		t.Fatalf("Apply() skipped = %+v, want build skipped by filter/current", result.Skipped)
	}
	if !adapter.rendered || adapter.def.Name != "docs" {
		t.Fatalf("rendered definition = %+v, want docs only", adapter.def)
	}
}

func TestApplySkipsAlreadyCurrentMCP(t *testing.T) {
	t.Parallel()
	lib := testLibrary(t)
	adapter := &recordingAdapter{}
	result := Apply(
		lock.Lock{MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "1", DefinitionHash: "same", Target: "claude"}}},
		config.Config{Libraries: []config.Library{lib}},
		fakeClient{def: model.Definition{Name: "docs", Version: "1", Command: "npx"}, hash: "same"},
		[]render.AdapterByName{{Name: "claude", Adapter: adapter}},
	)
	if adapter.rendered {
		t.Fatal("Apply() rendered already-current MCP")
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "docs" {
		t.Fatalf("Apply() skipped = %+v, want docs", result.Skipped)
	}
}

func TestApplyReportsSucceededFailedAndSkippedTogether(t *testing.T) {
	t.Parallel()
	lib := testLibrary(t)
	adapter := &recordingAdapter{}
	client := fakeClient{defs: map[string]definitionResult{
		"docs":  {def: model.Definition{Name: "docs", Version: "2", Command: "npx"}, hash: "new-docs"},
		"bad":   {err: errDefinitionFailed},
		"build": {def: model.Definition{Name: "build", Version: "1", Command: "npx"}, hash: "same-build"},
	}}
	result := Apply(
		lock.Lock{MCPs: []lock.InstalledMCP{
			{Name: "docs", Library: "core", Version: "2", DefinitionHash: "old-docs", Target: "claude"},
			{Name: "bad", Library: "core", Version: "1", DefinitionHash: "old-bad", Target: "claude"},
			{Name: "build", Library: "core", Version: "1", DefinitionHash: "same-build", Target: "claude"},
		}},
		config.Config{Libraries: []config.Library{lib}},
		client,
		[]render.AdapterByName{{Name: "claude", Adapter: adapter}},
	)
	if len(result.Succeeded) != 1 || result.Succeeded[0] != "docs" {
		t.Fatalf("succeeded = %+v, want docs", result.Succeeded)
	}
	if len(result.Failed) != 1 || result.Failed[0] != "bad" {
		t.Fatalf("failed = %+v, want bad", result.Failed)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "build" {
		t.Fatalf("skipped = %+v, want build", result.Skipped)
	}
}

func TestApplyWarnsAndSkipsAuthSensitiveDefinition(t *testing.T) {
	t.Parallel()
	lib := testLibrary(t)
	adapter := &recordingAdapter{}
	result := Apply(
		lock.Lock{MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "2", DefinitionHash: "old", Target: "claude"}}},
		config.Config{Libraries: []config.Library{lib}},
		fakeClient{def: model.Definition{Name: "docs", Version: "2", Command: "npx", Env: map[string]string{"API_KEY": "${API_KEY}"}, Headers: map[string]string{"Authorization": "${AUTH}"}}, hash: "new"},
		[]render.AdapterByName{{Name: "claude", Adapter: adapter}},
	)
	if adapter.rendered {
		t.Fatal("Apply() rendered auth-sensitive definition")
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "docs" {
		t.Fatalf("Apply() skipped = %+v, want docs", result.Skipped)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "auth warning") {
		t.Fatalf("Apply() warnings = %+v, want auth warning", result.Warnings)
	}
}

func TestApplyWarnsAndSkipsAdapterOverrideAuthDefinition(t *testing.T) {
	t.Parallel()
	lib := testLibrary(t)
	adapter := &recordingAdapter{}
	result := Apply(
		lock.Lock{MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "2", DefinitionHash: "old", Target: "claude"}}},
		config.Config{Libraries: []config.Library{lib}},
		fakeClient{def: model.Definition{
			Name:     "docs",
			Version:  "2",
			Command:  "npx",
			Adapters: map[string]model.AdapterConfig{"claude": {Headers: map[string]string{"Authorization": "${AUTH}"}}},
		}, hash: "new"},
		[]render.AdapterByName{{Name: "claude", Adapter: adapter}},
	)
	if adapter.rendered {
		t.Fatal("Apply() rendered adapter auth-sensitive definition")
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "docs" {
		t.Fatalf("Apply() skipped = %+v, want docs", result.Skipped)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "claude") {
		t.Fatalf("Apply() warnings = %+v, want claude auth warning", result.Warnings)
	}
}

func TestApplyWarnsWhenMigrationIntroducesCredentials(t *testing.T) {
	t.Parallel()
	lib := testLibrary(t)
	writeMigration(t, lib.CachePath, "docs", map[string]any{
		"from": "1",
		"to":   "2",
		"steps": []map[string]any{
			{"type": "set_default", "path": "env.API_KEY", "value": "${API_KEY}"},
		},
	})
	adapter := &recordingAdapter{}
	result := Apply(
		lock.Lock{MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "1", DefinitionHash: "old", Target: "claude"}}},
		config.Config{Libraries: []config.Library{lib}},
		fakeClient{def: model.Definition{Name: "docs", Version: "2", Command: "npx"}, hash: "new"},
		[]render.AdapterByName{{Name: "claude", Adapter: adapter}},
	)
	if adapter.rendered {
		t.Fatal("Apply() rendered definition after migration introduced credentials")
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "docs" {
		t.Fatalf("Apply() skipped = %+v, want docs", result.Skipped)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], "auth warning") {
		t.Fatalf("Apply() warnings = %+v, want migrated auth warning", result.Warnings)
	}
}

func TestApplyFailsMixedUnknownTargetWithoutRendering(t *testing.T) {
	t.Parallel()
	lib := testLibrary(t)
	adapter := &recordingAdapter{}
	result := Apply(
		lock.Lock{MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", Version: "2", DefinitionHash: "old", Target: "claude,bad"}}},
		config.Config{Libraries: []config.Library{lib}},
		fakeClient{def: model.Definition{Name: "docs", Version: "2", Command: "npx"}, hash: "new"},
		[]render.AdapterByName{{Name: "claude", Adapter: adapter}},
	)
	if adapter.rendered {
		t.Fatal("Apply() rendered a partial unknown target")
	}
	if len(result.Failed) != 1 || result.Failed[0] != "docs" {
		t.Fatalf("Apply() failed = %+v, want docs", result.Failed)
	}
	if len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "bad") {
		t.Fatalf("Apply() errors = %+v, want bad target detail", result.Errors)
	}
}

func TestAuthWarningCoversCredentialPatternsAndAdapterOverrides(t *testing.T) {
	t.Parallel()
	tests := []model.Definition{
		{Name: "token", Command: "npx", Env: map[string]string{"ACCESS_TOKEN": "${ACCESS_TOKEN}"}},
		{Name: "secret", Command: "npx", Env: map[string]string{"CLIENT_SECRET": "${CLIENT_SECRET}"}},
		{Name: "password", Command: "npx", Env: map[string]string{"PASSWORD": "${PASSWORD}"}},
		{Name: "credential", Command: "npx", Env: map[string]string{"GOOGLE_CREDENTIAL": "${GOOGLE_CREDENTIAL}"}},
		{Name: "bearer-key", Command: "npx", Env: map[string]string{"bearer_token_env_var": "TOKEN"}},
		{Name: "bearer-value", Command: "npx", Env: map[string]string{"AUTH": "Bearer ${TOKEN}"}},
		{Name: "adapter-header", Command: "npx", Adapters: map[string]model.AdapterConfig{"claude": {Headers: map[string]string{"Authorization": "${AUTH}"}}}},
		{Name: "adapter-env", Command: "npx", Adapters: map[string]model.AdapterConfig{"codex": {Env: map[string]string{"API_KEY": "${API_KEY}"}}}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.Name, func(t *testing.T) {
			t.Parallel()
			if got := AuthWarningForTarget(tt, "both"); got == "" {
				t.Fatalf("AuthWarningForTarget(%s) = empty, want warning", tt.Name)
			}
		})
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
	err  error
	defs map[string]definitionResult
}

type definitionResult struct {
	def  model.Definition
	hash string
	err  error
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

func (c fakeClient) Definition(_ config.Library, name string) (model.Definition, string, error) {
	if c.defs != nil {
		result := c.defs[name]
		return result.def, result.hash, result.err
	}
	if c.err != nil {
		return model.Definition{}, "", c.err
	}
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
var errDefinitionFailed = errors.New("definition failed")

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
