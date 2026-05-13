//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock/store.go -package=mock github.com/poconnor/graft/internal/lock Store

// Package lock manages the per-project graft.lock file that records which libraries
// and MCP definitions are pinned for a project.
package lock

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/poconnor/graft/internal/fileutil"
)

// Filename is the name of the lock file written to the project root.
const Filename = "graft.lock"

// LibraryRef records a library that this project has registered.
// Commit is the git SHA at the time of the last sync; empty means unpinned.
type LibraryRef struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Commit string `json:"commit,omitempty"`
}

// InstalledMCP records a single MCP definition that has been synced into a project.
// DefinitionHash is the SHA256 of the definition JSON at the time of installation;
// a mismatch with the library's current hash indicates drift.
// PendingInput is set when the definition requires user-provided environment values before it can be activated.
type InstalledMCP struct {
	Name           string `json:"name"`
	Library        string `json:"library"`
	Version        string `json:"version"`
	DefinitionHash string `json:"definition_hash"`
	Target         string `json:"target"`
	PendingInput   bool   `json:"pending_input,omitempty"`
}

// Lock is the root of the graft.lock file for a project.
type Lock struct {
	Libraries []LibraryRef   `json:"libraries"`
	MCPs      []InstalledMCP `json:"mcps"`
}

type Store interface {
	Load(root string) (Lock, error)
	Save(root string, lock Lock) error
}

type FileStore struct{}

func (FileStore) Load(root string) (Lock, error) {
	path := filepath.Join(root, Filename)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Lock{Libraries: []LibraryRef{}, MCPs: []InstalledMCP{}}, nil
	}
	if err != nil {
		return Lock{Libraries: []LibraryRef{}, MCPs: []InstalledMCP{}}, fmt.Errorf("read lock %q: %w", path, err)
	}
	var parsed Lock
	if err := json.Unmarshal(data, &parsed); err != nil {
		return Lock{Libraries: []LibraryRef{}, MCPs: []InstalledMCP{}}, fmt.Errorf("parse lock %q: %w", path, err)
	}
	if parsed.Libraries == nil {
		parsed.Libraries = []LibraryRef{}
	}
	if parsed.MCPs == nil {
		parsed.MCPs = []InstalledMCP{}
	}
	return parsed, nil
}

func (FileStore) Save(root string, lock Lock) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal lock: %w", err)
	}
	path := filepath.Join(root, Filename)
	return fileutil.AtomicWriteFile(path, append(data, '\n'), 0o600)
}

func HashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func HashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read hash source %q: %w", path, err)
	}
	return HashBytes(data), nil
}

func (l Lock) Library(name string) (LibraryRef, bool) {
	for _, lib := range l.Libraries {
		if lib.Name == name {
			return lib, true
		}
	}
	return LibraryRef{}, false
}

func (l Lock) MCP(name string) (InstalledMCP, bool) {
	for _, mcp := range l.MCPs {
		if mcp.Name == name {
			return mcp, true
		}
	}
	return InstalledMCP{}, false
}
