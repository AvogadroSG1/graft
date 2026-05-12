package hooks

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallPostCheckoutRefusesUnmanagedHook(t *testing.T) {
	t.Parallel()
	gitDir := filepath.Join(t.TempDir(), ".git")
	hookPath := filepath.Join(gitDir, "hooks", "post-checkout")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\necho user\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InstallPostCheckout(gitDir); err == nil {
		t.Fatal("InstallPostCheckout() error = nil, want unmanaged hook refusal")
	}
}

func TestUninstallRefusesMixedHook(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rc := filepath.Join(root, ".zshrc")
	gitDir := filepath.Join(root, ".git")
	hookPath := filepath.Join(gitDir, "hooks", "post-checkout")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "#!/bin/sh\n" + Marker + "\necho graft\necho user\n"
	if err := os.WriteFile(hookPath, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(rc, gitDir); err == nil {
		t.Fatal("Uninstall() error = nil, want mixed hook refusal")
	}
}
