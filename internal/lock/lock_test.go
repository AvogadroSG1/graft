package lock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileStore_SaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store := FileStore{}
	want := Lock{
		Libraries: []LibraryRef{{Name: "core", URL: "https://example.com/core.git"}},
		MCPs:      []InstalledMCP{{Name: "docs", Library: "core", DefinitionHash: HashBytes([]byte("docs"))}},
	}
	if err := store.Save(root, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := store.Load(root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got.MCPs) != 1 || got.MCPs[0].Name != "docs" {
		t.Fatalf("Load() = %+v, want docs MCP", got)
	}
	if got.Libraries[0].Commit != "" {
		t.Fatalf("Load() commit = %q, want empty", got.Libraries[0].Commit)
	}
	if _, err := os.Stat(filepath.Join(root, Filename)); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
}

func TestHashBytes(t *testing.T) {
	t.Parallel()
	got := HashBytes([]byte("abc"))
	if got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("HashBytes() = %s", got)
	}
}

func TestHashFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "data")
	if err := os.WriteFile(path, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := HashFile(path)
	if err != nil {
		t.Fatalf("HashFile() error = %v", err)
	}
	if got != HashBytes([]byte("abc")) {
		t.Fatalf("HashFile() = %s", got)
	}
}
