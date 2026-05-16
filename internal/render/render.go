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

func (a ClaudeAdapter) Snapshot() (any, error) {
	return snapshotFile(a.TargetFile())
}

func (a ClaudeAdapter) Restore(snapshot any) error {
	return restoreFile(a.TargetFile(), snapshot)
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
	return writeClaude(a.TargetFile(), doc)
}

func (a ClaudeAdapter) Remove(name string) error {
	doc, err := readClaude(a.TargetFile())
	if err != nil {
		return err
	}
	if existing, ok := doc.MCPServers[name]; ok && existing.Managed {
		delete(doc.MCPServers, name)
	}
	return writeClaude(a.TargetFile(), doc)
}

type claudeDoc struct {
	MCPServers map[string]claudeServer `json:"mcpServers"`
	raw        map[string]any
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
	doc := claudeDoc{MCPServers: map[string]claudeServer{}, raw: map[string]any{}}
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
	if err := json.Unmarshal(data, &doc.raw); err != nil {
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

func (a CodexAdapter) Snapshot() (any, error) {
	return snapshotFile(a.TargetFile())
}

func (a CodexAdapter) Restore(snapshot any) error {
	return restoreFile(a.TargetFile(), snapshot)
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
		Headers: remoteHeaders(cfg),
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
	Headers map[string]string `toml:"headers,omitempty"`
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

func writeClaude(path string, doc claudeDoc) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}
	if doc.raw == nil {
		doc.raw = map[string]any{}
	}
	rawServers, _ := doc.raw["mcpServers"].(map[string]any)
	if rawServers == nil {
		rawServers = map[string]any{}
	}
	for name, rawServer := range rawServers {
		if _, ok := doc.MCPServers[name]; ok {
			continue
		}
		if rawManaged(rawServer) {
			delete(rawServers, name)
		}
	}
	for name, server := range doc.MCPServers {
		if server.Managed {
			rawServers[name] = claudeServerFields(server)
		}
	}
	doc.raw["mcpServers"] = rawServers
	data, err := json.MarshalIndent(doc.raw, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json target: %w", err)
	}
	return fileutil.AtomicWriteFile(path, append(data, '\n'), 0o600)
}

func rawManaged(value any) bool {
	server, ok := value.(map[string]any)
	if !ok {
		return false
	}
	managed, _ := server["_graft_managed"].(bool)
	return managed
}

func claudeServerFields(server claudeServer) map[string]any {
	fields := map[string]any{}
	if server.Type != "" {
		fields["type"] = server.Type
	}
	if server.Command != "" {
		fields["command"] = server.Command
	}
	if len(server.Args) > 0 {
		fields["args"] = server.Args
	}
	if len(server.Env) > 0 {
		fields["env"] = server.Env
	}
	if server.URL != "" {
		fields["url"] = server.URL
	}
	if len(server.Headers) > 0 {
		fields["headers"] = server.Headers
	}
	if server.Managed {
		fields["_graft_managed"] = true
	}
	return fields
}

func writeCodex(path string, doc codexDoc) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}
	if doc.raw == nil {
		doc.raw = map[string]any{}
	}
	rawServers, _ := doc.raw["mcp_servers"].(map[string]any)
	if rawServers == nil {
		rawServers = map[string]any{}
	}
	for name, rawServer := range rawServers {
		if _, ok := doc.MCPServers[name]; ok {
			continue
		}
		if rawCodexServerManaged(rawServer) {
			delete(rawServers, name)
		}
	}
	for name, server := range doc.MCPServers {
		if server.Managed {
			rawServers[name] = codexServerFields(server)
		}
	}
	doc.raw["mcp_servers"] = rawServers
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(doc.raw); err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(path, buf.Bytes(), 0o600)
}

func rawCodexServerManaged(value any) bool {
	server, ok := value.(map[string]any)
	if !ok {
		return false
	}
	managed, _ := server["_graft_managed"].(bool)
	return managed
}

func codexServerFields(server codexServer) map[string]any {
	fields := map[string]any{}
	if server.Command != "" {
		fields["command"] = server.Command
	}
	if len(server.Args) > 0 {
		fields["args"] = server.Args
	}
	if len(server.Env) > 0 {
		fields["env"] = server.Env
	}
	if server.Type != "" {
		fields["type"] = server.Type
	}
	if server.URL != "" {
		fields["url"] = server.URL
	}
	if len(server.Headers) > 0 {
		fields["headers"] = server.Headers
	}
	if server.Managed {
		fields["_graft_managed"] = true
	}
	return fields
}

type fileSnapshot struct {
	exists bool
	data   []byte
}

func snapshotFile(path string) (fileSnapshot, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return fileSnapshot{}, nil
	}
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("snapshot target %q: %w", path, err)
	}
	return fileSnapshot{exists: true, data: append([]byte{}, data...)}, nil
}

func restoreFile(path string, snapshot any) error {
	snap, ok := snapshot.(fileSnapshot)
	if !ok {
		return fmt.Errorf("unexpected snapshot type %T", snapshot)
	}
	if !snap.exists {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove restored target %q: %w", path, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create restored target dir: %w", err)
	}
	return fileutil.AtomicWriteFile(path, snap.data, 0o600)
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
