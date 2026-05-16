// Package status computes the drift state of a project by comparing the lock file
// against the active library indexes and rendered config files.
package status

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/poconnor/graft/internal/config"
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/model"
	"github.com/poconnor/graft/internal/pin"
)

// State describes the drift relationship between a project's lock file and its rendered configs.
type State string

const (
	// StateUninitialized means graft.lock does not exist in the project root.
	StateUninitialized State = "uninitialized"
	// StateInitialized means graft.lock exists but no MCPs have been selected yet.
	StateInitialized State = "initialized"
	// StateConfigured means all selected MCPs match the current library definitions.
	StateConfigured State = "configured"
	// StateDrifted means a selected MCP definition or rendered config differs from the lock.
	StateDrifted State = "drifted"
	// StatePinMismatch means a pinned version does not match what is installed.
	StatePinMismatch State = "pinmismatch"
	// StatePendingInput means at least one MCP requires user-provided environment values.
	StatePendingInput State = "pending_input"
	// StateUnknownLibrary means the lock file references a library not registered in the config.
	StateUnknownLibrary State = "unknown_library"
)

// Result is the output of a status check.
type Result struct {
	State   State    `json:"state"`
	Details []string `json:"details"`
}

// Resolve computes the current drift state of the project at root. index maps library
// names to their current LibraryIndex; pass an empty map to skip hash comparisons.
func Resolve(root string, cfg config.Config, lk lock.Lock, index map[string]model.LibraryIndex) Result {
	return ResolveWithDefinitions(root, cfg, lk, index, nil)
}

// ResolveWithDefinitions computes status with optional current library definitions for pin checks.
func ResolveWithDefinitions(root string, cfg config.Config, lk lock.Lock, index map[string]model.LibraryIndex, definitions map[string]map[string]model.Definition) Result {
	if _, err := os.Stat(filepath.Join(root, lock.Filename)); os.IsNotExist(err) {
		return Result{State: StateUninitialized, Details: []string{"graft.lock not found"}}
	}
	if len(lk.MCPs) == 0 {
		return Result{State: StateInitialized, Details: []string{"no MCPs selected"}}
	}
	for _, lib := range lk.Libraries {
		if _, ok := cfg.Library(lib.Name); !ok {
			return Result{State: StateUnknownLibrary, Details: []string{fmt.Sprintf("%s must be registered", lib.Name)}}
		}
	}
	if detail := externalEditDetail(root, lk); detail != "" {
		return Result{State: StateDrifted, Details: []string{detail}}
	}
	if detail := pinMismatchDetail(root, lk, definitions); detail != "" {
		return Result{State: StatePinMismatch, Details: []string{detail}}
	}
	for _, mcp := range lk.MCPs {
		if mcp.PendingInput {
			return Result{State: StatePendingInput, Details: []string{mcp.Name}}
		}
		libraryIndex, ok := index[mcp.Library]
		if !ok {
			continue
		}
		for _, entry := range libraryIndex.MCPs {
			if entry.Name == mcp.Name && entry.SHA256 != mcp.DefinitionHash {
				return Result{State: StateDrifted, Details: []string{mcp.Name}}
			}
		}
	}
	return Result{State: StateConfigured, Details: []string{"all selected MCPs are current"}}
}

func pinMismatchDetail(root string, lk lock.Lock, definitions map[string]map[string]model.Definition) string {
	if definitions == nil {
		return ""
	}
	registry := pin.NewRegistry()
	for _, mcp := range lk.MCPs {
		def, ok := definitions[mcp.Library][mcp.Name]
		if !ok || def.Pin.Runtime == "" {
			continue
		}
		handler, ok := registry.HandlerForRuntime(def.Pin.Runtime)
		if !ok {
			continue
		}
		for _, target := range statusTargetNames(mcp.Target) {
			cfg, ok := renderedConfig(root, target, mcp.Name)
			if !ok {
				continue
			}
			if !handler.Detect(cfg.Command) {
				return fmt.Sprintf("pin mismatch: %s/%s runtime %s does not match command %q", target, mcp.Name, def.Pin.Runtime, cfg.Command)
			}
			if err := handler.Validate(def.Pin, pin.InstalledRuntimeVersion(def.Pin.Runtime, cfg.Args)); err != nil {
				return fmt.Sprintf("pin mismatch: %s/%s %v", target, mcp.Name, err)
			}
		}
	}
	return ""
}

func externalEditDetail(root string, lk lock.Lock) string {
	for _, mcp := range lk.MCPs {
		for _, target := range statusTargetNames(mcp.Target) {
			cfg, ok, detail := renderedConfigWithDetail(root, target, mcp.Name)
			if detail != "" {
				return detail
			}
			if ok && !cfg.Managed {
				return "external edit: " + target + "/" + mcp.Name
			}
			if ok && cfg.Managed && targetNewerThanLock(root, target) {
				return "external edit: " + target + "/" + mcp.Name
			}
		}
	}
	return ""
}

func targetNewerThanLock(root, target string) bool {
	lockInfo, err := os.Stat(filepath.Join(root, lock.Filename))
	if err != nil {
		return false
	}
	targetInfo, err := os.Stat(statusTargetFile(root, target))
	if err != nil {
		return false
	}
	return targetInfo.ModTime().After(lockInfo.ModTime())
}

func statusTargetFile(root, target string) string {
	switch target {
	case "claude":
		return filepath.Join(root, ".mcp.json")
	case "codex":
		return filepath.Join(root, ".codex", "config.toml")
	default:
		return ""
	}
}

type statusClaudeDoc struct {
	MCPServers map[string]statusClaudeServer `json:"mcpServers"`
}

type statusClaudeServer struct {
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Managed bool     `json:"_graft_managed,omitempty"`
}

type statusCodexDoc struct {
	MCPServers map[string]statusCodexServer `toml:"mcp_servers"`
}

type statusCodexServer struct {
	Command string   `toml:"command,omitempty"`
	Args    []string `toml:"args,omitempty"`
	Managed bool     `toml:"_graft_managed"`
}

type renderedStatusConfig struct {
	Command string
	Args    []string
	Managed bool
}

func renderedConfig(root, target, name string) (renderedStatusConfig, bool) {
	cfg, ok, _ := renderedConfigWithDetail(root, target, name)
	return cfg, ok
}

func renderedConfigWithDetail(root, target, name string) (renderedStatusConfig, bool, string) {
	switch target {
	case "claude":
		data, err := os.ReadFile(filepath.Join(root, ".mcp.json"))
		if os.IsNotExist(err) {
			return renderedStatusConfig{}, false, "external edit: claude config is missing"
		}
		if err != nil || len(data) == 0 {
			return renderedStatusConfig{}, false, "external edit: claude config is unreadable"
		}
		var doc statusClaudeDoc
		if err := json.Unmarshal(data, &doc); err != nil {
			return renderedStatusConfig{}, false, "external edit: claude config is unreadable"
		}
		server, ok := doc.MCPServers[name]
		if !ok {
			return renderedStatusConfig{}, false, "external edit: claude/" + name + " is missing"
		}
		return renderedStatusConfig(server), true, ""
	case "codex":
		data, err := os.ReadFile(filepath.Join(root, ".codex", "config.toml"))
		if os.IsNotExist(err) {
			return renderedStatusConfig{}, false, "external edit: codex config is missing"
		}
		if err != nil || len(data) == 0 {
			return renderedStatusConfig{}, false, "external edit: codex config is unreadable"
		}
		var doc statusCodexDoc
		if err := toml.Unmarshal(data, &doc); err != nil {
			return renderedStatusConfig{}, false, "external edit: codex config is unreadable"
		}
		server, ok := doc.MCPServers[name]
		if !ok {
			return renderedStatusConfig{}, false, "external edit: codex/" + name + " is missing"
		}
		return renderedStatusConfig(server), true, ""
	default:
		return renderedStatusConfig{}, false, ""
	}
}

func statusTargetNames(target string) []string {
	if target == "both" {
		return []string{"claude", "codex"}
	}
	targets := []string{}
	for _, part := range strings.Split(target, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			targets = append(targets, part)
		}
	}
	return targets
}

// JSON returns the result as indented JSON bytes.
func (r Result) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
