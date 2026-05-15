// Package sync orchestrates applying library definition updates to a project's rendered configs.
package sync

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/poconnor/graft/internal/config"
	"github.com/poconnor/graft/internal/library"
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/migrate"
	"github.com/poconnor/graft/internal/model"
	"github.com/poconnor/graft/internal/pin"
	"github.com/poconnor/graft/internal/render"
)

// Result summarises the outcome of a sync operation.
type Result struct {
	Succeeded []string
	Failed    []string
	Skipped   []string
	Errors    []string
	Lock      lock.Lock `json:"-"`
}

// Apply iterates over the MCPs in the lock file, fetches each definition from the library,
// enforces pins, and renders the result via the appropriate adapter. Each MCP ends up in
// exactly one of Succeeded, Failed, or Skipped (already up-to-date or duplicate).
func Apply(lk lock.Lock, cfg config.Config, client library.Client, adapters []render.AdapterByName) Result {
	result := Result{
		Succeeded: []string{},
		Failed:    []string{},
		Skipped:   []string{},
		Errors:    []string{},
		Lock:      lk,
	}
	seen := map[string]bool{}
	for idx, mcp := range lk.MCPs {
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
		migrated, pending, err := migrateDefinition(lib, mcp, def)
		if err != nil {
			if errors.Is(err, migrate.ErrPendingInput) {
				result.Lock.MCPs[idx].PendingInput = true
				result.Skipped = append(result.Skipped, mcp.Name)
				continue
			}
			result.Failed = append(result.Failed, mcp.Name)
			continue
		}
		def = migrated
		if pending {
			result.Lock.MCPs[idx].PendingInput = true
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
		if err := renderTarget(mcp.Target, def, adapters); err != nil {
			result.Failed = append(result.Failed, mcp.Name)
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", mcp.Name, err))
			continue
		}
		result.Succeeded = append(result.Succeeded, mcp.Name)
		result.Lock.MCPs[idx].Version = def.Version
		result.Lock.MCPs[idx].DefinitionHash = hash
		result.Lock.MCPs[idx].PendingInput = false
	}
	return result
}

func migrateDefinition(lib config.Library, mcp lock.InstalledMCP, def model.Definition) (model.Definition, bool, error) {
	if mcp.Version == "" || def.Version == "" || mcp.Version == def.Version {
		return def, false, nil
	}
	chain, err := migrate.Chain(lib.CachePath, mcp.Name, mcp.Version, def.Version)
	if err != nil {
		return model.Definition{}, false, err
	}
	data, err := json.Marshal(def)
	if err != nil {
		return model.Definition{}, false, fmt.Errorf("marshal definition for migration: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return model.Definition{}, false, fmt.Errorf("prepare definition for migration: %w", err)
	}
	pending := false
	for _, file := range chain {
		stepPending, err := migrate.Apply(doc, file.Steps, true, nil)
		if err != nil {
			return model.Definition{}, stepPending, err
		}
		pending = pending || stepPending
	}
	data, err = json.Marshal(doc)
	if err != nil {
		return model.Definition{}, false, fmt.Errorf("marshal migrated definition: %w", err)
	}
	var migrated model.Definition
	if err := json.Unmarshal(data, &migrated); err != nil {
		return model.Definition{}, false, fmt.Errorf("parse migrated definition: %w", err)
	}
	return migrated, pending, nil
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

func renderTarget(target string, def model.Definition, adapters []render.AdapterByName) error {
	targets := renderTargetNames(target)
	targeted := []render.AdapterByName{}
	for _, adapter := range adapters {
		if !targets[adapter.Name] {
			continue
		}
		targeted = append(targeted, adapter)
	}
	if len(targeted) == 0 {
		return fmt.Errorf("no render adapter for target %q", target)
	}
	snapshots := []adapterSnapshot{}
	if len(targeted) > 1 {
		for _, adapter := range targeted {
			restorable, ok := adapter.Adapter.(restorableAdapter)
			if !ok {
				return fmt.Errorf("adapter %q does not support rollback", adapter.Name)
			}
			snapshot, err := restorable.Snapshot()
			if err != nil {
				return fmt.Errorf("snapshot %s target: %w", adapter.Name, err)
			}
			snapshots = append(snapshots, adapterSnapshot{adapter: restorable, snapshot: snapshot})
		}
	}
	for _, adapter := range targeted {
		if err := adapter.Adapter.Render(def); err != nil {
			if rollbackErr := restoreSnapshots(snapshots); rollbackErr != nil {
				return fmt.Errorf("render %s target: %w; rollback: %w", adapter.Name, err, rollbackErr)
			}
			return fmt.Errorf("render %s target: %w", adapter.Name, err)
		}
	}
	return nil
}

type restorableAdapter interface {
	Snapshot() (any, error)
	Restore(any) error
}

type adapterSnapshot struct {
	adapter  restorableAdapter
	snapshot any
}

func restoreSnapshots(snapshots []adapterSnapshot) error {
	var errs []error
	for idx := len(snapshots) - 1; idx >= 0; idx-- {
		if err := snapshots[idx].adapter.Restore(snapshots[idx].snapshot); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func renderTargetNames(target string) map[string]bool {
	if target == "both" {
		return map[string]bool{"claude": true, "codex": true}
	}
	names := map[string]bool{}
	for _, part := range strings.Split(target, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			names[part] = true
		}
	}
	return names
}
