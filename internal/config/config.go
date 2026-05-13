//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock/store.go -package=mock github.com/poconnor/graft/internal/config Store

// Package config manages the global graft configuration file that tracks registered MCP libraries.
// The config file lives at ~/.config/graft/config.json (XDG-aware).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/poconnor/graft/internal/fileutil"
)

// Library is a registered MCP library entry. URL points to the git remote;
// CachePath is the local clone location (defaults to ~/.cache/graft/libraries/<name>).
// The first registered library, or the one with Default: true, is used when no --library flag is given.
type Library struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	CachePath string `json:"cache_path"`
	Default   bool   `json:"default"`
}

// Config is the root of the graft global configuration.
type Config struct {
	Libraries []Library `json:"libraries"`
}

// Store abstracts reading and writing the global config file.
type Store interface {
	Load(path string) (Config, error)
	Save(path string, cfg Config) error
}

// FileStore implements Store using the filesystem.
type FileStore struct{}

// DefaultPath returns the XDG-aware default path for the graft config file.
// It respects $XDG_CONFIG_HOME and falls back to ~/.config/graft/config.json.
func DefaultPath() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "graft", "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	return filepath.Join(home, ".config", "graft", "config.json"), nil
}

// Load reads the config from path. If path is empty, DefaultPath is used.
// Missing config files return an empty Config rather than an error.
func (FileStore) Load(path string) (Config, error) {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return Config{Libraries: []Library{}}, err
		}
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{Libraries: []Library{}}, nil
	}
	if err != nil {
		return Config{Libraries: []Library{}}, fmt.Errorf("read config %q: %w", path, err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{Libraries: []Library{}}, fmt.Errorf("parse config %q: %w", path, err)
	}
	if cfg.Libraries == nil {
		cfg.Libraries = []Library{}
	}
	return cfg, nil
}

// Save writes cfg to path atomically. If path is empty, DefaultPath is used.
// Parent directories are created with 0700 permissions.
func (FileStore) Save(path string, cfg Config) error {
	if path == "" {
		var err error
		path, err = DefaultPath()
		if err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return fileutil.AtomicWriteFile(path, append(data, '\n'), 0o600)
}

// DefaultLibrary returns the library marked as default, or the first library if none is marked.
// Returns false only when the config has no libraries at all.
func (c Config) DefaultLibrary() (Library, bool) {
	for _, lib := range c.Libraries {
		if lib.Default {
			return lib, true
		}
	}
	if len(c.Libraries) == 0 {
		return Library{}, false
	}
	return c.Libraries[0], true
}

func (c Config) Library(name string) (Library, bool) {
	for _, lib := range c.Libraries {
		if lib.Name == name {
			return lib, true
		}
	}
	return Library{}, false
}

func (c Config) WithLibrary(lib Library) (Config, error) {
	next := Config{Libraries: slices.Clone(c.Libraries)}
	if lib.CachePath == "" {
		cachePath, err := defaultCachePath(lib.Name)
		if err != nil {
			return next, err
		}
		lib.CachePath = cachePath
	}
	if len(next.Libraries) == 0 {
		lib.Default = true
	}
	for idx, existing := range next.Libraries {
		if existing.Name == lib.Name {
			next.Libraries[idx] = lib
			return next, nil
		}
	}
	next.Libraries = append(next.Libraries, lib)
	return next, nil
}

func defaultCachePath(name string) (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home for cache path: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "graft", "libraries", name), nil
}
