// Package status computes the drift state of a project by comparing the lock file
// against the active library indexes and rendered config files.
package status

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/poconnor/graft/internal/config"
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/model"
)

type State string

const (
	StateUninitialized  State = "uninitialized"
	StateInitialized    State = "initialized"
	StateConfigured     State = "configured"
	StateDrifted        State = "drifted"
	StatePinMismatch    State = "pinmismatch"
	StatePendingInput   State = "pending_input"
	StateUnknownLibrary State = "unknown_library"
)

type Result struct {
	State   State    `json:"state"`
	Details []string `json:"details"`
}

func Resolve(root string, cfg config.Config, lk lock.Lock, index map[string]model.LibraryIndex) Result {
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

func (r Result) JSON() ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}
