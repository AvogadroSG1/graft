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

func NewClaudeAdapter(root string) ClaudeAdapter {
	return ClaudeAdapter{root: root}
}

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
		Command: cfg.Command,
		Args:    cfg.Args,
		Env:     cfg.Env,
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
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
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
		Command: cfg.Command,
		Args:    cfg.Args,
		Env:     cfg.Env,
		Managed: true,
	}
	return writeToml(a.TargetFile(), doc)
}

func (a CodexAdapter) Remove(name string) error {
	doc, err := readCodex(a.TargetFile())
	if err != nil {
		return err
	}
	if existing, ok := doc.MCPServers[name]; ok && existing.Managed {
		delete(doc.MCPServers, name)
	}
	return writeToml(a.TargetFile(), doc)
}

type codexDoc struct {
	MCPServers map[string]codexServer `toml:"mcp_servers"`
}

type codexServer struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args,omitempty"`
	Env     map[string]string `toml:"env,omitempty"`
	Managed bool              `toml:"_graft_managed,omitempty"`
}

func readCodex(path string) (codexDoc, error) {
	doc := codexDoc{MCPServers: map[string]codexServer{}}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return doc, nil
	}
	if _, err := toml.DecodeFile(path, &doc); err != nil {
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

func SortedTargetNames(names []string) []string {
	cp := append([]string{}, names...)
	sort.Strings(cp)
	return cp
}
