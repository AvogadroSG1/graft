package pin

import (
	"strings"
	"testing"

	"github.com/poconnor/graft/internal/model"
)

func TestRegistryDetectsHandlers(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	if _, ok := reg.Handler("docker"); !ok {
		t.Fatal("Handler(docker) not found")
	}
	if _, ok := reg.Handler("unknown"); ok {
		t.Fatal("Handler(unknown) found unexpectedly")
	}
}

func TestEnforceRequiresConfirmationWhenForced(t *testing.T) {
	t.Parallel()
	err := Enforce(
		NPMHandler{},
		model.Pin{Runtime: "npm", Version: "1.0.0", Hash: "sha512-good"},
		"2.0.0",
		true,
		"nope",
	)
	if err == nil || !strings.Contains(err.Error(), "SECURITY WARNING") {
		t.Fatalf("Enforce() error = %v, want security warning", err)
	}
}

func TestDockerInjectDigest(t *testing.T) {
	t.Parallel()
	hash := "sha256:" + strings.Repeat("a", 64)
	got := DockerHandler{}.Inject(model.Pin{Hash: hash}, []string{"run", "ghcr.io/acme/tool:latest"})
	if got[1] != "ghcr.io/acme/tool:latest@"+hash {
		t.Fatalf("Inject() = %v", got)
	}
}

func TestRegistryZeroValueRegister(t *testing.T) {
	t.Parallel()
	var reg Registry
	reg.Register("npm", NPMHandler{})
	if _, ok := reg.Handler("npx"); !ok {
		t.Fatal("zero-value Registry did not retain registered handler")
	}
}

func TestEnforceNilHandler(t *testing.T) {
	t.Parallel()
	if err := Enforce(nil, model.Pin{}, "", false, ""); err == nil {
		t.Fatal("Enforce(nil) error = nil, want error")
	}
}

func TestDockerInjectSkipsVolumePath(t *testing.T) {
	t.Parallel()
	hash := "sha256:" + strings.Repeat("b", 64)
	got := DockerHandler{}.Inject(model.Pin{Hash: hash}, []string{"run", "-v", "/tmp:/tmp", "ghcr.io/acme/tool:latest"})
	if got[2] != "/tmp:/tmp" {
		t.Fatalf("Inject() changed volume arg: %v", got)
	}
	if got[3] != "ghcr.io/acme/tool:latest@"+hash {
		t.Fatalf("Inject() = %v, want image pinned", got)
	}
}
