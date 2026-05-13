// Package model defines the shared domain types for MCP server definitions and library indexes.
package model

// Pin records the exact version and hash of an MCP server's runtime artifact,
// enabling reproducible installs. The Hash format depends on the runtime:
// sha512- prefix for npm, sha256: prefix for uvx, sha256:... digest for Docker.
type Pin struct {
	Runtime string `json:"runtime"`
	Version string `json:"version"`
	Hash    string `json:"hash"`
}

// AdapterConfig holds the command invocation for a specific AI tool adapter.
// When present in a Definition's Adapters map, it overrides the top-level Command/Args/Env.
type AdapterConfig struct {
	Command string            `json:"command,omitempty" toml:"command,omitempty"`
	Args    []string          `json:"args,omitempty" toml:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty" toml:"env,omitempty"`
}

// Definition describes a single MCP server stored in a library. It is serialized
// as JSON under mcps/<name>.json in the library repository. The Command and Args fields
// provide the default invocation; per-adapter overrides live in Adapters.
type Definition struct {
	Name        string                   `json:"name" toml:"name"`
	Version     string                   `json:"version" toml:"version"`
	Description string                   `json:"description" toml:"description"`
	Tags        []string                 `json:"tags" toml:"tags"`
	Command     string                   `json:"command" toml:"command"`
	Args        []string                 `json:"args" toml:"args"`
	Env         map[string]string        `json:"env,omitempty" toml:"env,omitempty"`
	Pin         Pin                      `json:"pin,omitempty" toml:"pin,omitempty"`
	Adapters    map[string]AdapterConfig `json:"adapters,omitempty" toml:"adapters,omitempty"`
}

// Adapter returns the resolved AdapterConfig for the given adapter name (e.g. "claude" or "codex").
// The base command, args, and env are copied from the Definition, then any per-adapter
// overrides in d.Adapters[name] are applied on top. The returned config is safe to mutate.
func (d Definition) Adapter(name string) AdapterConfig {
	cfg := AdapterConfig{
		Command: d.Command,
		Args:    append([]string{}, d.Args...),
		Env:     map[string]string{},
	}
	for key, value := range d.Env {
		cfg.Env[key] = value
	}
	if d.Adapters == nil {
		return cfg
	}
	override, ok := d.Adapters[name]
	if !ok {
		return cfg
	}
	if override.Command != "" {
		cfg.Command = override.Command
	}
	if len(override.Args) > 0 {
		cfg.Args = append([]string{}, override.Args...)
	}
	for key, value := range override.Env {
		cfg.Env[key] = value
	}
	return cfg
}

type IndexEntry struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	SHA256      string   `json:"sha256"`
}

type LibraryIndex struct {
	Name string       `json:"name"`
	MCPs []IndexEntry `json:"mcps"`
}
