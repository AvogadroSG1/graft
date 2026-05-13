// Package claudecfg converts Claude MCP configuration files into graft definitions.
package claudecfg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/poconnor/graft/internal/model"
)

// Scope identifies where an MCP server was found in Claude configuration.
type Scope string

const (
	// ScopeGlobal describes root-level mcpServers entries.
	ScopeGlobal Scope = "global"
	// ScopeLocal describes project-scoped entries in ~/.claude.json.
	ScopeLocal Scope = "local"
	// ScopeProject describes entries in the current project's .mcp.json.
	ScopeProject Scope = "project"
)

// MCP is a parsed Claude MCP server with its canonical graft definition.
type MCP struct {
	Name       string
	Definition model.Definition
}

// Group is a prompt/import group from a single Claude configuration scope.
type Group struct {
	Scope Scope
	Name  string
	MCPs  []MCP
}

type rawDocument struct {
	MCPServers map[string]rawServer            `json:"mcpServers"`
	Projects   map[string]rawProjectDefinition `json:"projects"`
}

type rawProjectDefinition struct {
	MCPServers map[string]rawServer `json:"mcpServers"`
}

type rawServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

const importedVersion = "0.1.0"

// DefaultPath returns the source Claude config path for migrate-from-claude.
func DefaultPath() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "claude.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".claude.json"), nil
}

// Load reads Claude global/local config and the project .mcp.json, returning groups in deterministic order.
func Load(sourcePath, projectRoot string) ([]Group, error) {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("read Claude config: %w", err)
	}
	var doc rawDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse Claude config: %w", err)
	}
	groups := []Group{}
	if len(doc.MCPServers) > 0 {
		mcps := mcpsFromServers(doc.MCPServers, "Imported from Claude global config")
		if len(mcps) > 0 {
			groups = append(groups, Group{Scope: ScopeGlobal, Name: "global", MCPs: mcps})
		}
	}
	paths := make([]string, 0, len(doc.Projects))
	for path := range doc.Projects {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		mcps := mcpsFromServers(doc.Projects[path].MCPServers, "Imported from Claude project config")
		if len(mcps) > 0 {
			groups = append(groups, Group{Scope: ScopeLocal, Name: path, MCPs: mcps})
		}
	}
	projectGroup, err := loadProjectGroup(projectRoot)
	if err != nil {
		return nil, err
	}
	if len(projectGroup.MCPs) > 0 {
		groups = append(groups, projectGroup)
	}
	return groups, nil
}

func loadProjectGroup(projectRoot string) (Group, error) {
	path := filepath.Join(projectRoot, ".mcp.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Group{Scope: ScopeProject, Name: ".mcp.json", MCPs: []MCP{}}, nil
	}
	if err != nil {
		return Group{}, fmt.Errorf("read project MCP config: %w", err)
	}
	var doc rawDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return Group{}, fmt.Errorf("parse project MCP config: %w", err)
	}
	return Group{Scope: ScopeProject, Name: ".mcp.json", MCPs: mcpsFromServers(doc.MCPServers, "Imported from project .mcp.json")}, nil
}

func mcpsFromServers(servers map[string]rawServer, description string) []MCP {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	mcps := []MCP{}
	for _, name := range names {
		server := servers[name]
		if server.Type != "" && server.Type != "stdio" {
			def := model.Definition{
				Name:        name,
				Version:     importedVersion,
				Description: description,
				Type:        server.Type,
				URL:         server.URL,
				Headers:     placeholderMap(server.Headers),
				Adapters: map[string]model.AdapterConfig{
					"claude": {
						Type:    server.Type,
						URL:     server.URL,
						Headers: placeholderMap(server.Headers),
					},
				},
			}
			mcps = append(mcps, MCP{Name: name, Definition: def})
			continue
		}
		if server.Command == "" {
			continue
		}
		def := model.Definition{
			Name:        name,
			Version:     importedVersion,
			Description: description,
			Command:     server.Command,
			Args:        append([]string{}, server.Args...),
			Env:         placeholderMap(server.Env),
			Adapters: map[string]model.AdapterConfig{
				"claude": {
					Command: server.Command,
					Args:    append([]string{}, server.Args...),
					Env:     placeholderMap(server.Env),
				},
			},
		}
		mcps = append(mcps, MCP{Name: name, Definition: def})
	}
	return mcps
}

func placeholderMap(source map[string]string) map[string]string {
	next := map[string]string{}
	for key := range source {
		next[key] = "${" + key + "}"
	}
	return next
}
