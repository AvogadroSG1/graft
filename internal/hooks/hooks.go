// Package hooks installs and uninstalls the shell rc alias and git post-checkout hook
// that run "graft status --quiet" automatically when entering a directory or checking out a branch.
package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/poconnor/graft/internal/fileutil"
)

// Marker and EndMarker delimit the graft-managed block in shell rc files.
// They are also used to identify graft-owned hooks in git hook files.
const Marker = "# graft managed hook"
const EndMarker = "# end graft managed hook"

func InstallShellHook(rcPath string) error {
	snippet := Marker + "\n" +
		"graft_cd() { builtin cd \"$@\" && command -v graft >/dev/null && [[ -f graft.lock ]] && graft status --quiet; }\n" +
		"alias cd=graft_cd\n" +
		EndMarker + "\n"
	data, err := os.ReadFile(rcPath)
	if err == nil && strings.Contains(string(data), Marker) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read rc file: %w", err)
	}
	file, err := os.OpenFile(rcPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open rc file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()
	_, err = file.WriteString("\n" + snippet)
	return err
}

func InstallPostCheckout(gitDir string) error {
	path := filepath.Join(gitDir, "hooks", "post-checkout")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := "#!/bin/sh\n" + Marker + "\ncommand -v graft >/dev/null 2>&1 && graft status --quiet\n"
	data, err := os.ReadFile(path)
	if err == nil && !strings.Contains(string(data), Marker) {
		return fmt.Errorf("refusing to overwrite unmanaged hook %q", path)
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return fileutil.AtomicWriteFile(path, []byte(content), 0o755)
}

func Uninstall(rcPath, gitDir string) error {
	if err := removeShellBlock(rcPath); err != nil {
		return err
	}
	hookPath := filepath.Join(gitDir, "hooks", "post-checkout")
	data, err := os.ReadFile(hookPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	text := string(data)
	if strings.TrimSpace(text) == strings.TrimSpace("#!/bin/sh\n"+Marker+"\ncommand -v graft >/dev/null 2>&1 && graft status --quiet") {
		return os.Remove(hookPath)
	}
	if strings.Contains(text, Marker) {
		return fmt.Errorf("refusing to remove mixed user hook %q", hookPath)
	}
	return nil
}

func removeShellBlock(rcPath string) error {
	data, err := os.ReadFile(rcPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	next := []string{}
	skip := false
	for _, line := range lines {
		if line == Marker {
			skip = true
			continue
		}
		if skip && line == EndMarker {
			skip = false
			continue
		}
		if skip {
			continue
		}
		next = append(next, line)
	}
	return fileutil.AtomicWriteFile(rcPath, []byte(strings.Join(next, "\n")), 0o600)
}
