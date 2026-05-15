package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallHooksCommandInstallsAndUninstalls(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	rc := filepath.Join(root, ".zshrc")
	gitDir := filepath.Join(root, ".git")
	command := NewRootCommand(context.Background())
	command.SetArgs([]string{"--root", root, "install-hooks", "--shell-rc", rc, "--git-dir", gitDir})
	var out bytes.Buffer
	command.SetOut(&out)

	if err := command.Execute(); err != nil {
		t.Fatalf("install Execute() error = %v", err)
	}
	if !strings.Contains(out.String(), "installed hooks") {
		t.Fatalf("output = %q, want install report", out.String())
	}
	for _, path := range []string{rc, filepath.Join(gitDir, "hooks", "post-checkout")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s missing after install: %v", path, err)
		}
	}

	command = NewRootCommand(context.Background())
	command.SetArgs([]string{"--root", root, "install-hooks", "--uninstall", "--shell-rc", rc, "--git-dir", gitDir})
	if err := command.Execute(); err != nil {
		t.Fatalf("uninstall Execute() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(gitDir, "hooks", "post-checkout")); !os.IsNotExist(err) {
		t.Fatalf("post-checkout stat err = %v, want removed", err)
	}
}
