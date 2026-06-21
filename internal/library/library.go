//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock/client.go -package=mock github.com/poconnor/graft/internal/library Client

// Package library implements the git-backed MCP library client. Libraries are plain
// git repositories with a library.json index and an mcps/ directory of JSON definitions.
package library

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// Reindex scans mcps/*.json in the local clone, rebuilds the library.json index, and
// writes it atomically. Returns the updated index.
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

// Diff stages library-managed files and returns the exact diff that push will commit.
func (c GitClient) Diff(ctx context.Context, cachePath string) (string, error) {
	if err := c.stageLibraryFiles(ctx, cachePath); err != nil {
		return "", err
	}
	gitCtx, cancel := c.commandContext(ctx)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, c.gitPath(), "-C", cachePath, "diff", "--cached", "--", "library.json", "mcps")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git diff --cached: %w", err)
	}
	return string(out), nil
}

// PushAll stages library files, commits staged changes when present, pushes, and returns HEAD.
func (c GitClient) PushAll(ctx context.Context, cachePath, message string) (string, error) {
	if err := c.stageLibraryFiles(ctx, cachePath); err != nil {
		return "", err
	}
	changed, err := c.hasStagedChanges(ctx, cachePath)
	if err != nil {
		return "", err
	}
	if changed {
		if err := c.runGit(ctx, cachePath, "commit", "-m", message, "--", "library.json", "mcps"); err != nil {
			return "", err
		}
	}
	if err := c.runGit(ctx, cachePath, "push", "-u", "origin", "HEAD"); err != nil {
		return "", err
	}
	return c.gitSHA(ctx, cachePath)
}

func (c GitClient) stageLibraryFiles(ctx context.Context, cachePath string) error {
	return c.runGit(ctx, cachePath, "add", "library.json", "mcps")
}

func (c GitClient) hasStagedChanges(ctx context.Context, cachePath string) (bool, error) {
	gitCtx, cancel := c.commandContext(ctx)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, c.gitPath(), "-C", cachePath, "diff", "--cached", "--quiet", "--", "library.json", "mcps")
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return true, nil
		}
		return false, fmt.Errorf("git diff --cached --quiet: %w", err)
	}
	return false, nil
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

// InitLocal initializes a local git-backed library cache. When force is true,
// the existing cache directory is removed before initialization.
func (c GitClient) InitLocal(ctx context.Context, lib config.Library, force bool) error {
	if err := ValidateMCPName(lib.Name); err != nil {
		return err
	}
	if lib.CachePath == "" {
		return fmt.Errorf("library %q requires cache path", lib.Name)
	}
	if force {
		if err := os.RemoveAll(lib.CachePath); err != nil {
			return fmt.Errorf("remove existing library cache: %w", err)
		}
	} else if _, err := os.Stat(lib.CachePath); err == nil {
		return fmt.Errorf("library cache %q already exists; pass --force to recreate", lib.CachePath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check library cache: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(lib.CachePath, "mcps"), 0o700); err != nil {
		return fmt.Errorf("create library cache: %w", err)
	}
	if err := c.runGit(ctx, lib.CachePath, "init"); err != nil {
		return err
	}
	return nil
}

// CommitAll stages all library files and creates a git commit with message.
func (c GitClient) CommitAll(ctx context.Context, cachePath, message string) error {
	if err := c.runGit(ctx, cachePath, "add", "."); err != nil {
		return err
	}
	return c.runGit(ctx, cachePath, "-c", "user.name=graft", "-c", "user.email=graft@example.invalid", "commit", "-m", message)
}

func (c GitClient) runGit(ctx context.Context, path string, args ...string) error {
	gitCtx, cancel := c.commandContext(ctx)
	defer cancel()
	cmd := exec.CommandContext(gitCtx, c.gitPath(), append([]string{"-C", path}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, message)
	}
	return nil
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

// WriteDefinition writes def to the library's mcps/<name>.json. Returns an error if the file
// already exists; use WriteDefinitionFile with overwrite=true to replace it.
func WriteDefinition(lib config.Library, def model.Definition) (string, error) {
	return WriteDefinitionFile(lib, def, false)
}

// WriteDefinitionFile writes def to mcps/<name>.json in the library cache. When overwrite is false
// and the file already exists, it returns an error instructing the caller to resolve the conflict.
// Returns the absolute path of the written file.
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
	if def.Headers == nil {
		def.Headers = map[string]string{}
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

// ValidateMCPName returns an error if name contains path separators or would escape
// the mcps/ directory via ".." traversal. Empty names are also rejected.
func ValidateMCPName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid MCP name %q", name)
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("invalid MCP name %q", name)
	}
	return nil
}

// NormalizeDefinition ensures all slice and map fields are non-nil so that JSON
// serialization produces [] and {} rather than null.
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
	if def.Headers == nil {
		def.Headers = map[string]string{}
	}
	if def.Adapters == nil {
		def.Adapters = map[string]model.AdapterConfig{}
	}
}

// ImportFile reads an existing Claude .mcp.json or Codex .codex/config.toml and converts
// each server entry into a model.Definition. The format is inferred from the file extension.
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

// claudeServerJSON is the wire shape of a single MCP server entry as it appears in
// a Claude .mcp.json file, used by both file import and pasted-JSON parsing.
type claudeServerJSON struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// parseClaudeServers decodes the wrapped {"mcpServers": {...}} form into a map of
// server name to its raw fields. A document without an mcpServers object yields an
// empty map (not an error) so callers can fall back to a bare single-server shape.
func parseClaudeServers(data []byte) (map[string]claudeServerJSON, error) {
	var doc struct {
		MCPServers map[string]claudeServerJSON `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.MCPServers, nil
}

func importClaude(path string) ([]model.Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	servers, err := parseClaudeServers(data)
	if err != nil {
		return nil, err
	}
	defs := []model.Definition{}
	for name, server := range servers {
		if err := ValidateMCPName(name); err != nil {
			return nil, err
		}
		defs = append(defs, model.Definition{
			Name:        name,
			Version:     time.Now().UTC().Format("20060102150405"),
			Description: "Imported from Claude MCP JSON",
			Type:        server.Type,
			Command:     server.Command,
			Args:        append([]string{}, server.Args...),
			Env:         placeholderMap(server.Env),
			URL:         server.URL,
			Headers:     placeholderMap(server.Headers),
			Adapters: map[string]model.AdapterConfig{
				"claude": {
					Type:    server.Type,
					Command: server.Command,
					Args:    append([]string{}, server.Args...),
					Env:     placeholderMap(server.Env),
					URL:     server.URL,
					Headers: placeholderMap(server.Headers),
				},
			},
		})
	}
	sortDefinitions(defs)
	return defs, nil
}

// baseDefinition builds a tool-agnostic Definition from a raw server entry. It sets
// no per-tool Adapters override and applies no redaction, so the result renders to
// both Claude and Codex and is ready for ValidateDefinition / RedactSecrets.
func baseDefinition(name string, server claudeServerJSON) model.Definition {
	def := model.Definition{
		Name:    name,
		Version: "0.1.0",
		Type:    server.Type,
		Command: server.Command,
		Args:    append([]string{}, server.Args...),
		Env:     map[string]string{},
		URL:     server.URL,
		Headers: map[string]string{},
	}
	for key, value := range server.Env {
		def.Env[key] = value
	}
	for key, value := range server.Headers {
		def.Headers[key] = value
	}
	NormalizeDefinition(&def)
	return def
}

// ParseMCPJSON parses pasted MCP JSON in either the wrapped {"mcpServers":{...}}
// form (one definition per key) or a bare single-server object (name taken from a
// top-level "name" field, else nameOverride). Returned definitions carry literal
// values verbatim; callers apply RedactSecrets and ValidateDefinition.
func ParseMCPJSON(data []byte, nameOverride string) ([]model.Definition, error) {
	servers, err := parseClaudeServers(data)
	if err != nil {
		return nil, fmt.Errorf("parse MCP JSON: %w", err)
	}
	if len(servers) > 0 {
		defs := make([]model.Definition, 0, len(servers))
		for name, server := range servers {
			defs = append(defs, baseDefinition(name, server))
		}
		sortDefinitions(defs)
		return defs, nil
	}
	var bare struct {
		Name string `json:"name"`
		claudeServerJSON
	}
	if err := json.Unmarshal(data, &bare); err != nil {
		return nil, fmt.Errorf("parse MCP JSON: %w", err)
	}
	name := bare.Name
	if name == "" {
		name = nameOverride
	}
	if name == "" {
		return nil, fmt.Errorf("MCP name required: add a positional name or a \"name\" field to the JSON")
	}
	return []model.Definition{baseDefinition(name, bare.claudeServerJSON)}, nil
}

// ValidateDefinition rejects definitions that cannot produce a usable MCP entry:
// an invalid name, an unknown transport type, a stdio server without a command, or
// a remote (http/sse) server without a url.
func ValidateDefinition(def model.Definition) error {
	if err := ValidateMCPName(def.Name); err != nil {
		return err
	}
	switch def.Type {
	case "", "stdio":
		if def.Command == "" {
			return fmt.Errorf("stdio MCP %q requires a command", def.Name)
		}
	case "http", "sse":
		if def.URL == "" {
			return fmt.Errorf("%s MCP %q requires a url", def.Type, def.Name)
		}
	default:
		return fmt.Errorf("MCP %q has unknown type %q (want stdio, http, or sse)", def.Name, def.Type)
	}
	return nil
}

// RedactSecrets rewrites literal secret-looking env/header values into ${KEY}
// placeholders, leaves existing ${...} references and ordinary literals untouched,
// and returns the sorted list of placeholder variable names it created.
func RedactSecrets(def *model.Definition) []string {
	redacted := map[string]bool{}
	redactMap := func(values map[string]string) {
		for key, value := range values {
			if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
				continue
			}
			if IsSensitiveField(key, value) {
				values[key] = "${" + key + "}"
				redacted[key] = true
			}
		}
	}
	redactMap(def.Env)
	redactMap(def.Headers)
	names := make([]string, 0, len(redacted))
	for name := range redacted {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// IsSensitiveField reports whether an env or header key/value pair looks
// credential-bearing and should be redacted into a ${KEY} placeholder.
func IsSensitiveField(key, value string) bool {
	upperKey := strings.ToUpper(key)
	upperValue := strings.ToUpper(value)
	return strings.Contains(upperKey, "TOKEN") ||
		strings.Contains(upperKey, "SECRET") ||
		strings.Contains(upperKey, "KEY") ||
		strings.Contains(upperKey, "PASSWORD") ||
		strings.Contains(upperKey, "CREDENTIAL") ||
		upperKey == "AUTHORIZATION" ||
		upperKey == "BEARER_TOKEN_ENV_VAR" ||
		strings.Contains(upperValue, "BEARER ")
}

func importCodex(path string) ([]model.Definition, error) {
	var doc struct {
		MCPServers map[string]struct {
			Command string            `toml:"command"`
			Args    []string          `toml:"args"`
			Env     map[string]string `toml:"env"`
			Type    string            `toml:"type"`
			URL     string            `toml:"url"`
			Headers map[string]string `toml:"headers"`
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
			Type:        server.Type,
			Command:     server.Command,
			Args:        append([]string{}, server.Args...),
			Env:         placeholderMap(server.Env),
			URL:         server.URL,
			Headers:     placeholderMap(server.Headers),
			Adapters: map[string]model.AdapterConfig{
				"codex": {
					Type:    server.Type,
					Command: server.Command,
					Args:    append([]string{}, server.Args...),
					Env:     placeholderMap(server.Env),
					URL:     server.URL,
					Headers: placeholderMap(server.Headers),
				},
			},
		})
	}
	sortDefinitions(defs)
	return defs, nil
}

func placeholderMap(source map[string]string) map[string]string {
	next := map[string]string{}
	for key, value := range source {
		if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") {
			next[key] = value
			continue
		}
		next[key] = "${" + key + "}"
	}
	return next
}

func sortDefinitions(defs []model.Definition) {
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Name < defs[j].Name
	})
}
