// Package migrate applies forward migrations to MCP definition documents. Migration files
// are JSON files under migrations/<name>/ that describe a graph of schema transitions
// (rename fields, set defaults, require user input). Chain builds the migration path
// and Apply executes the steps.
package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// File represents a single migration edge in the schema version graph.
type File struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Steps []Step `json:"steps"`
}

// Step is a single transformation within a migration file.
// Supported types: "rename" (rename a field), "set_default" (fill missing field),
// "require_input" (prompt the user for a value).
type Step struct {
	Type  string `json:"type"`
	From  string `json:"from,omitempty"`
	To    string `json:"to,omitempty"`
	Path  string `json:"path,omitempty"`
	Value any    `json:"value,omitempty"`
}

// Chain builds the ordered list of migration files needed to advance schema version name
// from from to to. Returns an error if the path contains a cycle, is broken, or has
// duplicate edges (two files with the same From version).
func Chain(root, name, from, to string) ([]File, error) {
	dir := filepath.Join(root, "migrations", name)
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	edges := map[string]File{}
	for _, entry := range files {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		var file File
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, err
		}
		if _, exists := edges[file.From]; exists {
			return nil, fmt.Errorf("duplicate migration edge from %s", file.From)
		}
		edges[file.From] = file
	}
	chain := []File{}
	current := from
	visited := map[string]bool{}
	for current != to {
		if visited[current] {
			return nil, fmt.Errorf("migration cycle detected at %s", current)
		}
		visited[current] = true
		next, ok := edges[current]
		if !ok {
			return nil, fmt.Errorf("broken migration chain from %s to %s", current, to)
		}
		chain = append(chain, next)
		current = next.To
	}
	return chain, nil
}

// Apply executes steps against doc in place. input supplies values for require_input steps.
// When auto is true, missing inputs are skipped (pending set to true) rather than returning
// an error. Returns (pending, error): pending is true when any require_input step lacked a value.
func Apply(doc map[string]any, steps []Step, auto bool, input map[string]string) (bool, error) {
	pending := false
	for _, step := range steps {
		switch step.Type {
		case "rename":
			rename(doc, step.From, step.To)
		case "set_default":
			setDefault(doc, step.Path, step.Value)
		case "require_input":
			value, ok := input[step.Path]
			if !ok {
				pending = true
				if auto {
					continue
				}
				return true, fmt.Errorf("input required for %s", step.Path)
			}
			setPath(doc, step.Path, value)
		default:
			return pending, fmt.Errorf("unknown migration step %q", step.Type)
		}
	}
	return pending, nil
}

func rename(doc map[string]any, from, to string) {
	value, ok := getPath(doc, from)
	if !ok {
		return
	}
	deletePath(doc, from)
	setPath(doc, to, value)
}

func setDefault(doc map[string]any, path string, value any) {
	if _, ok := getPath(doc, path); ok {
		return
	}
	setPath(doc, path, value)
}

func getPath(doc map[string]any, path string) (any, bool) {
	current := doc
	parts := strings.Split(path, ".")
	for idx, part := range parts {
		value, ok := current[part]
		if !ok {
			return nil, false
		}
		if idx == len(parts)-1 {
			return value, true
		}
		next, ok := value.(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return nil, false
}

func setPath(doc map[string]any, path string, value any) {
	current := doc
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
}

func deletePath(doc map[string]any, path string) {
	current := doc
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			return
		}
		current = next
	}
	delete(current, parts[len(parts)-1])
}

func SortFiles(files []File) {
	sort.Slice(files, func(i, j int) bool {
		return files[i].From < files[j].From
	})
}

var ErrPendingInput = errors.New("pending input")
