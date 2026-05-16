//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mock/handler.go -package=mock github.com/poconnor/graft/internal/pin Handler

// Package pin enforces reproducible version pinning for MCP server runtimes.
// Supported runtimes: npm (sha512 integrity), Docker (sha256 digest), uvx/uv (sha256 hash).
package pin

import (
	"encoding/base64"
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

var (
	// ErrInvalidPin marks malformed pin metadata that cannot be bypassed with force.
	ErrInvalidPin = errors.New("invalid pin")
	// ErrPinMismatch marks a valid pin that differs from the installed runtime artifact.
	ErrPinMismatch = errors.New("pin mismatch")
)

// Registry maps runtime names to their Handler implementations, preserving registration order.
type Registry struct {
	order    []string
	handlers map[string]Handler
}

// NewRegistry returns a Registry pre-loaded with npm, docker, uvx, and uv handlers.
func NewRegistry() Registry {
	reg := Registry{order: []string{}, handlers: map[string]Handler{}}
	reg.Register("npm", NPMHandler{})
	reg.Register("docker", DockerHandler{})
	reg.Register("uvx", UVHandler{})
	reg.Register("uv", UVHandler{})
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

// HandlerForRuntime returns the handler registered for an exact pin runtime name.
func (r Registry) HandlerForRuntime(runtime string) (Handler, bool) {
	handler, ok := r.handlers[runtime]
	return handler, ok
}

// Enforce validates that the installed runtime version satisfies pin. If there is a mismatch
// and force is true, the caller must also pass the exact phrase "I understand the risk" as
// confirmation — this two-factor check prevents accidental bypass of security pins.
func Enforce(handler Handler, pin model.Pin, installedVersion string, force bool, confirmation string) error {
	if handler == nil {
		return errors.New("no pin handler for runtime")
	}
	err := handler.Validate(pin, installedVersion)
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrInvalidPin) {
		return err
	}
	if !force {
		return fmt.Errorf("%w: installed %q does not match pinned %q", ErrPinMismatch, installedVersion, pin.Version)
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
	if pin.Version == "" || !validNPMIntegrity(pin.Hash) {
		return fmt.Errorf("%w: npm pins require version and sha512 integrity hash", ErrInvalidPin)
	}
	if installedVersion != "" && installedVersion != pin.Version {
		return fmt.Errorf("%w: npm version mismatch: %s != %s", ErrPinMismatch, installedVersion, pin.Version)
	}
	return nil
}

func (NPMHandler) Inject(pin model.Pin, args []string) []string {
	next := append([]string{}, args...)
	idx, prefix := firstPackageArg(next, map[string]bool{"--package": true, "-p": true}, map[string]bool{"--registry": true, "--cache": true, "--userconfig": true, "--tag": true, "--workspace": true, "-w": true})
	if idx == -1 {
		return []string{"@" + pin.Version}
	}
	next[idx] = prefix + packageWithVersion(strings.TrimPrefix(next[idx], prefix), pin.Version)
	return next
}

// DockerHandler enforces Docker image pins using sha256 image digests.
type DockerHandler struct{}

var digestPattern = regexp.MustCompile(`^sha256:[a-fA-F0-9]{64}$`)

func (DockerHandler) Detect(command string) bool {
	return command == "docker"
}

func (DockerHandler) Validate(pin model.Pin, installedVersion string) error {
	if !digestPattern.MatchString(pin.Hash) {
		return fmt.Errorf("%w: docker pins require sha256 image digest", ErrInvalidPin)
	}
	if installedVersion != "" && installedVersion != pin.Hash {
		return fmt.Errorf("%w: docker digest mismatch: %s != %s", ErrPinMismatch, installedVersion, pin.Hash)
	}
	return nil
}

func (DockerHandler) Inject(pin model.Pin, args []string) []string {
	next := append([]string{}, args...)
	skipNext := false
	for idx := 0; idx < len(next); idx++ {
		arg := next[idx]
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "run" {
			continue
		}
		if dockerOptionConsumesValue(arg) {
			skipNext = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
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

// UVHandler enforces uvx/uv package version pins using sha256 hashes.
type UVHandler struct{}

func (UVHandler) Detect(command string) bool {
	return command == "uvx" || command == "uv"
}

func (UVHandler) Validate(pin model.Pin, installedVersion string) error {
	if pin.Version == "" || !digestPattern.MatchString(pin.Hash) {
		return fmt.Errorf("%w: uv pins require version and sha256 hash", ErrInvalidPin)
	}
	if installedVersion != "" && installedVersion != pin.Version {
		return fmt.Errorf("%w: uv version mismatch: %s != %s", ErrPinMismatch, installedVersion, pin.Version)
	}
	return nil
}

func (UVHandler) Inject(pin model.Pin, args []string) []string {
	next := append([]string{}, args...)
	idx, prefix := firstPackageArg(next, map[string]bool{"--from": true}, map[string]bool{"--with": true, "--python": true, "-p": true})
	if idx == -1 {
		return []string{"package==" + pin.Version}
	}
	next[idx] = prefix + packageWithEqualsVersion(strings.TrimPrefix(next[idx], prefix), pin.Version)
	return next
}

// InstalledRuntimeVersion extracts the pinned runtime artifact version or digest from rendered args.
func InstalledRuntimeVersion(runtime string, args []string) string {
	switch runtime {
	case "npm":
		idx, prefix := firstPackageArg(args, map[string]bool{"--package": true, "-p": true}, map[string]bool{"--registry": true, "--cache": true, "--userconfig": true, "--tag": true, "--workspace": true, "-w": true})
		if idx == -1 {
			return ""
		}
		return npmPackageVersion(strings.TrimPrefix(args[idx], prefix))
	case "uv", "uvx":
		idx, prefix := firstPackageArg(args, map[string]bool{"--from": true}, map[string]bool{"--with": true, "--python": true, "-p": true})
		if idx == -1 {
			return ""
		}
		return uvPackageVersion(strings.TrimPrefix(args[idx], prefix))
	case "docker":
		return dockerArgDigest(args)
	default:
		return ""
	}
}

func firstPackageArg(args []string, packageValueOptions map[string]bool, otherValueOptions map[string]bool) (int, string) {
	skipNext := false
argsLoop:
	for idx, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		for opt := range packageValueOptions {
			if arg == opt {
				if idx+1 < len(args) {
					return idx + 1, ""
				}
				return -1, ""
			}
			prefix := opt + "="
			if strings.HasPrefix(arg, prefix) {
				return idx, prefix
			}
		}
		if otherValueOptions[arg] {
			skipNext = true
			continue
		}
		for opt := range otherValueOptions {
			if strings.HasPrefix(arg, opt+"=") {
				continue argsLoop
			}
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return idx, ""
	}
	return -1, ""
}

func dockerOptionConsumesValue(arg string) bool {
	switch arg {
	case "-p", "--publish", "-v", "--volume", "--name", "-e", "--env", "--network", "--entrypoint", "--workdir", "-w", "--user", "-u", "--platform":
		return true
	default:
		return false
	}
}

func npmPackageVersion(pkg string) string {
	if strings.HasPrefix(pkg, "@") {
		if idx := strings.LastIndex(pkg, "@"); idx > 0 {
			return pkg[idx+1:]
		}
		return ""
	}
	if idx := strings.LastIndex(pkg, "@"); idx > 0 {
		return pkg[idx+1:]
	}
	return ""
}

func packageWithVersion(pkg string, version string) string {
	if strings.HasPrefix(pkg, "@") {
		if idx := strings.LastIndex(pkg, "@"); idx > 0 {
			return pkg[:idx] + "@" + version
		}
		return pkg + "@" + version
	}
	if idx := strings.LastIndex(pkg, "@"); idx > 0 {
		return pkg[:idx] + "@" + version
	}
	return pkg + "@" + version
}

func uvPackageVersion(pkg string) string {
	if idx := strings.Index(pkg, "=="); idx >= 0 {
		return pkg[idx+2:]
	}
	return ""
}

func dockerArgDigest(args []string) string {
	for _, arg := range args {
		if idx := strings.Index(arg, "@sha256:"); idx >= 0 {
			return arg[idx+1:]
		}
	}
	return ""
}

func validNPMIntegrity(hash string) bool {
	encoded, ok := strings.CutPrefix(hash, "sha512-")
	if !ok {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	return err == nil && len(decoded) == 64
}

func packageWithEqualsVersion(pkg string, version string) string {
	if idx := strings.Index(pkg, "=="); idx >= 0 {
		return pkg[:idx] + "==" + version
	}
	return pkg + "==" + version
}
