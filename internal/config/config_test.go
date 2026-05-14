package config

import (
	"path/filepath"
	"testing"
)

func TestFileStore_SaveLoadRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.json")
	store := FileStore{}
	cfg := Config{Libraries: []Library{{Name: "core", URL: "https://example.com/core.git", CachePath: "/tmp/core", Default: true}}}
	if err := store.Save(path, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := store.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(got.Libraries) != 1 || got.Libraries[0].Name != "core" {
		t.Fatalf("Load() = %+v, want core library", got)
	}
}

func TestConfig_WithLibrarySetsFirstAsDefault(t *testing.T) {
	t.Parallel()
	got, err := (Config{}).WithLibrary(Library{Name: "core", URL: "url", CachePath: "/tmp/core"})
	if err != nil {
		t.Fatalf("WithLibrary() error = %v", err)
	}
	if len(got.Libraries) != 1 || !got.Libraries[0].Default {
		t.Fatalf("WithLibrary() = %+v, want first library default", got)
	}
}

func TestDefaultPathUsesXDGConfigHome(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath() error = %v", err)
	}
	want := filepath.Join(dir, "graft", "config.json")
	if got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}
