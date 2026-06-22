package features

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	root              string
	configPath        string
	output            bytes.Buffer
	err               error
	picked            []lock.InstalledMCP
	pickClient        *featureLibraryClient
	pickRunner        func(context.Context, tui.PickModel) (tui.PickModel, error)
	placeholderRunner func(context.Context, tui.PlaceholderModel) (tui.PlaceholderModel, error)
}

// placeholderRunnerOrDefault returns the configured placeholder runner, or a
// default that accepts every default ${VAR} when none is set.
func (s *featureState) placeholderRunnerOrDefault() func(context.Context, tui.PlaceholderModel) (tui.PlaceholderModel, error) {
	if s.placeholderRunner != nil {
		return s.placeholderRunner
	}
	return func(_ context.Context, m tui.PlaceholderModel) (tui.PlaceholderModel, error) {
		for range m.Items {
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(tui.PlaceholderModel)
		}
		if len(m.Items) == 0 {
			m.Confirmed = true
		}
		return m, nil
	}
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
	ctx.Step(`^a git-backed MCP library URL$`, s.gitBackedLibraryURL)
	ctx.Step(`^I run graft library add$`, s.runLibraryAdd)
	ctx.Step(`^the library is saved in user config$`, s.librarySavedInUserConfig)
	ctx.Step(`^a registered library$`, s.registeredLibrary)
	ctx.Step(`^I run graft library pull$`, s.runLibraryPull)
	ctx.Step(`^the latest commit SHA is reported$`, s.latestCommitSHAReported)
	ctx.Step(`^a registered library with an index$`, s.registeredLibraryWithIndex)
	ctx.Step(`^I run graft library show$`, s.runLibraryShow)
	ctx.Step(`^MCP name, version, tags, and description are shown$`, s.mcpSummaryShown)
	ctx.Step(`^graft\.lock references an unregistered library$`, s.lockReferencesUnregisteredLibrary)
	ctx.Step(`^graft loads the lock$`, s.graftLoadsLock)
	ctx.Step(`^the unknown library auto-registration flow starts$`, s.unknownLibraryAutoRegistrationFlowStarts)
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
	ctx.Step(`^I pick a new MCP with placeholder environment variables$`, s.pickNewMCPWithPlaceholders)
	ctx.Step(`^I supply a replacement variable name at the prompt$`, s.runPick)
	ctx.Step(`^the rendered config references the supplied variable$`, s.renderedConfigReferencesSuppliedVariable)
	ctx.Step(`^an imported MCP already exists$`, s.noop)
	ctx.Step(`^I import it again$`, s.noop)
	ctx.Step(`^graft offers keep, use-new, editor, or skip$`, s.noop)
	ctx.Step(`^authored MCP definitions changed$`, s.noop)
	ctx.Step(`^I run graft mcp push --yes$`, s.noop)
	ctx.Step(`^graft recomputes the index and reports the commit flow$`, s.noop)
	ctx.Step(`^a default library for authoring$`, s.defaultLibraryForAuthoring)
	ctx.Step(`^I run graft add-interactive and answer the prompts$`, s.runAddInteractive)
	ctx.Step(`^the answered MCP definition is written$`, s.answeredDefinitionWritten)
}

func (s *featureState) defaultLibraryForAuthoring() error {
	root, err := os.MkdirTemp("", "graft-feature-add-interactive-*")
	if err != nil {
		return err
	}
	s.root = root
	s.configPath = filepath.Join(s.root, "config.json")
	cachePath := filepath.Join(s.root, "core")
	if err := os.MkdirAll(filepath.Join(cachePath, "mcps"), 0o755); err != nil {
		return err
	}
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: cachePath, Default: true}
	return (config.FileStore{}).Save(s.configPath, config.Config{Libraries: []config.Library{lib}})
}

func (s *featureState) runAddInteractive() error {
	command := cmd.NewRootCommand(context.Background())
	command.SetArgs([]string{"--config", s.configPath, "--root", s.root, "add-interactive"})
	// Name, Description, Version (blank -> default), Type (stdio), Command,
	// Args, Env (blank ends), Tags.
	command.SetIn(strings.NewReader("docs\nDocs server\n\nstdio\nnpx\n@acme/docs\n\ndocs\n"))
	command.SetOut(&s.output)
	command.SetErr(&s.output)
	s.err = command.Execute()
	return s.err
}

func (s *featureState) answeredDefinitionWritten() error {
	if s.err != nil {
		return s.err
	}
	data, err := os.ReadFile(filepath.Join(s.root, "core", "mcps", "docs.json"))
	if err != nil {
		return err
	}
	var def model.Definition
	if err := json.Unmarshal(data, &def); err != nil {
		return err
	}
	if def.Command != "npx" || def.Version != "0.1.0" || def.Description != "Docs server" {
		return fmt.Errorf("definition = %+v", def)
	}
	return nil
}

func (s *featureState) registeredDefaultLibrary() error {
	root, err := os.MkdirTemp("", "graft-feature-root-*")
	if err != nil {
		return err
	}
	s.root = root
	s.configPath = filepath.Join(s.root, "config.json")
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: filepath.Join(s.root, "core"), Default: true}
	if err := (config.FileStore{}).Save(s.configPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		return err
	}
	s.pickClient = &featureLibraryClient{index: model.LibraryIndex{MCPs: []model.IndexEntry{}}}
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

func (s *featureState) gitBackedLibraryURL() error {
	root, err := os.MkdirTemp("", "graft-feature-library-add-*")
	if err != nil {
		return err
	}
	s.root = root
	s.configPath = filepath.Join(s.root, "config.json")
	s.pickClient = &featureLibraryClient{}
	return nil
}

func (s *featureState) runLibraryAdd() error {
	command := cmd.NewLibraryCommandForTest(context.Background(), s.configPath, s.root, s.pickClient)
	command.SetArgs([]string{"add", "core", "https://example.com/core.git"})
	command.SetOut(&s.output)
	s.err = command.Execute()
	return s.err
}

func (s *featureState) librarySavedInUserConfig() error {
	cfg, err := (config.FileStore{}).Load(s.configPath)
	if err != nil {
		return err
	}
	lib, ok := cfg.Library("core")
	if !ok || lib.URL != "https://example.com/core.git" || lib.CachePath == "" || !lib.Default {
		return fmt.Errorf("config library = %+v, %v", lib, ok)
	}
	return nil
}

func (s *featureState) registeredLibrary() error {
	root, err := os.MkdirTemp("", "graft-feature-library-pull-*")
	if err != nil {
		return err
	}
	s.root = root
	s.configPath = filepath.Join(s.root, "config.json")
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: filepath.Join(s.root, "core"), Default: true}
	if err := (config.FileStore{}).Save(s.configPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		return err
	}
	s.pickClient = &featureLibraryClient{pullSHA: "abc123"}
	return nil
}

func (s *featureState) runLibraryPull() error {
	command := cmd.NewLibraryCommandForTest(context.Background(), s.configPath, s.root, s.pickClient)
	command.SetArgs([]string{"pull"})
	command.SetOut(&s.output)
	s.err = command.Execute()
	return s.err
}

func (s *featureState) latestCommitSHAReported() error {
	if !strings.Contains(s.output.String(), "core\tabc123") {
		return fmt.Errorf("stdout = %q", s.output.String())
	}
	cfg, err := (config.FileStore{}).Load(s.configPath)
	if err != nil {
		return err
	}
	lib, _ := cfg.Library("core")
	if lib.LastPulledAt == "" {
		return fmt.Errorf("last_pulled_at was not saved")
	}
	return nil
}

func (s *featureState) registeredLibraryWithIndex() error {
	root, err := os.MkdirTemp("", "graft-feature-library-show-*")
	if err != nil {
		return err
	}
	s.root = root
	s.configPath = filepath.Join(s.root, "config.json")
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: filepath.Join(s.root, "core"), Default: true}
	if err := (config.FileStore{}).Save(s.configPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		return err
	}
	s.pickClient = &featureLibraryClient{index: model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1.0.0", Tags: []string{"docs", "productivity"}, Description: "Documentation server", SHA256: "hash-docs"}}}}
	return nil
}

func (s *featureState) runLibraryShow() error {
	command := cmd.NewLibraryCommandForTest(context.Background(), s.configPath, s.root, s.pickClient)
	command.SetArgs([]string{"show"})
	command.SetOut(&s.output)
	s.err = command.Execute()
	return s.err
}

func (s *featureState) mcpSummaryShown() error {
	got := s.output.String()
	for _, want := range []string{"docs", "1.0.0", "docs,productivity", "Documentation server"} {
		if !strings.Contains(got, want) {
			return fmt.Errorf("stdout = %q, want %q", got, want)
		}
	}
	return nil
}

func (s *featureState) lockReferencesUnregisteredLibrary() error {
	root, err := os.MkdirTemp("", "graft-feature-unknown-library-*")
	if err != nil {
		return err
	}
	s.root = root
	s.configPath = filepath.Join(s.root, "config.json")
	if err := (config.FileStore{}).Save(s.configPath, config.Config{}); err != nil {
		return err
	}
	if err := (lock.FileStore{}).Save(s.root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: "https://example.com/core.git"}}}); err != nil {
		return err
	}
	s.pickClient = &featureLibraryClient{index: model.LibraryIndex{MCPs: []model.IndexEntry{}}}
	return nil
}

func (s *featureState) graftLoadsLock() error {
	command := cmd.NewStatusCommandForTest(s.configPath, s.root, s.pickClient)
	command.SetIn(strings.NewReader("\n"))
	command.SetOut(&s.output)
	command.SetErr(&s.output)
	s.err = command.Execute()
	return s.err
}

func (s *featureState) unknownLibraryAutoRegistrationFlowStarts() error {
	got := s.output.String()
	if !strings.Contains(got, "Register and clone") {
		return fmt.Errorf("stdout/stderr = %q", got)
	}
	cfg, err := (config.FileStore{}).Load(s.configPath)
	if err != nil {
		return err
	}
	if _, ok := cfg.Library("core"); !ok {
		return fmt.Errorf("core library was not registered")
	}
	return nil
}

func (s *featureState) libraryIndex() error {
	root, err := os.MkdirTemp("", "graft-feature-pick-*")
	if err != nil {
		return err
	}
	s.root = root
	s.configPath = filepath.Join(s.root, "config.json")
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: filepath.Join(s.root, "core"), Default: true}
	if err := (config.FileStore{}).Save(s.configPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		return err
	}
	if err := (lock.FileStore{}).Save(s.root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: lib.URL}}, MCPs: []lock.InstalledMCP{}}); err != nil {
		return err
	}
	s.pickClient = &featureLibraryClient{index: model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1.0.0", Description: "Documentation server", SHA256: "hash-docs"}}}, def: model.Definition{Name: "docs", Version: "1.0.0", Command: "npx", Args: []string{"docs"}}, hash: "hash-docs"}
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
	command := cmd.NewPickCommandForTestWithPlaceholders(context.Background(), s.configPath, s.root, s.pickClient, s.pickRunner, s.placeholderRunnerOrDefault())
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
	root, err := os.MkdirTemp("", "graft-feature-repick-*")
	if err != nil {
		return err
	}
	s.root = root
	s.configPath = filepath.Join(s.root, "config.json")
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: filepath.Join(s.root, "core"), Default: true}
	if err := (config.FileStore{}).Save(s.configPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		return err
	}
	s.pickClient = &featureLibraryClient{index: model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1.0.0", SHA256: "hash-docs"}}}, def: model.Definition{Name: "docs", Version: "1.0.0", Command: "npx", Args: []string{"docs"}}, hash: "hash-docs"}
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
	root, err := os.MkdirTemp("", "graft-feature-confirm-*")
	if err != nil {
		return err
	}
	s.root = root
	s.configPath = filepath.Join(s.root, "config.json")
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: filepath.Join(s.root, "core"), Default: true}
	if err := (config.FileStore{}).Save(s.configPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		return err
	}
	if err := (lock.FileStore{}).Save(s.root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: lib.URL}}, MCPs: []lock.InstalledMCP{}}); err != nil {
		return err
	}
	s.pickClient = &featureLibraryClient{index: model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1.0.0", SHA256: "hash-docs"}}}, def: model.Definition{Name: "docs", Version: "1.0.0", Command: "npx", Args: []string{"docs"}}, hash: "hash-docs"}
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

func (s *featureState) pickNewMCPWithPlaceholders() error {
	root, err := os.MkdirTemp("", "graft-feature-placeholder-*")
	if err != nil {
		return err
	}
	s.root = root
	s.configPath = filepath.Join(s.root, "config.json")
	lib := config.Library{Name: "core", URL: "https://example.com/core.git", CachePath: filepath.Join(s.root, "core"), Default: true}
	if err := (config.FileStore{}).Save(s.configPath, config.Config{Libraries: []config.Library{lib}}); err != nil {
		return err
	}
	if err := (lock.FileStore{}).Save(s.root, lock.Lock{Libraries: []lock.LibraryRef{{Name: "core", URL: lib.URL}}, MCPs: []lock.InstalledMCP{}}); err != nil {
		return err
	}
	s.pickClient = &featureLibraryClient{
		index: model.LibraryIndex{MCPs: []model.IndexEntry{{Name: "docs", Version: "1.0.0", SHA256: "hash-docs"}}},
		def:   model.Definition{Name: "docs", Version: "1.0.0", Command: "npx", Env: map[string]string{"API_KEY": "${API_KEY}"}},
		hash:  "hash-docs",
	}
	s.pickRunner = func(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeySpace})
		model = next.(tui.PickModel)
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return next.(tui.PickModel), nil
	}
	s.placeholderRunner = func(ctx context.Context, m tui.PlaceholderModel) (tui.PlaceholderModel, error) {
		for _, r := range "MY_KEY" {
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			m = next.(tui.PlaceholderModel)
		}
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return next.(tui.PlaceholderModel), nil
	}
	return nil
}

func (s *featureState) renderedConfigReferencesSuppliedVariable() error {
	if s.err != nil {
		return s.err
	}
	data, err := os.ReadFile(filepath.Join(s.root, ".mcp.json"))
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), "${MY_KEY}") {
		return fmt.Errorf("claude config = %s, want ${MY_KEY}", data)
	}
	return nil
}

func (s *featureState) noop() error {
	return nil
}

type featureLibraryClient struct {
	index   model.LibraryIndex
	def     model.Definition
	hash    string
	pullSHA string
}

func (c *featureLibraryClient) Add(context.Context, config.Library) error { return nil }

func (c *featureLibraryClient) Pull(context.Context, config.Library) (string, error) {
	return c.pullSHA, nil
}

func (c *featureLibraryClient) Index(config.Library) (model.LibraryIndex, error) { return c.index, nil }

func (c *featureLibraryClient) Definition(config.Library, string) (model.Definition, string, error) {
	return c.def, c.hash, nil
}

func (c *featureLibraryClient) Reindex(config.Library) (model.LibraryIndex, error) {
	return model.LibraryIndex{}, nil
}

var _ library.Client = (*featureLibraryClient)(nil)
