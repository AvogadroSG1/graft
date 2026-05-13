// Package sync orchestrates applying library definition updates to a project's rendered configs.
package sync

import (
	"fmt"
	"strings"

	"github.com/poconnor/graft/internal/config"
	"github.com/poconnor/graft/internal/library"
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/model"
	"github.com/poconnor/graft/internal/pin"
	"github.com/poconnor/graft/internal/render"
)

// Result summarises the outcome of a sync operation.
type Result struct {
	Succeeded []string
	Failed    []string
	Skipped   []string
}

// Apply iterates over the MCPs in the lock file, fetches each definition from the library,
// enforces pins, and renders the result via the appropriate adapter. Each MCP ends up in
// exactly one of Succeeded, Failed, or Skipped (already up-to-date or duplicate).
func Apply(lk lock.Lock, cfg config.Config, client library.Client, adapters []render.AdapterByName) Result {
	result := Result{
		Succeeded: []string{},
		Failed:    []string{},
		Skipped:   []string{},
	}
	seen := map[string]bool{}
	for _, mcp := range lk.MCPs {
		key := mcp.Library + "/" + mcp.Name
		if seen[key] {
			result.Skipped = append(result.Skipped, mcp.Name)
			continue
		}
		seen[key] = true
		lib, ok := cfg.Library(mcp.Library)
		if !ok {
			result.Failed = append(result.Failed, mcp.Name)
			continue
		}
		def, hash, err := client.Definition(lib, mcp.Name)
		if err != nil || hash == mcp.DefinitionHash {
			result.Skipped = append(result.Skipped, mcp.Name)
			continue
		}
		if def.Pin.Runtime != "" {
			registry := pin.NewRegistry()
			handler, ok := registry.Handler(def.Command)
			if !ok {
				result.Failed = append(result.Failed, mcp.Name)
				continue
			}
			if err := pin.Enforce(handler, def.Pin, mcp.Version, false, ""); err != nil {
				result.Failed = append(result.Failed, mcp.Name)
				continue
			}
			def.Args = handler.Inject(def.Pin, def.Args)
		}
		if !renderTarget(mcp.Target, def, adapters) {
			result.Failed = append(result.Failed, mcp.Name)
			continue
		}
		result.Succeeded = append(result.Succeeded, mcp.Name)
	}
	return result
}

// AuthWarning returns a human-readable warning if any env key or value contains
// credential-bearing patterns (TOKEN, SECRET, PASSWORD, CREDENTIAL, or a Bearer prefix).
// Returns an empty string when the definition appears credential-free.
func AuthWarning(command string, env map[string]string) string {
	for key, value := range env {
		upperKey := strings.ToUpper(key)
		upperValue := strings.ToUpper(value)
		if strings.Contains(upperKey, "TOKEN") ||
			strings.Contains(upperKey, "SECRET") ||
			strings.Contains(upperKey, "PASSWORD") ||
			strings.Contains(upperKey, "CREDENTIAL") ||
			strings.Contains(upperValue, "BEARER ") {
			return fmt.Sprintf("auth warning: %s uses credential-bearing environment; default answer is no", command)
		}
	}
	return ""
}

func renderTarget(target string, def model.Definition, adapters []render.AdapterByName) bool {
	for _, adapter := range adapters {
		if adapter.Name != target {
			continue
		}
		return adapter.Adapter.Render(def) == nil
	}
	return false
}
