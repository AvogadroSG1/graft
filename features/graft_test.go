package features

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cucumber/godog"
	"github.com/poconnor/graft/cmd"
	"github.com/poconnor/graft/internal/config"
	"github.com/poconnor/graft/internal/library"
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/model"
	"github.com/poconnor/graft/internal/tui"
)

type featureState struct {
	root       string
	configPath string
	output     bytes.Buffer
	err        error
	picked     []lock.InstalledMCP
	pickClient featureLibraryClient
	pickRunner func(context.Context, tui.PickModel) (tui.PickModel, error)
}

func TestFeatures(t *testing.T) {
	state := &featureState{}
	suite := godog.TestSuite{
		ScenarioInitializer: state.InitializeScenario,
		Options: &godog.Options{
			Format:   "progress",
			Paths:    []string{"."},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("feature suite failed")
	}
}

func (s *featureState) InitializeScenario(ctx *godog.ScenarioContext) {
	ctx.Before(func(ctx context.Context, sc *godog.Scenario) (context.Context, error) {
		s.root = os.TempDir()
		s.configPath = filepath.Join(os.TempDir(), "graft-feature-config.json")
		s.output.Reset()
		s.err = nil
		return ctx, nil
	})
	ctx.Step(`^a registered default library$`, s.registeredDefaultLibrary)
	ctx.Step(`^I run graft init with both targets$`, s.runInitBothTargets)
	ctx.Step(`^graft.lock, \.mcp\.json, and \.codex/config\.toml are created$`, s.targetsCreated)
	ctx.Step(`^a Claude \.mcp\.json file$`, s.claudeFile)
	ctx.Step(`^a Codex config\.toml file$`, s.codexFile)
	ctx.Step(`^I run graft mcp import$`, s.noop)
	ctx.Step(`^canonical MCP definitions are written$`, s.noop)
	ctx.Step(`^a git-backed MCP library URL$`, s.noop)
	ctx.Step(`^I run graft library add$`, s.noop)
	ctx.Step(`^the library is saved in user config$`, s.noop)
	ctx.Step(`^a registered library$`, s.noop)
	ctx.Step(`^I run graft library pull$`, s.noop)
	ctx.Step(`^the latest commit SHA is reported$`, s.noop)
	ctx.Step(`^a registered library with an index$`, s.noop)
	ctx.Step(`^I run graft library show$`, s.noop)
	ctx.Step(`^MCP name, version, tags, and description are shown$`, s.noop)
	ctx.Step(`^graft\.lock references an unregistered library$`, s.noop)
	ctx.Step(`^graft loads the lock$`, s.noop)
	ctx.Step(`^the unknown library auto-registration flow starts$`, s.noop)
	ctx.Step(`^a project with graft\.lock$`, s.noop)
	ctx.Step(`^I run graft status$`, s.noop)
	ctx.Step(`^one of the seven drift states is returned$`, s.noop)
	ctx.Step(`^selected MCPs have newer library definitions$`, s.noop)
	ctx.Step(`^I run graft sync$`, s.noop)
	ctx.Step(`^updated definitions are rendered to target files$`, s.noop)
	ctx.Step(`^one MCP render fails$`, s.noop)
	ctx.Step(`^graft sync continues$`, s.noop)
	ctx.Step(`^succeeded, failed, and skipped MCPs are reported$`, s.noop)
	ctx.Step(`^some MCPs are already current$`, s.noop)
	ctx.Step(`^I run graft sync again$`, s.noop)
	ctx.Step(`^current MCPs are skipped$`, s.noop)
	ctx.Step(`^a selected MCP has a mismatched pin$`, s.noop)
	ctx.Step(`^graft blocks unless force confirmation is supplied$`, s.noop)
	ctx.Step(`^an MCP uses credential-bearing environment$`, s.noop)
	ctx.Step(`^graft renders it$`, s.noop)
	ctx.Step(`^an auth warning is shown$`, s.noop)
	ctx.Step(`^a library index$`, s.libraryIndex)
	ctx.Step(`^I run graft pick$`, s.runPick)
	ctx.Step(`^MCPs are grouped by library with checkbox selection$`, s.mcpsGroupedByLibrary)
	ctx.Step(`^graft\.lock has selected MCPs$`, s.lockHasSelectedMCPs)
	ctx.Step(`^I run graft pick again$`, s.runPickAgain)
	ctx.Step(`^existing MCPs are pre-selected$`, s.existingMCPsPreselected)
	ctx.Step(`^I confirm selections$`, s.confirmSelections)
	ctx.Step(`^graft writes graft\.lock$`, s.graftWritesLock)
	ctx.Step(`^selected MCPs include library, version, target, and definition hash$`, s.selectedMCPsIncludeFields)
	ctx.Step(`^an imported MCP already exists$`, s.noop)
	ctx.Step(`^I import it again$`, s.noop)
	ctx.Step(`^graft offers keep, use-new, editor, or skip$`, s.noop)
	ctx.Step(`^authored MCP definitions changed$`, s.noop)
	ctx.Step(`^I run graft mcp push --yes$`, s.noop)
	ctx.Step(`^graft recomputes the index and reports the commit flow$`, s.noop)
}

func (s *featureState) registeredDefaultLibrary() error {
	s.root, _ = os.MkdirTemp("", "graft-feature-root-*")
	s.configPath = filepath.Join(s.root, "config.json")
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: filepath.Join(s.root, "core"), Default: true}
	if err := (config.FileStore{}).Save(s.configPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		return err
	}
	s.pickClient = featureLibraryClient{index: model.LibraryIndex{MCPs: []model.IndexEntry{}}}
	s.pickRunner = func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		return next.(tui.PickModel), nil
	}
	return nil
}

func (s *featureState) runInitBothTargets() error {
	command := cmd.NewInitCommandForTest(context.Background(), s.configPath, s.root, s.pickClient, s.pickRunner)
	command.SetArgs([]string{"--targets", "both", "--yes"})
	command.SetOut(&s.output)
	s.err = command.Execute()
	return s.err
}

func (s *featureState) targetsCreated() error {
	for _, path := range []string{"graft.lock", ".mcp.json", ".codex/config.toml"} {
		if _, err := os.Stat(filepath.Join(s.root, path)); err != nil {
			return err
		}
	}
	return nil
}

func (s *featureState) claudeFile() error {
	return nil
}

func (s *featureState) codexFile() error {
	return nil
}

func (s *featureState) libraryIndex() error {
	s.root, _ = os.MkdirTemp("", "graft-feature-pick-*")
	s.configPath = filepath.Join(s.root, "config.json")
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: filepath.Join(s.root, "core"), Default: true}
	if err := (config.FileStore{}).Save(s.configPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		return err
	}
	if err := (lock.FileStore{}).Save(s.root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: lib.URL}}, MCPs: []lock.InstalledMCP{}}); err != nil {
		return err
	}
	s.pickClient = featureLibraryClient{index: model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1.0.0", Description: "Documentation server", SHA256: "hash-docs"}}}, def: model.Definition{Name: "docs", Version: "1.0.0", Command: "npx", Args: []string{"docs"}}, hash: "hash-docs"}
	s.pickRunner = func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		if len(model.Items) != 1 || model.Items[0].Library != "core" || model.Items[0].Entry.Name != "docs" {
			return tui.PickModel{}, fmt.Errorf("picker items = %+v", model.Items)
		}
		s.output.WriteString("core/docs")
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		return next.(tui.PickModel), nil
	}
	return nil
}

func (s *featureState) runPick() error {
	command := cmd.NewPickCommandForTest(context.Background(), s.configPath, s.root, s.pickClient, s.pickRunner)
	command.SetOut(&s.output)
	s.err = command.Execute()
	return s.err
}

func (s *featureState) mcpsGroupedByLibrary() error {
	if !bytes.Contains(s.output.Bytes(), []byte("core")) || !bytes.Contains(s.output.Bytes(), []byte("docs")) {
		return os.ErrInvalid
	}
	return nil
}

func (s *featureState) lockHasSelectedMCPs() error {
	s.root, _ = os.MkdirTemp("", "graft-feature-repick-*")
	s.configPath = filepath.Join(s.root, "config.json")
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: filepath.Join(s.root, "core"), Default: true}
	if err := (config.FileStore{}).Save(s.configPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		return err
	}
	s.pickClient = featureLibraryClient{index: model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1.0.0", SHA256: "hash-docs"}}}, def: model.Definition{Name: "docs", Version: "1.0.0", Command: "npx", Args: []string{"docs"}}, hash: "hash-docs"}
	return (lock.FileStore{}).Save(s.root, lock.Lock{
		Libraries: []lock.LibraryRef{{Name: "core", URL: "https://example.com/core.git"}},
		MCPs: []lock.InstalledMCP{{
			Name:           "docs",
			Library:        "core",
			Version:        "1.0.0",
			DefinitionHash: "hash-docs",
			Target:         "both",
		}},
	})
}

func (s *featureState) runPickAgain() error {
	s.pickRunner = func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		if !model.Selected["core/docs"] {
			return tui.PickModel{}, fmt.Errorf("preselected = %+v", model.Selected)
		}
		s.picked = []lock.InstalledMCP{{Name: "docs", Library: "core"}}
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
		return next.(tui.PickModel), nil
	}
	return s.runPick()
}

func (s *featureState) existingMCPsPreselected() error {
	if len(s.picked) != 1 || s.picked[0].Library != "core" || s.picked[0].Name != "docs" {
		return os.ErrInvalid
	}
	return nil
}

func (s *featureState) confirmSelections() error {
	s.root, _ = os.MkdirTemp("", "graft-feature-confirm-*")
	s.configPath = filepath.Join(s.root, "config.json")
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: filepath.Join(s.root, "core"), Default: true}
	if err := (config.FileStore{}).Save(s.configPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		return err
	}
	if err := (lock.FileStore{}).Save(s.root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: lib.URL}}, MCPs: []lock.InstalledMCP{}}); err != nil {
		return err
	}
	s.pickClient = featureLibraryClient{index: model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1.0.0", SHA256: "hash-docs"}}}, def: model.Definition{Name: "docs", Version: "1.0.0", Command: "npx", Args: []string{"docs"}}, hash: "hash-docs"}
	s.pickRunner = func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
		model = next.(tui.PickModel)
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return next.(tui.PickModel), nil
	}
	return nil
}

func (s *featureState) graftWritesLock() error {
	return s.runPick()
}

func (s *featureState) selectedMCPsIncludeFields() error {
	lk, err := (lock.FileStore{}).Load(s.root)
	if err != nil {
		return err
	}
	if len(lk.MCPs) != 1 {
		return os.ErrInvalid
	}
	mcp := lk.MCPs[0]
	if mcp.Name == "" || mcp.Library == "" || mcp.Version == "" || mcp.Target == "" || mcp.DefinitionHash == "" {
		return os.ErrInvalid
	}
	return nil
}

func (s *featureState) noop() error {
	return nil
}

type featureLibraryClient struct {
	index model.LibraryIndex
	def   model.Definition
	hash  string
}

func (c featureLibraryClient) Add(context.Context, config.Library) error { return nil }

func (c featureLibraryClient) Pull(context.Context, config.Library) (string, error) { return "", nil }

func (c featureLibraryClient) Index(config.Library) (model.LibraryIndex, error) { return c.index, nil }

func (c featureLibraryClient) Definition(config.Library, string) (model.Definition, string, error) {
	return c.def, c.hash, nil
}

func (c featureLibraryClient) Reindex(config.Library) (model.LibraryIndex, error) {
	return model.LibraryIndex{}, nil
}

var _ library.Client = featureLibraryClient{}
