package status

import (
	"testing"

	"github.com/poconnor/graft/internal/config"
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/model"
)

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
