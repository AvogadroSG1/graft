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

func TestDefaultRCPathForShell(t *testing.T) {
	home := t.TempDir()
	if got := defaultRCPathForShell("linux", home, "/bin/bash"); got != filepath.Join(home, ".bashrc") {
		t.Fatalf("bash rcPath = %q, want .bashrc", got)
	}
	if got := defaultRCPathForShell("linux", home, "/bin/zsh"); got != filepath.Join(home, ".zshrc") {
		t.Fatalf("zsh rcPath = %q, want .zshrc", got)
	}
}

func TestShellSnippetForOS(t *testing.T) {
	win := shellSnippetForOS("windows")
	if !strings.Contains(win, "Set-Location") {
		t.Fatalf("windows snippet missing Set-Location: %q", win)
	}
	unix := shellSnippetForOS("linux")
	if !strings.Contains(unix, "graft_cd") || !strings.Contains(unix, "graft status --quiet || true") {
		t.Fatalf("linux snippet missing graft_cd: %q", unix)
	}
}

func TestInstallShellHookAppendsSnippetIdempotently(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".bashrc")
	if err := os.WriteFile(rc, []byte("export PATH=/usr/bin\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallShellHook(rc); err != nil {
		t.Fatalf("InstallShellHook() error = %v", err)
	}
	if err := InstallShellHook(rc); err != nil {
		t.Fatalf("InstallShellHook() second error = %v", err)
	}
	data, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, Marker) != 1 || !strings.Contains(text, "command -v graft") || !strings.Contains(text, "[[ -f graft.lock ]]") {
		t.Fatalf("rc contents = %q, want one guarded graft hook", text)
	}
}

func TestInstallPostCheckoutWritesExecutableManagedHook(t *testing.T) {
	t.Parallel()
	gitDir := filepath.Join(t.TempDir(), ".git")
	if err := InstallPostCheckout(gitDir); err != nil {
		t.Fatalf("InstallPostCheckout() error = %v", err)
	}
	if err := InstallPostCheckout(gitDir); err != nil {
		t.Fatalf("InstallPostCheckout() second error = %v", err)
	}
	path := filepath.Join(gitDir, "hooks", "post-checkout")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 || !strings.Contains(string(data), Marker) || !strings.Contains(string(data), "graft status --quiet") {
		t.Fatalf("post-checkout mode=%v contents=%q, want executable managed status hook", info.Mode().Perm(), string(data))
	}
}

func TestInstallPostCheckoutRefusesMixedManagedHook(t *testing.T) {
	t.Parallel()
	gitDir := filepath.Join(t.TempDir(), ".git")
	hookPath := filepath.Join(gitDir, "hooks", "post-checkout")
	if err := os.MkdirAll(filepath.Dir(hookPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hookPath, []byte(ownedPostCheckoutContent()+"echo user\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := InstallPostCheckout(gitDir); err == nil {
		t.Fatal("InstallPostCheckout() error = nil, want mixed managed hook refusal")
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

func TestUninstallRemovesOwnedShellBlockAndHook(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rc := filepath.Join(root, ".zshrc")
	gitDir := filepath.Join(root, ".git")
	if err := InstallShellHook(rc); err != nil {
		t.Fatal(err)
	}
	if err := InstallPostCheckout(gitDir); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(rc, gitDir); err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	data, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), Marker) {
		t.Fatalf("rc contents = %q, want graft block removed", string(data))
	}
	if _, err := os.Stat(filepath.Join(gitDir, "hooks", "post-checkout")); !os.IsNotExist(err) {
		t.Fatalf("post-checkout stat err = %v, want removed", err)
	}
}

func TestUninstallRefusesUnterminatedShellBlock(t *testing.T) {
	t.Parallel()
	rc := filepath.Join(t.TempDir(), ".zshrc")
	content := "export PATH=/usr/bin\n" + Marker + "\necho after\n"
	if err := os.WriteFile(rc, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeShellBlock(rc); err == nil {
		t.Fatal("removeShellBlock() error = nil, want unterminated block refusal")
	}
	data, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != content {
		t.Fatalf("rc contents changed to %q, want original preserved", string(data))
	}
}
