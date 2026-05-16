package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/poconnor/graft/internal/config"
	librarymock "github.com/poconnor/graft/internal/library/mock"
	"github.com/poconnor/graft/internal/model"
	"go.uber.org/mock/gomock"
)

func TestLibraryAddRegistersClonesAndSetsFirstAsDefault(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Add(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, lib config.Library) error {
		if lib.Name != "core" || lib.URL != "https://example.com/core.git" || lib.CachePath == "" || !lib.Default {
			t.Fatalf("Add lib = %+v, want populated default core library", lib)
		}
		return nil
	})
	command := newLibraryCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath}, client)
	command.SetArgs([]string{"add", "core", "https://example.com/core.git"})
	var out bytes.Buffer
	command.SetOut(&out)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got, err := (config.FileStore{}).Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	lib, ok := got.Library("core")
	if !ok || !lib.Default || lib.CachePath == "" || lib.URL != "https://example.com/core.git" {
		t.Fatalf("config library = %+v, %v; want registered default core", lib, ok)
	}
	if !strings.Contains(out.String(), "registered core") {
		t.Fatalf("stdout = %q, want registration message", out.String())
	}
}

func TestLibraryListIncludesLastPulledAndDefaultMarker(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	pulledAt := "2026-05-15T13:14:15Z"
	if err := (config.FileStore{}).Save(cfgPath, config.Config{Libraries: []config.Library{{
		Name:         "core",
		URL:          "https://example.com/core.git",
		CachePath:    "/tmp/core",
		LastPulledAt: pulledAt,
		Default:      true,
	}}}); err != nil {
		t.Fatal(err)
	}
	command := newLibraryCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath}, librarymock.NewMockClient(gomock.NewController(t)))
	command.SetArgs([]string{"list"})
	var out bytes.Buffer
	command.SetOut(&out)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{"core", "https://example.com/core.git", "/tmp/core", pulledAt, "default"} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	}
}

func TestLibraryPullNamedUpdatesLastPulledTimestamp(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	core := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: "/tmp/core", Default: true}
	other := config.Library{Name: "other", URL: "https://example.com/other.git", CachePath: "/tmp/other"}
	if err := (config.FileStore{}).Save(cfgPath, config.Config{Libraries: []config.Library{core, other}}); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Pull(gomock.Any(), core).Return("abc123", nil)
	command := newLibraryCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath}, client)
	command.SetArgs([]string{"pull", "core"})
	var out bytes.Buffer
	command.SetOut(&out)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got, err := (config.FileStore{}).Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	lib, _ := got.Library("core")
	if lib.LastPulledAt == "" {
		t.Fatalf("core LastPulledAt empty after pull")
	}
	if _, err := time.Parse(time.RFC3339, lib.LastPulledAt); err != nil {
		t.Fatalf("core LastPulledAt = %q, want RFC3339: %v", lib.LastPulledAt, err)
	}
	otherLib, _ := got.Library("other")
	if otherLib.LastPulledAt != "" {
		t.Fatalf("other LastPulledAt = %q, want unchanged empty timestamp", otherLib.LastPulledAt)
	}
	if !strings.Contains(out.String(), "core\tabc123") {
		t.Fatalf("stdout = %q, want pulled SHA", out.String())
	}
}

func TestLibraryShowListFiltersPlainAndJSONOutput(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: "/tmp/core", Default: true}
	if err := (config.FileStore{}).Save(cfgPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		t.Fatal(err)
	}
	index := model.LibraryIndex{Name: "core", MCPs: []model.IndexEntry{
		{Name: "docs", Version: "1.0.0", Tags: []string{"docs", "productivity"}, Description: "Docs helper"},
		{Name: "db", Version: "2.0.0", Tags: []string{"database"}, Description: "DB helper"},
	}}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(lib).Return(index, nil).Times(2)
	command := newLibraryCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath}, client)
	command.SetArgs([]string{"show", "--tag", "docs"})
	var plain bytes.Buffer
	command.SetOut(&plain)
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() plain error = %v", err)
	}
	if got := plain.String(); !strings.Contains(got, "docs\t1.0.0\tdocs,productivity\tDocs helper") || strings.Contains(got, "db") {
		t.Fatalf("plain stdout = %q, want only docs row", got)
	}

	command = newLibraryCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath}, client)
	command.SetArgs([]string{"show", "--tag", "docs", "--json"})
	var jsonOut bytes.Buffer
	command.SetOut(&jsonOut)
	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() JSON error = %v", err)
	}
	var decoded model.LibraryIndex
	if err := json.Unmarshal(jsonOut.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout = %q, want JSON: %v", jsonOut.String(), err)
	}
	if len(decoded.MCPs) != 1 || decoded.MCPs[0].Name != "docs" {
		t.Fatalf("JSON index = %+v, want filtered docs entry", decoded)
	}
}

func TestLibraryShowDetailRendersDefinitionSchema(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: "/tmp/core", Default: true}
	if err := (config.FileStore{}).Save(cfgPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		t.Fatal(err)
	}
	def := model.Definition{
		Name:        "docs",
		Version:     "1.0.0",
		Description: "Docs helper",
		Tags:        []string{"docs"},
		Command:     "npx",
		Adapters: map[string]model.AdapterConfig{
			"codex": {Command: "uvx", Args: []string{"docs-mcp"}},
		},
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Definition(lib, "docs").Return(def, "hash-docs", nil)
	command := newLibraryCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath}, client)
	command.SetArgs([]string{"show", "core", "docs"})
	var out bytes.Buffer
	command.SetOut(&out)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got := out.String()
	for _, want := range []string{`"name": "docs"`, `"adapters":`, `"codex":`, `"command": "uvx"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("stdout = %q, want %q", got, want)
		}
	}
}

func TestLibraryAddRejectsDuplicateWithoutMutatingConfig(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	original := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: "/tmp/core", Default: true}
	if err := (config.FileStore{}).Save(cfgPath, config.Config{Libraries: []config.Library{original}}); err != nil {
		t.Fatal(err)
	}
	command := newLibraryCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath}, librarymock.NewMockClient(gomock.NewController(t)))
	command.SetArgs([]string{"add", "core", "https://example.com/other.git"})

	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("Execute() error = %v, want duplicate refusal", err)
	}
	got, err := (config.FileStore{}).Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	lib, _ := got.Library("core")
	if lib.URL != original.URL || lib.CachePath != original.CachePath || !lib.Default {
		t.Fatalf("config library = %+v, want unchanged %+v", lib, original)
	}
}

func TestLibraryAddRejectsUnsafeNamesAndURLsBeforeClone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		url  string
	}{
		{name: "../core", url: "https://example.com/core.git"},
		{name: "core", url: "file:///tmp/core.git"},
		{name: "core", url: "/tmp/core.git"},
		{name: "core", url: "git@example.com:org/core.git"},
		{name: "core", url: "https://token@example.com/core.git"},
		{name: "core", url: "ftp://example.com/core.git"},
	}
	for _, tt := range tests {
		t.Run(tt.name+"/"+tt.url, func(t *testing.T) {
			t.Parallel()
			cfgPath := filepath.Join(t.TempDir(), "config.json")
			command := newLibraryCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath}, librarymock.NewMockClient(gomock.NewController(t)))
			command.SetArgs([]string{"add", tt.name, tt.url})

			err := command.Execute()
			if err == nil {
				t.Fatalf("Execute() error = nil, want validation error")
			}
			got, loadErr := (config.FileStore{}).Load(cfgPath)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if len(got.Libraries) != 0 {
				t.Fatalf("config = %+v, want no persisted libraries", got)
			}
		})
	}
}

func TestLibraryAddFailureLeavesConfigUntouched(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Add(gomock.Any(), gomock.Any()).Return(errors.New("clone failed"))
	command := newLibraryCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath}, client)
	command.SetArgs([]string{"add", "core", "https://example.com/core.git"})

	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "clone failed") {
		t.Fatalf("Execute() error = %v, want clone failure", err)
	}
	got, loadErr := (config.FileStore{}).Load(cfgPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(got.Libraries) != 0 {
		t.Fatalf("config = %+v, want no persisted libraries", got)
	}
}

func TestLibraryPullAllUpdatesAllLibraries(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	core := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: "/tmp/core", Default: true}
	other := config.Library{Name: "other", URL: "https://example.com/other.git", CachePath: "/tmp/other"}
	if err := (config.FileStore{}).Save(cfgPath, config.Config{Libraries: []config.Library{core, other}}); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	gomock.InOrder(
		client.EXPECT().Pull(gomock.Any(), core).Return("sha-core", nil),
		client.EXPECT().Pull(gomock.Any(), other).Return("sha-other", nil),
	)
	command := newLibraryCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath}, client)
	command.SetArgs([]string{"pull"})
	var out bytes.Buffer
	command.SetOut(&out)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	got, err := (config.FileStore{}).Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"core", "other"} {
		lib, _ := got.Library(name)
		if lib.LastPulledAt == "" {
			t.Fatalf("%s LastPulledAt empty after pull-all", name)
		}
	}
	for _, want := range []string{"core\tsha-core", "other\tsha-other"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("stdout = %q, want %q", out.String(), want)
		}
	}
}

func TestLibraryPullAllPersistsEarlierSuccessWhenLaterPullFails(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	core := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: "/tmp/core", Default: true}
	other := config.Library{Name: "other", URL: "https://example.com/other.git", CachePath: "/tmp/other"}
	if err := (config.FileStore{}).Save(cfgPath, config.Config{Libraries: []config.Library{core, other}}); err != nil {
		t.Fatal(err)
	}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	gomock.InOrder(
		client.EXPECT().Pull(gomock.Any(), core).Return("sha-core", nil),
		client.EXPECT().Pull(gomock.Any(), other).Return("", errors.New("pull failed")),
	)
	command := newLibraryCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath}, client)
	command.SetArgs([]string{"pull"})
	var out bytes.Buffer
	command.SetOut(&out)

	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "pull failed") {
		t.Fatalf("Execute() error = %v, want pull failure", err)
	}
	got, loadErr := (config.FileStore{}).Load(cfgPath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	coreAfter, _ := got.Library("core")
	if coreAfter.LastPulledAt == "" {
		t.Fatalf("core LastPulledAt empty after earlier successful pull")
	}
	otherAfter, _ := got.Library("other")
	if otherAfter.LastPulledAt != "" {
		t.Fatalf("other LastPulledAt = %q, want unchanged empty timestamp", otherAfter.LastPulledAt)
	}
	if !strings.Contains(out.String(), "core\tsha-core") {
		t.Fatalf("stdout = %q, want persisted successful pull output", out.String())
	}
}

func TestLibraryShowNamedListUsesRequestedLibrary(t *testing.T) {
	t.Parallel()
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	core := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: "/tmp/core", Default: true}
	other := config.Library{Name: "other", URL: "https://example.com/other.git", CachePath: "/tmp/other"}
	if err := (config.FileStore{}).Save(cfgPath, config.Config{Libraries: []config.Library{core, other}}); err != nil {
		t.Fatal(err)
	}
	index := model.LibraryIndex{Name: "other", MCPs: []model.IndexEntry{{Name: "db", Version: "2.0.0", Tags: []string{"database"}, Description: "DB helper"}}}
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	client.EXPECT().Index(other).Return(index, nil)
	command := newLibraryCommandWithDeps(context.Background(), &appOptions{configPath: cfgPath}, client)
	command.SetArgs([]string{"show", "other", "--tag", "database"})
	var out bytes.Buffer
	command.SetOut(&out)

	if err := command.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got := out.String(); !strings.Contains(got, "db\t2.0.0\tdatabase\tDB helper") {
		t.Fatalf("stdout = %q, want other library row", got)
	}
}
