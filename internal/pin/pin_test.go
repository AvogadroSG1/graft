package pin

import (
	"strings"
	"testing"

	"github.com/poconnor/graft/internal/model"
)

const validNPMIntegrityHash = "sha512-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=="

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
		model.Pin{Runtime: "npm", Version: "1.0.0", Hash: validNPMIntegrityHash},
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

func TestRegistryReturnsHandlerForRuntimeName(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	for _, runtime := range []string{"npm", "docker", "uvx", "uv"} {
		if _, ok := reg.HandlerForRuntime(runtime); !ok {
			t.Fatalf("HandlerForRuntime(%s) not found", runtime)
		}
	}
	if _, ok := reg.HandlerForRuntime("npx"); ok {
		t.Fatal("HandlerForRuntime(npx) found command alias unexpectedly")
	}
}

func TestNPMInjectPinsFirstPackageArgumentAfterFlags(t *testing.T) {
	t.Parallel()
	got := NPMHandler{}.Inject(model.Pin{Version: "1.2.3"}, []string{"--yes", "@modelcontextprotocol/server"})
	want := []string{"--yes", "@modelcontextprotocol/server@1.2.3"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Inject() = %v, want %v", got, want)
	}
}

func TestNPMInjectSkipsRegistryOptionValue(t *testing.T) {
	t.Parallel()
	got := NPMHandler{}.Inject(model.Pin{Version: "1.2.3"}, []string{"--registry", "https://registry.example", "pkg"})
	want := []string{"--registry", "https://registry.example", "pkg@1.2.3"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Inject() = %v, want %v", got, want)
	}
}

func TestNPMValidateRejectsMalformedIntegrity(t *testing.T) {
	t.Parallel()
	if err := (NPMHandler{}).Validate(model.Pin{Runtime: "npm", Version: "1.0.0", Hash: "sha512-good"}, ""); err == nil {
		t.Fatal("Validate(malformed integrity) error = nil, want error")
	}
}

func TestNPMInjectPinsPackageEqualsOptionValue(t *testing.T) {
	t.Parallel()
	got := NPMHandler{}.Inject(model.Pin{Version: "1.2.3"}, []string{"--package=@scope/pkg", "server"})
	want := []string{"--package=@scope/pkg@1.2.3", "server"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Inject() = %v, want %v", got, want)
	}
}

func TestNPMInjectPinsPackageOptionValue(t *testing.T) {
	t.Parallel()
	got := NPMHandler{}.Inject(model.Pin{Version: "1.2.3"}, []string{"--package", "@scope/pkg", "server"})
	want := []string{"--package", "@scope/pkg@1.2.3", "server"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Inject() = %v, want %v", got, want)
	}
}

func TestNPMInjectPreservesScopedPackageName(t *testing.T) {
	t.Parallel()
	got := NPMHandler{}.Inject(model.Pin{Version: "1.2.3"}, []string{"@scope/pkg@0.1.0"})
	if got[0] != "@scope/pkg@1.2.3" {
		t.Fatalf("Inject() = %v, want scoped package pinned", got)
	}
}

func TestDockerInjectSkipsPortMappingOptionValue(t *testing.T) {
	t.Parallel()
	hash := "sha256:" + strings.Repeat("c", 64)
	got := DockerHandler{}.Inject(model.Pin{Hash: hash}, []string{"run", "-p", "8080:8080", "ghcr.io/acme/tool:latest"})
	if got[2] != "8080:8080" {
		t.Fatalf("Inject() changed port mapping: %v", got)
	}
	if got[3] != "ghcr.io/acme/tool:latest@"+hash {
		t.Fatalf("Inject() = %v, want image pinned", got)
	}
}

func TestUVInjectPinsFromPackageValue(t *testing.T) {
	t.Parallel()
	got := UVHandler{}.Inject(model.Pin{Version: "2.0.0"}, []string{"--from", "toolbox==1.0.0", "package"})
	want := []string{"--from", "toolbox==2.0.0", "package"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Inject() = %v, want %v", got, want)
	}
}

func TestUVInjectPinsFromEqualsPackageValue(t *testing.T) {
	t.Parallel()
	got := UVHandler{}.Inject(model.Pin{Version: "2.0.0"}, []string{"--from=toolbox==1.0.0", "package"})
	want := []string{"--from=toolbox==2.0.0", "package"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Inject() = %v, want %v", got, want)
	}
}

func TestDockerValidateAcceptsDigestAndRejectsTags(t *testing.T) {
	t.Parallel()
	hash := "sha256:" + strings.Repeat("d", 64)
	if err := (DockerHandler{}).Validate(model.Pin{Hash: hash}, ""); err != nil {
		t.Fatalf("Validate(valid digest) error = %v", err)
	}
	if err := (DockerHandler{}).Validate(model.Pin{Hash: hash}, "sha256:"+strings.Repeat("e", 64)); err == nil {
		t.Fatal("Validate(different digest) error = nil, want mismatch")
	}
	if err := (DockerHandler{}).Validate(model.Pin{Hash: "latest"}, ""); err == nil {
		t.Fatal("Validate(tag) error = nil, want error")
	}
}

func TestDockerInjectSkipsPlatformOptionValue(t *testing.T) {
	t.Parallel()
	hash := "sha256:" + strings.Repeat("e", 64)
	got := DockerHandler{}.Inject(model.Pin{Hash: hash}, []string{"run", "--platform", "linux/amd64", "ghcr.io/acme/tool:latest"})
	if got[2] != "linux/amd64" {
		t.Fatalf("Inject() changed platform: %v", got)
	}
	if got[3] != "ghcr.io/acme/tool:latest@"+hash {
		t.Fatalf("Inject() = %v, want image pinned", got)
	}
}

func TestEnforceDoesNotForceInvalidPinMaterial(t *testing.T) {
	t.Parallel()
	err := Enforce(NPMHandler{}, model.Pin{Runtime: "npm", Version: "1.0.0", Hash: "not-sha512"}, "2.0.0", true, "I understand the risk")
	if err == nil || !strings.Contains(err.Error(), "npm pins require") {
		t.Fatalf("Enforce() error = %v, want invalid pin material error", err)
	}
}
