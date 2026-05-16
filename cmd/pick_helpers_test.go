package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/poconnor/graft/internal/config"
	librarymock "github.com/poconnor/graft/internal/library/mock"
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/model"
	"github.com/poconnor/graft/internal/tui"
	"go.uber.org/mock/gomock"
)

func TestBuildPickListFlattensAllLibraries(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	core := config.Library{Name: "core", CachePath: "/cache/core"}
	team := config.Library{Name: "team", CachePath: "/cache/team"}
	client.EXPECT().Index(core).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1", SHA256: "hash-a"}}}, nil)
	client.EXPECT().Index(team).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "build", Version: "2", SHA256: "hash-b"}}}, nil)

	got, err := buildPickList(config.Config{Libraries: []config.Library{core, team}}, client)

	if err != nil {
		t.Fatalf("buildPickList() error = %v", err)
	}
	if len(got.Items) != 2 || got.Items[0].Library != "core" || got.Items[0].Entry.Name != "docs" || got.Items[1].Library != "team" || got.Items[1].Entry.Name != "build" {
		t.Fatalf("buildPickList() = %+v, want flattened library-qualified entries", got)
	}
}

func TestBuildPickListWrapsIndexErrorWithLibraryName(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	core := config.Library{Name: "core"}
	sentinel := errors.New("network down")
	client.EXPECT().Index(core).Return(model.LibraryIndex{}, sentinel)

	_, err := buildPickList(config.Config{Libraries: []config.Library{core}}, client)

	if err == nil || !strings.Contains(err.Error(), "index library \"core\": network down") {
		t.Fatalf("buildPickList() error = %v, want wrapped library name", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("buildPickList() error = %v, want errors.Is sentinel", err)
	}
}

func TestBuildPickListWarnsForDuplicateNamesFromDifferentLibraries(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	client := librarymock.NewMockClient(ctrl)
	core := config.Library{Name: "core"}
	team := config.Library{Name: "team"}
	client.EXPECT().Index(core).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs"}}}, nil)
	client.EXPECT().Index(team).Return(model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs"}}}, nil)

	got, err := buildPickList(config.Config{Libraries: []config.Library{core, team}}, client)

	if err != nil {
		t.Fatalf("buildPickList() error = %v", err)
	}
	if len(got.Items) != 2 || got.Items[0].Library == got.Items[1].Library || got.Items[0].Entry.Name != got.Items[1].Entry.Name {
		t.Fatalf("buildPickList() = %+v, want both duplicate names with library provenance", got)
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "duplicate MCP name \"docs\" in libraries core and team") {
		t.Fatalf("Warnings = %+v, want duplicate-name warning", got.Warnings)
	}
}

func TestApplyPickResultWritesSelectedMCPsAndEmptySlice(t *testing.T) {
	t.Parallel()
	lk := lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: "https://example.com/core.git"}}, MCPs: []lock.InstalledMCP{{Name: "old"}}}
	results := []tui.PickItem{{
		Library: "core",
		Entry:   model.IndexEntry{Name: "docs", Version: "1.2.3", SHA256: "hash-a"},
	}}

	got, err := applyPickResult(lk, results, "both")

	if err != nil {
		t.Fatalf("applyPickResult() error = %v", err)
	}
	if len(got.MCPs) != 1 {
		t.Fatalf("MCPs length = %d, want 1", len(got.MCPs))
	}
	mcp := got.MCPs[0]
	if mcp.Name != "docs" || mcp.Library != "core" || mcp.Version != "1.2.3" || mcp.DefinitionHash != "hash-a" || mcp.Target != "both" {
		t.Fatalf("MCP = %+v, want copied selected entry", mcp)
	}

	got, err = applyPickResult(lk, nil, "claude")
	if err != nil {
		t.Fatalf("applyPickResult(nil) error = %v", err)
	}
	if got.MCPs == nil || len(got.MCPs) != 0 {
		t.Fatalf("MCPs = %#v, want non-nil empty slice", got.MCPs)
	}
}

func TestApplyPickResultErrorsForUnknownLibrary(t *testing.T) {
	t.Parallel()
	lk := lock.Lock{Libraries: []lock.LibraryRef{{Name: "core"}}}
	results := []tui.PickItem{{Library: "missing", Entry: model.IndexEntry{Name: "docs"}}}

	_, err := applyPickResult(lk, results, "both")

	if err == nil || !strings.Contains(err.Error(), "library \"missing\" is not in graft.lock") {
		t.Fatalf("applyPickResult() error = %v, want unknown library error", err)
	}
}

func TestApplyPickResultRejectsDuplicateRenderedNames(t *testing.T) {
	t.Parallel()
	lk := lock.Lock{Libraries: []lock.LibraryRef{{Name: "core"}, {Name: "team"}}}
	results := []tui.PickItem{
		{Library: "core", Entry: model.IndexEntry{Name: "docs"}},
		{Library: "team", Entry: model.IndexEntry{Name: "docs"}},
	}

	_, err := applyPickResult(lk, results, "both")

	if err == nil || !strings.Contains(err.Error(), "duplicate MCP name \"docs\"") {
		t.Fatalf("applyPickResult() error = %v, want duplicate name error", err)
	}
}
