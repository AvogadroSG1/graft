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

// InputFunc provides a value for require_input steps.
type InputFunc func(step Step) (any, error)

// Chain builds the ordered list of migration files needed to advance name from
// the installed version to the current version. Returns an error if the path
// contains a cycle, is broken, or has duplicate From and To edges.
func Chain(root, name, from, to string) ([]File, error) {
	if err := validateMigrationName(name); err != nil {
		return nil, err
	}
	if from == to {
		return []File{}, nil
	}
	if err := rejectSymlink(filepath.Join(root, "migrations")); err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "migrations", name)
	if err := rejectSymlink(dir); err != nil {
		return nil, err
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	edges := map[string][]File{}
	seenEdges := map[string]bool{}
	for _, entry := range files {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("migration file %q must not be a symlink", entry.Name())
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		var file File
		if err := json.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("parse migration %q: %w", entry.Name(), err)
		}
		if err := validateMigrationFilename(entry.Name(), file); err != nil {
			return nil, err
		}
		key := file.From + "\x00" + file.To
		if seenEdges[key] {
			return nil, fmt.Errorf("duplicate migration edge from %s to %s", file.From, file.To)
		}
		seenEdges[key] = true
		edges[file.From] = append(edges[file.From], file)
	}
	for fromVersion := range edges {
		sort.Slice(edges[fromVersion], func(i, j int) bool {
			left := edges[fromVersion][i]
			right := edges[fromVersion][j]
			if left.To == to {
				return true
			}
			if right.To == to {
				return false
			}
			return left.To < right.To
		})
	}
	type path struct {
		current string
		files   []File
		seen    map[string]bool
	}
	queue := []path{{current: from, files: []File{}, seen: map[string]bool{from: true}}}
	cycleVersion := ""
	for len(queue) > 0 {
		nextPath := queue[0]
		queue = queue[1:]
		for _, edge := range edges[nextPath.current] {
			if nextPath.seen[edge.To] {
				if cycleVersion == "" {
					cycleVersion = edge.To
				}
				continue
			}
			files := append(append([]File{}, nextPath.files...), edge)
			if edge.To == to {
				return files, nil
			}
			seen := map[string]bool{}
			for version, ok := range nextPath.seen {
				seen[version] = ok
			}
			seen[edge.To] = true
			queue = append(queue, path{current: edge.To, files: files, seen: seen})
		}
	}
	if cycleVersion != "" {
		return nil, fmt.Errorf("migration cycle detected at %s", cycleVersion)
	}
	return nil, fmt.Errorf("broken migration chain from %s to %s", from, to)
}

// Apply executes steps against doc in place. input supplies values for
// require_input steps. The retained auto parameter is ignored by the executor:
// missing require_input values return ErrPendingInput and callers decide
// whether to prompt, skip an MCP, or mark it pending.
func Apply(doc map[string]any, steps []Step, _ bool, input map[string]string) (bool, error) {
	return ApplyWithInput(doc, steps, false, input, nil)
}

// ApplyWithInput executes steps against doc in place and calls prompt when a
// require_input step has no value in input. The retained auto parameter is
// ignored by the executor. When prompt is nil, missing inputs return
// ErrPendingInput.
func ApplyWithInput(doc map[string]any, steps []Step, _ bool, input map[string]string, prompt InputFunc) (bool, error) {
	pending := false
	for _, step := range steps {
		switch step.Type {
		case "rename":
			if err := renamePath(doc, step.From, step.To); err != nil {
				return pending, err
			}
		case "set_default":
			if err := setDefault(doc, step.Path, step.Value); err != nil {
				return pending, err
			}
		case "require_input":
			var value any
			value, ok := input[step.Path]
			if !ok {
				if prompt != nil {
					prompted, err := prompt(step)
					if err != nil {
						return pending, fmt.Errorf("input required for %s: %w", step.Path, err)
					}
					value = prompted
				} else {
					return true, fmt.Errorf("%w: %s", ErrPendingInput, step.Path)
				}
			}
			if err := setPath(doc, step.Path, value); err != nil {
				return pending, err
			}
		default:
			return pending, fmt.Errorf("unknown migration step %q", step.Type)
		}
	}
	return pending, nil
}

func renamePath(doc map[string]any, from, to string) error {
	if from == to {
		return nil
	}
	if pathsOverlap(from, to) {
		return fmt.Errorf("rename path %q overlaps destination %q", from, to)
	}
	value, ok := getPath(doc, from)
	if !ok {
		return nil
	}
	if _, exists := getPath(doc, to); exists {
		return fmt.Errorf("rename destination %q already exists", to)
	}
	if err := validatePathParents(doc, to); err != nil {
		return err
	}
	deletePath(doc, from)
	return setPath(doc, to, value)
}

func setDefault(doc map[string]any, path string, value any) error {
	if _, ok := getPath(doc, path); ok {
		return nil
	}
	return setPath(doc, path, value)
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

func setPath(doc map[string]any, path string, value any) error {
	current := doc
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		next, ok := current[part].(map[string]any)
		if !ok {
			if _, exists := current[part]; exists {
				return fmt.Errorf("path %q has non-object parent %q", path, part)
			}
			next = map[string]any{}
			current[part] = next
		}
		current = next
	}
	current[parts[len(parts)-1]] = value
	return nil
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

func validatePathParents(doc map[string]any, path string) error {
	current := doc
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		value, ok := current[part]
		if !ok {
			return nil
		}
		next, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("path %q has non-object parent %q", path, part)
		}
		current = next
	}
	return nil
}

func pathsOverlap(left, right string) bool {
	return strings.HasPrefix(left, right+".") || strings.HasPrefix(right, left+".")
}

func validateMigrationName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid migration name %q", name)
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return fmt.Errorf("invalid migration name %q", name)
	}
	return nil
}

func validateMigrationFilename(name string, file File) error {
	base := strings.TrimSuffix(name, ".json")
	from, to, ok := strings.Cut(base, "-to-")
	if !ok || from == "" || to == "" {
		return fmt.Errorf("migration filename %q must match <from>-to-<to>.json", name)
	}
	if from != file.From || to != file.To {
		return fmt.Errorf("migration filename %q does not match JSON edge %s-to-%s", name, file.From, file.To)
	}
	return nil
}

func rejectSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect migration path %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("migration path %q must not be a symlink", path)
	}
	return nil
}

// SortFiles sorts files in ascending From-version order for deterministic display.
func SortFiles(files []File) {
	sort.Slice(files, func(i, j int) bool {
		return files[i].From < files[j].From
	})
}

// ErrPendingInput is returned when a migration requires user-provided values that have not been supplied.
var ErrPendingInput = errors.New("pending input")
