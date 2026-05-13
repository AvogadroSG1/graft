//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock/adapter.go -package=mock github.com/poconnor/graft/internal/render Adapter

// Package render writes resolved MCP definitions into AI tool config files.
// ClaudeAdapter targets .mcp.json; CodexAdapter targets .codex/config.toml.
// Both adapters refuse to overwrite entries that were not placed there by graft.
package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
	"github.com/poconnor/graft/internal/fileutil"
	"github.com/poconnor/graft/internal/model"
)

// Adapter writes and removes MCP server entries in a tool-specific config file.
type Adapter interface {
	Render(mcp model.Definition) error
	Remove(name string) error
	TargetFile() string
}

// ClaudeAdapter implements Adapter for the Claude Code .mcp.json config file.
type ClaudeAdapter struct {
	root string
}

// CodexAdapter implements Adapter for the OpenAI Codex .codex/config.toml config file.
type CodexAdapter struct {
	root string
}

// NewClaudeAdapter returns a ClaudeAdapter that writes to <root>/.mcp.json.
func NewClaudeAdapter(root string) ClaudeAdapter {
	return ClaudeAdapter{root: root}
}

// NewCodexAdapter returns a CodexAdapter that writes to <root>/.codex/config.toml.
func NewCodexAdapter(root string) CodexAdapter {
	return CodexAdapter{root: root}
}

func (a ClaudeAdapter) TargetFile() string {
	return filepath.Join(a.root, ".mcp.json")
}

func (a ClaudeAdapter) Render(mcp model.Definition) error {
	doc, err := readClaude(a.TargetFile())
	if err != nil {
		return err
	}
	cfg := mcp.Adapter("claude")
	if existing, ok := doc.MCPServers[mcp.Name]; ok && !existing.Managed {
		return fmt.Errorf("refusing to overwrite unmanaged Claude MCP %q", mcp.Name)
	}
	doc.MCPServers[mcp.Name] = claudeServer{
		Type:    remoteType(cfg.Type),
		Command: stdioCommand(cfg),
		Args:    stdioArgs(cfg),
		Env:     stdioEnv(cfg),
		URL:     remoteURL(cfg),
		Headers: remoteHeaders(cfg),
		Managed: true,
	}
	return writeJSON(a.TargetFile(), doc)
}

func (a ClaudeAdapter) Remove(name string) error {
	doc, err := readClaude(a.TargetFile())
	if err != nil {
		return err
	}
	if existing, ok := doc.MCPServers[name]; ok && existing.Managed {
		delete(doc.MCPServers, name)
	}
	return writeJSON(a.TargetFile(), doc)
}

type claudeDoc struct {
	MCPServers map[string]claudeServer `json:"mcpServers"`
}

type claudeServer struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Managed bool              `json:"_graft_managed,omitempty"`
}

func readClaude(path string) (claudeDoc, error) {
	doc := claudeDoc{MCPServers: map[string]claudeServer{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return doc, nil
	}
	if err != nil {
		return doc, fmt.Errorf("read claude config %q: %w", path, err)
	}
	if len(data) == 0 {
		return doc, nil
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return doc, fmt.Errorf("parse claude config %q: %w", path, err)
	}
	if doc.MCPServers == nil {
		doc.MCPServers = map[string]claudeServer{}
	}
	return doc, nil
}

func (a CodexAdapter) TargetFile() string {
	return filepath.Join(a.root, ".codex", "config.toml")
}

func (a CodexAdapter) Render(mcp model.Definition) error {
	doc, err := readCodex(a.TargetFile())
	if err != nil {
		return err
	}
	cfg := mcp.Adapter("codex")
	if existing, ok := doc.MCPServers[mcp.Name]; ok && !existing.Managed {
		return fmt.Errorf("refusing to overwrite unmanaged Codex MCP %q", mcp.Name)
	}
	doc.MCPServers[mcp.Name] = codexServer{
		Type:    remoteType(cfg.Type),
		Command: stdioCommand(cfg),
		Args:    stdioArgs(cfg),
		Env:     stdioEnv(cfg),
		URL:     remoteURL(cfg),
		Managed: true,
	}
	return writeCodex(a.TargetFile(), doc)
}

func (a CodexAdapter) Remove(name string) error {
	doc, err := readCodex(a.TargetFile())
	if err != nil {
		return err
	}
	if existing, ok := doc.MCPServers[name]; ok && existing.Managed {
		delete(doc.MCPServers, name)
	}
	return writeCodex(a.TargetFile(), doc)
}

type codexDoc struct {
	MCPServers map[string]codexServer `toml:"mcp_servers"`
	raw        map[string]any
}

type codexServer struct {
	Command string            `toml:"command,omitempty"`
	Args    []string          `toml:"args,omitempty"`
	Env     map[string]string `toml:"env,omitempty"`
	Type    string            `toml:"type,omitempty"`
	URL     string            `toml:"url,omitempty"`
	Managed bool              `toml:"_graft_managed,omitempty"`
}

func stdioCommand(cfg model.AdapterConfig) string {
	if remoteType(cfg.Type) != "" {
		return ""
	}
	return cfg.Command
}

func stdioArgs(cfg model.AdapterConfig) []string {
	if remoteType(cfg.Type) != "" {
		return nil
	}
	return cfg.Args
}

func stdioEnv(cfg model.AdapterConfig) map[string]string {
	if remoteType(cfg.Type) != "" {
		return nil
	}
	return cfg.Env
}

func remoteType(value string) string {
	if value == "" || value == "stdio" {
		return ""
	}
	return value
}

func remoteURL(cfg model.AdapterConfig) string {
	if remoteType(cfg.Type) == "" {
		return ""
	}
	return cfg.URL
}

func remoteHeaders(cfg model.AdapterConfig) map[string]string {
	if remoteType(cfg.Type) == "" {
		return nil
	}
	return cfg.Headers
}

func readCodex(path string) (codexDoc, error) {
	doc := codexDoc{MCPServers: map[string]codexServer{}, raw: map[string]any{}}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return doc, nil
	}
	if _, err := toml.DecodeFile(path, &doc); err != nil {
		return doc, fmt.Errorf("parse codex config %q: %w", path, err)
	}
	if _, err := toml.DecodeFile(path, &doc.raw); err != nil {
		return doc, fmt.Errorf("parse codex config %q: %w", path, err)
	}
	if doc.MCPServers == nil {
		doc.MCPServers = map[string]codexServer{}
	}
	return doc, nil
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json target: %w", err)
	}
	return fileutil.AtomicWriteFile(path, append(data, '\n'), 0o600)
}

func writeToml(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(value); err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(path, buf.Bytes(), 0o600)
}

func writeCodex(path string, doc codexDoc) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}
	if doc.raw == nil {
		doc.raw = map[string]any{}
	}
	doc.raw["mcp_servers"] = doc.MCPServers
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(doc.raw); err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(path, buf.Bytes(), 0o600)
}

// Targets constructs the Adapter list for the given target names ("claude", "codex").
// Unknown names are silently ignored.
func Targets(root string, names []string) []Adapter {
	adapters := []Adapter{}
	seen := map[string]bool{}
	for _, name := range names {
		seen[name] = true
	}
	if seen["claude"] {
		adapters = append(adapters, NewClaudeAdapter(root))
	}
	if seen["codex"] {
		adapters = append(adapters, NewCodexAdapter(root))
	}
	return adapters
}

// SortedTargetNames returns a sorted copy of names for deterministic output.
func SortedTargetNames(names []string) []string {
	cp := append([]string{}, names...)
	sort.Strings(cp)
	return cp
}
