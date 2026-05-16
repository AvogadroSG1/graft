package status

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/poconnor/graft/internal/config"
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/model"
)

func TestResolveReportsUninitialized(t *testing.T) {
	t.Parallel()
	got := Resolve(t.TempDir(), config.Config{}, lock.Lock{}, map[string]model.LibraryIndex{})
	if got.State != StateUninitialized {
		t.Fatalf("Resolve() = %+v, want uninitialized", got)
	}
}

func TestResolveReportsInitialized(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := (lock.FileStore{}).Save(root, lock.Lock{}); err != nil {
		t.Fatal(err)
	}
	got := Resolve(root, config.Config{}, lock.Lock{}, map[string]model.LibraryIndex{})
	if got.State != StateInitialized {
		t.Fatalf("Resolve() = %+v, want initialized", got)
	}
}

func TestResolveReportsConfigured(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lk := lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", DefinitionHash: "same"}},
	}
	if err := (lock.FileStore{}).Save(root, lk); err != nil {
		t.Fatal(err)
	}
	got := Resolve(
		root,
		config.Config{Libraries: []config.Library{{Name: "core", URL: "url"}}},
		lk,
		map[string]model.LibraryIndex{"core": {MCPs: []model.IndexEntry{{Name: "docs", SHA256: "same"}}}},
	)
	if got.State != StateConfigured {
		t.Fatalf("Resolve() = %+v, want configured", got)
	}
}

func TestResolveReportsUnknownLibrary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lk := lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core"}},
	}
	if err := (lock.FileStore{}).Save(root, lk); err != nil {
		t.Fatal(err)
	}
	got := Resolve(root, config.Config{}, lk, map[string]model.LibraryIndex{})
	if got.State != StateUnknownLibrary {
		t.Fatalf("Resolve() = %+v, want unknown_library", got)
	}
}

func TestResolveDetectsDrift(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := (lock.FileStore{}).Save(root, lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", DefinitionHash: "old"}},
	}); err != nil {
		t.Fatal(err)
	}
	got := Resolve(
		root,
		config.Config{Libraries: []config.Library{{Name: "core", URL: "url"}}},
		lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}}, MCPs: []lock.InstalledMCP{{Name: "docs", Library: "core", DefinitionHash: "old"}}},
		map[string]model.LibraryIndex{"core": {MCPs: []model.IndexEntry{{Name: "docs", SHA256: "new"}}}},
	)
	if got.State != StateDrifted {
		t.Fatalf("Resolve() = %+v, want drifted", got)
	}
}

func TestResolveReportsPendingInput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lk := lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", PendingInput: true}},
	}
	if err := (lock.FileStore{}).Save(root, lk); err != nil {
		t.Fatal(err)
	}
	got := Resolve(
		root,
		config.Config{Libraries: []config.Library{{Name: "core", URL: "url"}}},
		lk,
		map[string]model.LibraryIndex{},
	)
	if got.State != StatePendingInput {
		t.Fatalf("Resolve() = %+v, want pending_input", got)
	}
	if len(got.Details) != 1 || got.Details[0] != "docs" {
		t.Fatalf("Resolve() details = %+v, want docs", got.Details)
	}
}

func TestResolveReportsExternalClaudeEditAsDrift(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lk := lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", DefinitionHash: "same", Target: "claude"}},
	}
	if err := (lock.FileStore{}).Save(root, lk); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"docs":{"command":"npx"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := Resolve(
		root,
		config.Config{Libraries: []config.Library{{Name: "core", URL: "url"}}},
		lk,
		map[string]model.LibraryIndex{"core": {MCPs: []model.IndexEntry{{Name: "docs", SHA256: "same"}}}},
	)
	if got.State != StateDrifted {
		t.Fatalf("Resolve() = %+v, want drifted", got)
	}
	if len(got.Details) != 1 || got.Details[0] != "external edit: claude/docs" {
		t.Fatalf("Resolve() details = %+v, want external edit detail", got.Details)
	}
}

func TestResolveWithDefinitionsReportsPinMismatch(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lk := lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", DefinitionHash: "same", Target: "claude"}},
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"docs":{"command":"npx","args":["pkg@1.0.0"],"_graft_managed":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (lock.FileStore{}).Save(root, lk); err != nil {
		t.Fatal(err)
	}
	got := ResolveWithDefinitions(
		root,
		config.Config{Libraries: []config.Library{{Name: "core", URL: "url"}}},
		lk,
		map[string]model.LibraryIndex{"core": {MCPs: []model.IndexEntry{{Name: "docs", SHA256: "same"}}}},
		map[string]map[string]model.Definition{"core": {"docs": {Name: "docs", Command: "npx", Args: []string{"pkg@1.0.0"}, Pin: model.Pin{Runtime: "npm", Version: "2.0.0", Hash: "sha512-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="}}}},
	)
	if got.State != StatePinMismatch {
		t.Fatalf("ResolveWithDefinitions() = %+v, want pinmismatch", got)
	}
}

func TestResolveWithDefinitionsAcceptsUVDependencyOptions(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lk := lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", DefinitionHash: "same", Target: "claude"}},
	}
	rendered := `{"mcpServers":{"docs":{"command":"uvx","args":["--with","dep==1.0.0","tool==2.0.0"],"_graft_managed":true}}}`
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(rendered), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (lock.FileStore{}).Save(root, lk); err != nil {
		t.Fatal(err)
	}
	got := ResolveWithDefinitions(
		root,
		config.Config{Libraries: []config.Library{{Name: "core", URL: "url"}}},
		lk,
		map[string]model.LibraryIndex{"core": {MCPs: []model.IndexEntry{{Name: "docs", SHA256: "same"}}}},
		map[string]map[string]model.Definition{"core": {"docs": {Name: "docs", Command: "uvx", Args: []string{"tool==2.0.0"}, Pin: model.Pin{Runtime: "uvx", Version: "2.0.0", Hash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}},
	)
	if got.State != StateConfigured {
		t.Fatalf("ResolveWithDefinitions() = %+v, want configured", got)
	}
}

func TestResolveReportsMalformedClaudeConfigAsDrift(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lk := lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", DefinitionHash: "same", Target: "claude"}},
	}
	if err := (lock.FileStore{}).Save(root, lk); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := Resolve(root, config.Config{Libraries: []config.Library{{Name: "core", URL: "url"}}}, lk, map[string]model.LibraryIndex{"core": {MCPs: []model.IndexEntry{{Name: "docs", SHA256: "same"}}}})
	if got.State != StateDrifted || got.Details[0] != "external edit: claude config is unreadable" {
		t.Fatalf("Resolve() = %+v, want unreadable claude drift", got)
	}
}

func TestResolveReportsDeletedClaudeConfigAsDrift(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lk := lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", DefinitionHash: "same", Target: "claude"}},
	}
	if err := (lock.FileStore{}).Save(root, lk); err != nil {
		t.Fatal(err)
	}
	got := Resolve(root, config.Config{Libraries: []config.Library{{Name: "core", URL: "url"}}}, lk, map[string]model.LibraryIndex{"core": {MCPs: []model.IndexEntry{{Name: "docs", SHA256: "same"}}}})
	if got.State != StateDrifted || !strings.Contains(got.Details[0], "claude config is missing") {
		t.Fatalf("Resolve() = %+v, want missing claude drift", got)
	}
}

func TestResolveReportsDeletedClaudeEntryAsDrift(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lk := lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", DefinitionHash: "same", Target: "claude"}},
	}
	if err := (lock.FileStore{}).Save(root, lk); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"mcpServers":{"other":{"command":"npx","_graft_managed":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got := Resolve(root, config.Config{Libraries: []config.Library{{Name: "core", URL: "url"}}}, lk, map[string]model.LibraryIndex{"core": {MCPs: []model.IndexEntry{{Name: "docs", SHA256: "same"}}}})
	if got.State != StateDrifted || got.Details[0] != "external edit: claude/docs is missing" {
		t.Fatalf("Resolve() = %+v, want missing claude entry drift", got)
	}
}

func TestResolveReportsExternalCodexEditAsDrift(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lk := lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", DefinitionHash: "same", Target: "codex"}},
	}
	if err := (lock.FileStore{}).Save(root, lk); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex", "config.toml"), []byte("[mcp_servers.docs]\ncommand = \"npx\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := Resolve(root, config.Config{Libraries: []config.Library{{Name: "core", URL: "url"}}}, lk, map[string]model.LibraryIndex{"core": {MCPs: []model.IndexEntry{{Name: "docs", SHA256: "same"}}}})
	if got.State != StateDrifted || got.Details[0] != "external edit: codex/docs" {
		t.Fatalf("Resolve() = %+v, want codex external edit", got)
	}
}

func TestResolveReportsManagedClaudeEditNewerThanLockAsDrift(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	lk := lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "url"}},
		MCPs:      []lock.InstalledMCP{{Name: "docs", Library: "core", DefinitionHash: "same", Target: "claude"}},
	}
	if err := (lock.FileStore{}).Save(root, lk); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"docs":{"command":"npx","_graft_managed":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	got := Resolve(root, config.Config{Libraries: []config.Library{{Name: "core", URL: "url"}}}, lk, map[string]model.LibraryIndex{"core": {MCPs: []model.IndexEntry{{Name: "docs", SHA256: "same"}}}})
	if got.State != StateDrifted || got.Details[0] != "external edit: claude/docs" {
		t.Fatalf("Resolve() = %+v, want managed external edit", got)
	}
}
