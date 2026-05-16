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
	Warnings  []string
	Errors    []string
	Lock      lock.Lock `json:"-"`
}

// Options controls optional sync filtering behavior.
type Options struct {
	// Names limits sync processing to installed MCPs with matching names.
	Names []string
	// ForcePins allows a valid pin mismatch when a confirmation phrase is supplied.
	ForcePins bool
	// PinConfirmation is the pre-supplied phrase used when ConfirmPinMismatch is nil.
	PinConfirmation string
	// ConfirmPinMismatch prompts for confirmation after a concrete pin mismatch is known.
	ConfirmPinMismatch func(diff string) (string, error)
}

// Apply iterates over the MCPs in the lock file, fetches each definition from the library,
// enforces pins, and renders the result via the appropriate adapter. Each MCP ends up in
// exactly one of Succeeded, Failed, or Skipped (already up-to-date or duplicate).
func Apply(lk lock.Lock, cfg config.Config, client library.Client, adapters []render.AdapterByName) Result {
	return ApplyWithOptions(lk, cfg, client, adapters, Options{})
}

// ApplyWithOptions applies library updates like Apply, with optional filtering.
func ApplyWithOptions(lk lock.Lock, cfg config.Config, client library.Client, adapters []render.AdapterByName, opts Options) Result {
	result := Result{
		Succeeded: []string{},
		Failed:    []string{},
		Skipped:   []string{},
		Warnings:  []string{},
		Errors:    []string{},
		Lock:      lk,
	}
	names := map[string]bool{}
	for _, name := range opts.Names {
		names[name] = true
	}
	seen := map[string]bool{}
	for idx, mcp := range lk.MCPs {
		if len(names) > 0 && !names[mcp.Name] {
			result.Skipped = append(result.Skipped, mcp.Name)
			continue
		}
		key := mcp.Library + "/" + mcp.Name
		if seen[key] {
			result.Skipped = append(result.Skipped, mcp.Name)
			continue
		}
		seen[key] = true
		lib, ok := cfg.Library(mcp.Library)
		if !ok {
			result.Failed = append(result.Failed, mcp.Name)
			result.Errors = append(result.Errors, fmt.Sprintf("%s: library %q is not registered", mcp.Name, mcp.Library))
			continue
		}
		def, hash, err := client.Definition(lib, mcp.Name)
		if err != nil {
			result.Failed = append(result.Failed, mcp.Name)
			result.Errors = append(result.Errors, fmt.Sprintf("%s: definition: %v", mcp.Name, err))
			continue
		}
		if hash == mcp.DefinitionHash {
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
			result.Errors = append(result.Errors, fmt.Sprintf("%s: migration: %v", mcp.Name, err))
			continue
		}
		def = migrated
		if pending {
			result.Lock.MCPs[idx].PendingInput = true
			result.Skipped = append(result.Skipped, mcp.Name)
			continue
		}
		if warning := AuthWarningForTarget(def, mcp.Target); warning != "" {
			result.Warnings = append(result.Warnings, warning)
			result.Skipped = append(result.Skipped, mcp.Name)
			continue
		}
		if def.Pin.Runtime != "" {
			pinned, warnings, err := enforcePinForTargets(def, mcp.Target, opts)
			if err != nil {
				result.Failed = append(result.Failed, mcp.Name)
				result.Errors = append(result.Errors, fmt.Sprintf("%s: pin: %v", mcp.Name, err))
				continue
			}
			result.Warnings = append(result.Warnings, warnings...)
			def = pinned
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

// AuthWarning returns a human-readable warning if any environment or header key/value
// contains credential-bearing patterns. Returns an empty string when the definition
// appears credential-free.
func AuthWarning(def model.Definition) string {
	if credentialMapHasSensitiveFields(def.Env) || credentialMapHasSensitiveFields(def.Headers) {
		return fmt.Sprintf("auth warning: %s uses credential-bearing fields; default answer is no", def.Command)
	}
	return ""
}

// AuthWarningForTarget returns a warning for target-specific credential-bearing fields.
func AuthWarningForTarget(def model.Definition, target string) string {
	if warning := AuthWarning(def); warning != "" {
		return warning
	}
	for _, name := range renderTargetList(target) {
		cfg := def.Adapter(name)
		if credentialMapHasSensitiveFields(cfg.Env) || credentialMapHasSensitiveFields(cfg.Headers) {
			return fmt.Sprintf("auth warning: %s uses credential-bearing fields for %s; default answer is no", cfg.Command, name)
		}
	}
	return ""
}

func credentialMapHasSensitiveFields(values map[string]string) bool {
	for key, value := range values {
		upperKey := strings.ToUpper(key)
		upperValue := strings.ToUpper(value)
		if strings.Contains(upperKey, "TOKEN") ||
			strings.Contains(upperKey, "SECRET") ||
			strings.Contains(upperKey, "KEY") ||
			strings.Contains(upperKey, "PASSWORD") ||
			strings.Contains(upperKey, "CREDENTIAL") ||
			upperKey == "AUTHORIZATION" ||
			upperKey == "BEARER_TOKEN_ENV_VAR" ||
			strings.Contains(upperValue, "BEARER ") {
			return true
		}
	}
	return false
}

func enforcePinForTargets(def model.Definition, target string, opts Options) (model.Definition, []string, error) {
	registry := pin.NewRegistry()
	handler, ok := registry.HandlerForRuntime(def.Pin.Runtime)
	if !ok {
		return def, []string{fmt.Sprintf("no pin handler for runtime %s; MCP will run unpinned", def.Pin.Runtime)}, nil
	}
	for idx, name := range renderTargetList(target) {
		cfg := def.Adapter(name)
		if !handler.Detect(cfg.Command) {
			return def, nil, fmt.Errorf("runtime %s does not match command %q", def.Pin.Runtime, cfg.Command)
		}
		installedVersion := pin.InstalledRuntimeVersion(def.Pin.Runtime, cfg.Args)
		if err := handler.Validate(def.Pin, installedVersion); err != nil {
			confirmation := opts.PinConfirmation
			if opts.ForcePins && !errors.Is(err, pin.ErrInvalidPin) && opts.ConfirmPinMismatch != nil {
				var confirmErr error
				confirmation, confirmErr = opts.ConfirmPinMismatch(pinMismatchDiff(installedVersion, def.Pin))
				if confirmErr != nil {
					return def, nil, confirmErr
				}
			}
			if err := pin.Enforce(handler, def.Pin, installedVersion, opts.ForcePins, confirmation); err != nil {
				return def, nil, err
			}
		}
		injected := handler.Inject(def.Pin, cfg.Args)
		if idx == 0 {
			def.Args = append([]string{}, injected...)
		}
		if def.Adapters == nil {
			def.Adapters = map[string]model.AdapterConfig{}
		}
		override := def.Adapters[name]
		override.Args = append([]string{}, injected...)
		def.Adapters[name] = override
	}
	return def, nil, nil
}

func pinMismatchDiff(installedVersion string, pin model.Pin) string {
	if installedVersion == "" {
		installedVersion = "<none>"
	}
	return fmt.Sprintf("installed runtime version: %s\npinned runtime version: %s\npinned hash: %s", installedVersion, pin.Version, pin.Hash)
}

func renderTarget(target string, def model.Definition, adapters []render.AdapterByName) error {
	requested := renderTargetList(target)
	adapterByName := map[string]render.AdapterByName{}
	for _, adapter := range adapters {
		adapterByName[adapter.Name] = adapter
	}
	targeted := []render.AdapterByName{}
	missing := []string{}
	for _, name := range requested {
		adapter, ok := adapterByName[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		targeted = append(targeted, adapter)
	}
	if len(missing) > 0 {
		return fmt.Errorf("no render adapter for target %q", strings.Join(missing, ","))
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

func renderTargetList(target string) []string {
	if target == "both" {
		return []string{"claude", "codex"}
	}
	names := []string{}
	seen := map[string]bool{}
	for _, part := range strings.Split(target, ",") {
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			names = append(names, part)
			seen[part] = true
		}
	}
	return names
}
