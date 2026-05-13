//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock/client.go -package=mock github.com/poconnor/graft/internal/library Client

// Package library implements the git-backed MCP library client. Libraries are plain
// git repositories with a library.json index and an mcps/ directory of JSON definitions.
package library

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/poconnor/graft/internal/config"
	"github.com/poconnor/graft/internal/fileutil"
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/model"
)

// Client abstracts the git operations needed to interact with an MCP library repository.
type Client interface {
	Add(ctx context.Context, lib config.Library) error
	Pull(ctx context.Context, lib config.Library) (string, error)
	Index(lib config.Library) (model.LibraryIndex, error)
	Definition(lib config.Library, name string) (model.Definition, string, error)
	Reindex(lib config.Library) (model.LibraryIndex, error)
}

// GitClient implements Client by shelling out to git. GitPath overrides the git binary
// (defaults to "git"); Timeout, if positive, caps each git subprocess.
type GitClient struct {
	GitPath string
	Timeout time.Duration
}

// Add clones lib.URL into lib.CachePath. It is a no-op if the cache directory already exists.
func (c GitClient) Add(ctx context.Context, lib config.Library) error {
	if lib.URL == "" {
		return fmt.Errorf("library %q requires url", lib.Name)
	}
	if lib.CachePath == "" {
		return fmt.Errorf("library %q requires cache path", lib.Name)
	}
	if _, err := os.Stat(lib.CachePath); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(lib.CachePath), 0o755); err != nil {
		return fmt.Errorf("create cache parent: %w", err)
	}
	gitCtx, cancel := c.commandContext(ctx)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, c.gitPath(), "clone", lib.URL, lib.CachePath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("clone library: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Pull fast-forwards the local clone of lib and returns the resulting HEAD SHA.
func (c GitClient) Pull(ctx context.Context, lib config.Library) (string, error) {
	gitCtx, cancel := c.commandContext(ctx)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, c.gitPath(), "-C", lib.CachePath, "pull", "--ff-only")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("pull library: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	sha, err := c.gitSHA(ctx, lib.CachePath)
	if err != nil {
		return "", err
	}
	return sha, nil
}

// Index reads and parses the library.json index from the local clone of lib.
func (GitClient) Index(lib config.Library) (model.LibraryIndex, error) {
	data, err := os.ReadFile(filepath.Join(lib.CachePath, "library.json"))
	if err != nil {
		return model.LibraryIndex{MCPs: []model.IndexEntry{}}, fmt.Errorf("read library index: %w", err)
	}
	var index model.LibraryIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return model.LibraryIndex{MCPs: []model.IndexEntry{}}, fmt.Errorf("parse library index: %w", err)
	}
	if index.MCPs == nil {
		index.MCPs = []model.IndexEntry{}
	}
	return index, nil
}

// Definition reads and parses a single MCP definition from the library's mcps/ directory.
// Returns the definition, its SHA256 hash, and any error.
func (GitClient) Definition(lib config.Library, name string) (model.Definition, string, error) {
	if err := ValidateMCPName(name); err != nil {
		return model.Definition{}, "", err
	}
	path := filepath.Join(lib.CachePath, "mcps", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return model.Definition{}, "", fmt.Errorf("read definition: %w", err)
	}
	var def model.Definition
	if err := json.Unmarshal(data, &def); err != nil {
		return model.Definition{}, "", fmt.Errorf("parse definition: %w", err)
	}
	return def, lock.HashBytes(data), nil
}

func (GitClient) Reindex(lib config.Library) (model.LibraryIndex, error) {
	dir := filepath.Join(lib.CachePath, "mcps")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return model.LibraryIndex{MCPs: []model.IndexEntry{}}, fmt.Errorf("read mcps dir: %w", err)
	}
	index := model.LibraryIndex{Name: lib.Name, MCPs: []model.IndexEntry{}}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return index, err
		}
		var def model.Definition
		if err := json.Unmarshal(data, &def); err != nil {
			return index, err
		}
		index.MCPs = append(index.MCPs, model.IndexEntry{
			Name:        def.Name,
			Version:     def.Version,
			Description: def.Description,
			Tags:        append([]string{}, def.Tags...),
			SHA256:      lock.HashBytes(data),
		})
	}
	sort.Slice(index.MCPs, func(i, j int) bool {
		return index.MCPs[i].Name < index.MCPs[j].Name
	})
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return index, err
	}
	if err := fileutil.AtomicWriteFile(filepath.Join(lib.CachePath, "library.json"), append(data, '\n'), 0o600); err != nil {
		return index, err
	}
	return index, nil
}

func (c GitClient) gitSHA(ctx context.Context, path string) (string, error) {
	gitCtx, cancel := c.commandContext(ctx)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, c.gitPath(), "-C", path, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read git sha: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (c GitClient) gitPath() string {
	if c.GitPath != "" {
		return c.GitPath
	}
	return "git"
}

func (c GitClient) commandContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if c.Timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.Timeout)
}

func WriteDefinition(lib config.Library, def model.Definition) (string, error) {
	return WriteDefinitionFile(lib, def, false)
}

func WriteDefinitionFile(lib config.Library, def model.Definition, overwrite bool) (string, error) {
	if def.Name == "" {
		return "", fmt.Errorf("definition name is required")
	}
	if err := ValidateMCPName(def.Name); err != nil {
		return "", err
	}
	NormalizeDefinition(&def)
	if def.Env == nil {
		def.Env = map[string]string{}
	}
	path := filepath.Join(lib.CachePath, "mcps", def.Name+".json")
	if _, err := os.Stat(path); err == nil && !overwrite {
		return "", fmt.Errorf("MCP %q already exists; choose keep/use-new/editor/skip", def.Name)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create mcps dir: %w", err)
	}
	data, err := json.MarshalIndent(def, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal definition: %w", err)
	}
	if err := fileutil.AtomicWriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write definition: %w", err)
	}
	return path, nil
}

func ValidateMCPName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid MCP name %q", name)
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("invalid MCP name %q", name)
	}
	return nil
}

func NormalizeDefinition(def *model.Definition) {
	if def.Tags == nil {
		def.Tags = []string{}
	}
	if def.Args == nil {
		def.Args = []string{}
	}
	if def.Env == nil {
		def.Env = map[string]string{}
	}
	if def.Adapters == nil {
		def.Adapters = map[string]model.AdapterConfig{}
	}
}

func ImportFile(path string) ([]model.Definition, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return importClaude(path)
	case ".toml":
		return importCodex(path)
	default:
		return nil, fmt.Errorf("unsupported import format %q", filepath.Ext(path))
	}
}

func importClaude(path string) ([]model.Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	defs := []model.Definition{}
	for name, server := range doc.MCPServers {
		if err := ValidateMCPName(name); err != nil {
			return nil, err
		}
		defs = append(defs, model.Definition{
			Name:        name,
			Version:     time.Now().UTC().Format("20060102150405"),
			Description: "Imported from Claude MCP JSON",
			Command:     server.Command,
			Args:        append([]string{}, server.Args...),
			Env:         cloneMap(server.Env),
			Adapters: map[string]model.AdapterConfig{
				"claude": {
					Command: server.Command,
					Args:    append([]string{}, server.Args...),
					Env:     cloneMap(server.Env),
				},
			},
		})
	}
	sortDefinitions(defs)
	return defs, nil
}

func importCodex(path string) ([]model.Definition, error) {
	var doc struct {
		MCPServers map[string]struct {
			Command string            `toml:"command"`
			Args    []string          `toml:"args"`
			Env     map[string]string `toml:"env"`
		} `toml:"mcp_servers"`
	}
	if _, err := toml.DecodeFile(path, &doc); err != nil {
		return nil, err
	}
	defs := []model.Definition{}
	for name, server := range doc.MCPServers {
		if err := ValidateMCPName(name); err != nil {
			return nil, err
		}
		defs = append(defs, model.Definition{
			Name:        name,
			Version:     time.Now().UTC().Format("20060102150405"),
			Description: "Imported from Codex TOML",
			Command:     server.Command,
			Args:        append([]string{}, server.Args...),
			Env:         cloneMap(server.Env),
			Adapters: map[string]model.AdapterConfig{
				"codex": {
					Command: server.Command,
					Args:    append([]string{}, server.Args...),
					Env:     cloneMap(server.Env),
				},
			},
		})
	}
	sortDefinitions(defs)
	return defs, nil
}

func cloneMap(source map[string]string) map[string]string {
	next := map[string]string{}
	for key, value := range source {
		next[key] = value
	}
	return next
}

func sortDefinitions(defs []model.Definition) {
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})
}
