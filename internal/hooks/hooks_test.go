package hooks

import (
	"os"
	"path/filepath"
	"strings"
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

func TestDefaultRCPathForOS(t *testing.T) {
	home := t.TempDir()
	if got := defaultRCPathForOS("windows", home); got != filepath.Join(home, "Documents", "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1") {
		t.Fatalf("windows rcPath = %q", got)
	}
	if got := defaultRCPathForOS("linux", home); got != filepath.Join(home, ".zshrc") {
		t.Fatalf("linux rcPath = %q", got)
	}
	if got := defaultRCPathForOS("darwin", home); got != filepath.Join(home, ".zshrc") {
		t.Fatalf("darwin rcPath = %q", got)
	}
}

func TestShellSnippetForOS(t *testing.T) {
	win := shellSnippetForOS("windows")
	if !strings.Contains(win, "Set-Location") {
		t.Fatalf("windows snippet missing Set-Location: %q", win)
	}
	unix := shellSnippetForOS("linux")
	if !strings.Contains(unix, "graft_cd") {
		t.Fatalf("linux snippet missing graft_cd: %q", unix)
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
