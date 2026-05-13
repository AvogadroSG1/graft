//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock/handler.go -package=mock github.com/poconnor/graft/internal/pin Handler

// Package pin enforces reproducible version pinning for MCP server runtimes.
// Supported runtimes: npm (sha512 integrity), Docker (sha256 digest), uvx (sha256 hash).
package pin

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/poconnor/graft/internal/model"
)

// Handler validates and injects pin information for a specific runtime.
type Handler interface {
	Detect(command string) bool
	Validate(pin model.Pin, installedVersion string) error
	Inject(pin model.Pin, args []string) []string
}

// Registry maps runtime commands to their Handler implementations, preserving registration order.
type Registry struct {
	order    []string
	handlers map[string]Handler
}

// NewRegistry returns a Registry pre-loaded with npm, docker, and uvx handlers.
func NewRegistry() Registry {
	reg := Registry{order: []string{}, handlers: map[string]Handler{}}
	reg.Register("npm", NPMHandler{})
	reg.Register("docker", DockerHandler{})
	reg.Register("uvx", UVHandler{})
	return reg
}

// Register adds or replaces a handler under name. Handlers are tested in registration order.
func (r *Registry) Register(name string, handler Handler) {
	if r.handlers == nil {
		r.handlers = map[string]Handler{}
	}
	if _, exists := r.handlers[name]; !exists {
		r.order = append(r.order, name)
	}
	r.handlers[name] = handler
}

// Handler returns the first registered handler whose Detect method returns true for command.
func (r Registry) Handler(command string) (Handler, bool) {
	for _, name := range r.order {
		handler := r.handlers[name]
		if handler.Detect(command) {
			return handler, true
		}
	}
	return nil, false
}

// Enforce validates that the installed runtime version satisfies pin. If there is a mismatch
// and force is true, the caller must also pass the exact phrase "I understand the risk" as
// confirmation — this two-factor check prevents accidental bypass of security pins.
func Enforce(handler Handler, pin model.Pin, installedVersion string, force bool, confirmation string) error {
	if handler == nil {
		return errors.New("no pin handler for runtime")
	}
	if err := handler.Validate(pin, installedVersion); err == nil {
		return nil
	}
	if !force {
		return fmt.Errorf("pin mismatch: installed %q does not match pinned %q", installedVersion, pin.Version)
	}
	if confirmation != "I understand the risk" {
		return errors.New("SECURITY WARNING: forced pin mismatch requires confirmation phrase")
	}
	return nil
}

// NPMHandler enforces npm/npx/node package version pins using sha512 integrity hashes.
type NPMHandler struct{}

func (NPMHandler) Detect(command string) bool {
	return command == "npx" || command == "node" || command == "npm"
}

func (NPMHandler) Validate(pin model.Pin, installedVersion string) error {
	if pin.Version == "" || !strings.HasPrefix(pin.Hash, "sha512-") {
		return errors.New("npm pins require version and sha512 integrity hash")
	}
	if installedVersion != "" && installedVersion != pin.Version {
		return fmt.Errorf("npm version mismatch: %s != %s", installedVersion, pin.Version)
	}
	return nil
}

func (NPMHandler) Inject(pin model.Pin, args []string) []string {
	next := append([]string{}, args...)
	if len(next) == 0 {
		return []string{"@" + pin.Version}
	}
	next[0] = packageWithVersion(next[0], pin.Version)
	return next
}

type DockerHandler struct{}

var digestPattern = regexp.MustCompile(`^sha256:[a-fA-F0-9]{64}$`)

func (DockerHandler) Detect(command string) bool {
	return command == "docker"
}

func (DockerHandler) Validate(pin model.Pin, installedVersion string) error {
	if !digestPattern.MatchString(pin.Hash) {
		return errors.New("docker pins require sha256 image digest")
	}
	return nil
}

func (DockerHandler) Inject(pin model.Pin, args []string) []string {
	next := append([]string{}, args...)
	for idx := 0; idx < len(next); idx++ {
		arg := next[idx]
		if arg == "run" || strings.HasPrefix(arg, "-") {
			continue
		}
		if looksLikeImage(arg) {
			next[idx] = strings.Split(arg, "@")[0] + "@" + pin.Hash
			return next
		}
	}
	return append(next, "@"+pin.Hash)
}

func looksLikeImage(value string) bool {
	if strings.HasPrefix(value, "/") || strings.Contains(value, "=") {
		return false
	}
	return strings.Contains(value, "/") || strings.Contains(value, ":")
}

type UVHandler struct{}

func (UVHandler) Detect(command string) bool {
	return command == "uvx" || command == "uv"
}

func (UVHandler) Validate(pin model.Pin, installedVersion string) error {
	if pin.Version == "" || !strings.HasPrefix(pin.Hash, "sha256:") {
		return errors.New("uv pins require version and sha256 hash")
	}
	if installedVersion != "" && installedVersion != pin.Version {
		return fmt.Errorf("uv version mismatch: %s != %s", installedVersion, pin.Version)
	}
	return nil
}

func (UVHandler) Inject(pin model.Pin, args []string) []string {
	next := append([]string{}, args...)
	if len(next) == 0 {
		return []string{"package==" + pin.Version}
	}
	next[0] = next[0] + "==" + pin.Version
	return next
}

func packageWithVersion(pkg string, version string) string {
	name := strings.Split(pkg, "@")[0]
	if strings.HasPrefix(pkg, "@") {
		parts := strings.Split(pkg[1:], "@")
		name = "@" + parts[0]
	}
	return name + "@" + version
}
