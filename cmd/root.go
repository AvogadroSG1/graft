// Package cmd contains all Cobra command definitions for the graft CLI.
// NewRootCommand assembles the full command tree; Execute is the entry point called from main.
package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/poconnor/graft/internal/claudecfg"
	"github.com/poconnor/graft/internal/config"
	"github.com/poconnor/graft/internal/fileutil"
	"github.com/poconnor/graft/internal/hooks"
	"github.com/poconnor/graft/internal/library"
	"github.com/poconnor/graft/internal/lock"
	"github.com/poconnor/graft/internal/model"
	"github.com/poconnor/graft/internal/render"
	"github.com/poconnor/graft/internal/status"
	graftsync "github.com/poconnor/graft/internal/sync"
	"github.com/poconnor/graft/internal/tui"
	"github.com/spf13/cobra"
)

var version = "dev"

type appOptions struct {
	configPath string
	root       string
}

// Execute runs the root command and returns any error. Called from main.
func Execute(ctx context.Context) error {
	cmd := NewRootCommand(ctx)
	return cmd.ExecuteContext(ctx)
}

// ExitCode maps an error to a POSIX exit code: 0 for nil, 2 for usage errors
// (wrong number of arguments), 1 for all other errors.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	if strings.Contains(err.Error(), "accepts") || strings.Contains(err.Error(), "requires") {
		return 2
	}
	return 1
}

// NewRootCommand builds and returns the fully-wired graft command tree.
func NewRootCommand(ctx context.Context) *cobra.Command {
	opts := &appOptions{root: "."}
	root := &cobra.Command{
		Use:           "graft",
		Short:         "Add MCP skills to projects from git-backed libraries",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&opts.configPath, "config", "", "config file path")
	root.PersistentFlags().StringVar(&opts.root, "root", ".", "project root")
	root.AddCommand(
		newInitCommand(opts),
		newLibraryCommand(ctx, opts),
		newMCPCommand(ctx, opts),
		newStatusCommand(opts),
		newSyncCommand(ctx, opts),
		newInstallHooksCommand(opts),
		newPickCommand(ctx, opts),
	)
	return root
}

func newInitCommand(opts *appOptions) *cobra.Command {
	var libraryName string
	var targets string
	var yes bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize graft in a project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			store := lock.FileStore{}
			existing, err := store.Load(opts.root)
			if err != nil {
				return err
			}
			if len(existing.Libraries) > 0 && !yes {
				return fmt.Errorf("graft.lock exists; pass --yes to reinitialize")
			}
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			if libraryName == "" {
				if lib, ok := cfg.DefaultLibrary(); ok {
					libraryName = lib.Name
				}
			}
			lk := lock.Lock{Libraries: []lock.LibraryRef{}, MCPs: []lock.InstalledMCP{}}
			if libraryName != "" {
				lib, ok := cfg.Library(libraryName)
				if !ok {
					return fmt.Errorf("library %q is not registered", libraryName)
				}
				lk.Libraries = append(lk.Libraries, lock.LibraryRef{Name: lib.Name, URL: lib.URL})
			}
			for _, target := range parseTargets(targets) {
				if err := createTarget(opts.root, target); err != nil {
					return err
				}
			}
			if err := store.Save(opts.root, lk); err != nil {
				return err
			}
			return printf(cmd, "initialized graft at %s\n", opts.root)
		},
	}
	cmd.Flags().StringVar(&libraryName, "library", "", "library to reference")
	cmd.Flags().StringVar(&targets, "targets", "both", "claude, codex, or both")
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm non-interactive writes")
	return cmd
}

func newLibraryCommand(ctx context.Context, opts *appOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "library", Short: "Manage MCP libraries", Args: cobra.NoArgs}
	cmd.AddCommand(
		newLibraryAddCommand(ctx, opts),
		newLibraryListCommand(opts),
		newLibraryPullCommand(ctx, opts),
		newLibraryShowCommand(opts),
		newLibraryMigrateFromClaudeCommand(ctx, opts),
	)
	return cmd
}

func newLibraryAddCommand(ctx context.Context, opts *appOptions) *cobra.Command {
	var cachePath string
	cmd := &cobra.Command{
		Use:   "add <name> <url>",
		Short: "Register and clone a library",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			lib := config.Library{Name: args[0], URL: args[1], CachePath: cachePath}
			cfg, err = cfg.WithLibrary(lib)
			if err != nil {
				return err
			}
			lib, _ = cfg.Library(args[0])
			if err := (library.GitClient{}).Add(ctx, lib); err != nil {
				return err
			}
			if err := saveConfig(opts.configPath, cfg); err != nil {
				return err
			}
			return printf(cmd, "registered %s at %s\n", lib.Name, lib.CachePath)
		},
	}
	cmd.Flags().StringVar(&cachePath, "cache-path", "", "local cache path")
	return cmd
}

func newLibraryListCommand(opts *appOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered libraries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			for _, lib := range cfg.Libraries {
				marker := ""
				if lib.Default {
					marker = " default"
				}
				if err := printf(cmd, "%s\t%s\t%s%s\n", lib.Name, lib.URL, lib.CachePath, marker); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func newLibraryPullCommand(ctx context.Context, opts *appOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "pull [name]",
		Short: "Pull one or all libraries",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			libs := cfg.Libraries
			if len(args) == 1 {
				lib, ok := cfg.Library(args[0])
				if !ok {
					return fmt.Errorf("unknown library %q", args[0])
				}
				libs = []config.Library{lib}
			}
			for _, lib := range libs {
				sha, err := (library.GitClient{}).Pull(ctx, lib)
				if err != nil {
					return err
				}
				if err := printf(cmd, "%s\t%s\n", lib.Name, sha); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func newLibraryShowCommand(opts *appOptions) *cobra.Command {
	var tag string
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "show [name] [mcp]",
		Short: "Show library MCPs",
		Args:  cobra.RangeArgs(0, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			lib, ok := cfg.DefaultLibrary()
			if len(args) > 0 {
				lib, ok = cfg.Library(args[0])
			}
			if !ok {
				return fmt.Errorf("no library configured")
			}
			client := library.GitClient{}
			if len(args) == 2 {
				def, _, err := client.Definition(lib, args[1])
				if err != nil {
					return err
				}
				return writeValue(cmd, jsonOutput, def)
			}
			index, err := client.Index(lib)
			if err != nil {
				return err
			}
			for _, entry := range index.MCPs {
				if tag != "" && !contains(entry.Tags, tag) {
					continue
				}
				if jsonOutput {
					continue
				}
				if err := printf(
					cmd,
					"%s\t%s\t%s\t%s\n",
					entry.Name,
					entry.Version,
					strings.Join(entry.Tags, ","),
					entry.Description,
				); err != nil {
					return err
				}
			}
			if jsonOutput {
				return writeValue(cmd, true, index)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "filter by tag")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "emit JSON")
	return cmd
}

func newLibraryMigrateFromClaudeCommand(ctx context.Context, opts *appOptions) *cobra.Command {
	var from string
	var force bool
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "migrate-from-claude <name>",
		Short: "Create a library from Claude MCP configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := from
			if source == "" {
				var err error
				source, err = claudecfg.DefaultPath()
				if err != nil {
					return err
				}
			}
			groups, err := claudecfg.Load(source, opts.root)
			if err != nil {
				return err
			}
			if dryRun {
				return printClaudeMigrationDryRun(cmd, groups)
			}
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			cfg, lib, err := prepareLocalLibraryConfig(cfg, args[0])
			if err != nil {
				return err
			}
			client := library.GitClient{}
			if err := client.InitLocal(ctx, lib, force); err != nil {
				return err
			}
			approved, err := approveClaudeMCPs(cmd, groups)
			if err != nil {
				return err
			}
			for _, mcp := range approved {
				if _, err := library.WriteDefinition(lib, mcp.Definition); err != nil {
					return err
				}
				if err := printf(cmd, "imported %s\n", mcp.Name); err != nil {
					return err
				}
			}
			if _, err := client.Reindex(lib); err != nil {
				return err
			}
			if err := client.CommitAll(ctx, lib.CachePath, "Initial import from ~/.claude.json"); err != nil {
				return err
			}
			if err := saveConfig(opts.configPath, cfg); err != nil {
				return err
			}
			return printf(cmd, "registered %s at %s\n", lib.Name, lib.CachePath)
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "source Claude config file")
	cmd.Flags().BoolVar(&force, "force", false, "wipe existing library cache before import")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "preview imports without prompting or writing")
	return cmd
}

func printClaudeMigrationDryRun(cmd *cobra.Command, groups []claudecfg.Group) error {
	for _, group := range groups {
		for _, mcp := range group.MCPs {
			action := "would prompt"
			if group.Scope == claudecfg.ScopeGlobal {
				action = "would import"
			}
			if err := printf(cmd, "%s\t%s\t%s\n", mcp.Name, group.Name, action); err != nil {
				return err
			}
		}
	}
	return nil
}

func prepareLocalLibraryConfig(cfg config.Config, name string) (config.Config, config.Library, error) {
	if err := library.ValidateMCPName(name); err != nil {
		return cfg, config.Library{}, err
	}
	if existing, ok := cfg.Library(name); ok {
		if existing.CachePath == "" {
			next, err := cfg.WithLibrary(existing)
			if err != nil {
				return cfg, config.Library{}, err
			}
			existing, _ = next.Library(name)
			cfg = next
		}
		if existing.URL == "" {
			existing.URL = existing.CachePath
			next, err := cfg.WithLibrary(existing)
			if err != nil {
				return cfg, config.Library{}, err
			}
			existing, _ = next.Library(name)
			cfg = next
		}
		return cfg, existing, nil
	}
	next, err := cfg.WithLibrary(config.Library{Name: name})
	if err != nil {
		return cfg, config.Library{}, err
	}
	lib, ok := next.Library(name)
	if !ok {
		return cfg, config.Library{}, fmt.Errorf("library %q was not registered", name)
	}
	lib.URL = lib.CachePath
	next, err = next.WithLibrary(lib)
	if err != nil {
		return cfg, config.Library{}, err
	}
	lib, _ = next.Library(name)
	return next, lib, nil
}

func approveClaudeMCPs(cmd *cobra.Command, groups []claudecfg.Group) ([]claudecfg.MCP, error) {
	approved := []claudecfg.MCP{}
	seen := map[string]string{}
	reader := bufio.NewReader(cmd.InOrStdin())
	for _, group := range groups {
		approveAll := group.Scope == claudecfg.ScopeGlobal
		for _, mcp := range group.MCPs {
			if prior, ok := seen[mcp.Name]; ok {
				if err := eprintf(cmd, "warning: skipping duplicate MCP %s from %s; already imported from %s\n", mcp.Name, group.Name, prior); err != nil {
					return nil, err
				}
				continue
			}
			if !approveAll {
				choice, err := promptApproval(cmd, reader, group.Name, mcp.Name)
				if err != nil {
					return nil, err
				}
				switch choice {
				case "a":
					approveAll = true
				case "y":
				default:
					continue
				}
			}
			approved = append(approved, mcp)
			seen[mcp.Name] = group.Name
		}
	}
	return approved, nil
}

func promptApproval(cmd *cobra.Command, reader *bufio.Reader, groupName, mcpName string) (string, error) {
	for {
		if err := eprintf(cmd, "Import %s from %s? [y/n/a] ", mcpName, groupName); err != nil {
			return "", err
		}
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return "", err
		}
		choice := strings.ToLower(strings.TrimSpace(line))
		if choice == "y" || choice == "n" || choice == "a" {
			return choice, nil
		}
		if err == io.EOF && choice == "" {
			return "", fmt.Errorf("approval required for %s", mcpName)
		}
	}
}

func newMCPCommand(ctx context.Context, opts *appOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "mcp", Short: "Author MCP definitions", Args: cobra.NoArgs}
	cmd.AddCommand(newMCPImportCommand(opts), newMCPAddCommand(opts), newMCPEditCommand(opts), newMCPPushCommand(ctx, opts))
	return cmd
}

func newMCPImportCommand(opts *appOptions) *cobra.Command {
	var from string
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import MCP definitions from Claude JSON or Codex TOML",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" {
				return fmt.Errorf("--from is required")
			}
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			lib, ok := cfg.DefaultLibrary()
			if !ok {
				return fmt.Errorf("no default library configured")
			}
			defs, err := library.ImportFile(from)
			if err != nil {
				return err
			}
			reader := bufio.NewReader(cmd.InOrStdin())
			for _, def := range defs {
				if hasAuthFields(def) {
					answer, err := promptLine(cmd, reader, fmt.Sprintf("Import auth placeholders for %s? [y/N] ", def.Name))
					if err != nil {
						return err
					}
					if strings.ToLower(answer) != "y" && strings.ToLower(answer) != "yes" {
						if err := printf(cmd, "skipped %s\n", def.Name); err != nil {
							return err
						}
						continue
					}
				}
				written, err := writeImportedDefinition(cmd, reader, lib, def)
				if err != nil {
					return err
				}
				if written {
					if err := printf(cmd, "imported %s\n", def.Name); err != nil {
						return err
					}
				}
			}
			_, err = (library.GitClient{}).Reindex(lib)
			return err
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "source config file")
	return cmd
}

func writeImportedDefinition(cmd *cobra.Command, reader *bufio.Reader, lib config.Library, def model.Definition) (bool, error) {
	if _, err := library.WriteDefinition(lib, def); err == nil {
		return true, nil
	} else if !strings.Contains(err.Error(), "already exists") {
		return false, err
	}
	for {
		choice, err := promptLine(cmd, reader, fmt.Sprintf("MCP %s exists; choose keep/use-new/editor/skip: ", def.Name))
		if err != nil {
			return false, err
		}
		switch strings.ToLower(choice) {
		case "keep", "k":
			return false, printf(cmd, "kept %s\n", def.Name)
		case "use-new", "u":
			_, err := library.WriteDefinitionFile(lib, def, true)
			return err == nil, err
		case "editor", "e":
			edited, err := editDefinitionCandidate(cmd, def)
			if err != nil {
				return false, err
			}
			_, err = library.WriteDefinitionFile(lib, edited, true)
			return err == nil, err
		case "skip", "s":
			return false, printf(cmd, "skipped %s\n", def.Name)
		}
	}
}

func editDefinitionCandidate(cmd *cobra.Command, def model.Definition) (model.Definition, error) {
	library.NormalizeDefinition(&def)
	data, err := json.MarshalIndent(def, "", "  ")
	if err != nil {
		return model.Definition{}, err
	}
	tmp, err := os.CreateTemp("", def.Name+"-*.json")
	if err != nil {
		return model.Definition{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return model.Definition{}, err
	}
	if err := tmp.Close(); err != nil {
		return model.Definition{}, err
	}
	if err := runEditor(cmd, tmp.Name()); err != nil {
		return model.Definition{}, err
	}
	edited, err := readDefinitionFile(tmp.Name())
	if err != nil {
		return model.Definition{}, err
	}
	if edited.Name != def.Name {
		return model.Definition{}, fmt.Errorf("edited definition name %q does not match %q", edited.Name, def.Name)
	}
	return edited, nil
}

func readDefinitionFile(path string) (model.Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.Definition{}, err
	}
	var def model.Definition
	if err := json.Unmarshal(data, &def); err != nil {
		return model.Definition{}, err
	}
	return def, nil
}

func hasAuthFields(def model.Definition) bool {
	return len(def.Env) > 0 || len(def.Headers) > 0
}

func promptLine(cmd *cobra.Command, reader *bufio.Reader, prompt string) (string, error) {
	if err := eprintf(cmd, "%s", prompt); err != nil {
		return "", err
	}
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	if err == io.EOF && line == "" {
		return "", fmt.Errorf("input required")
	}
	return strings.TrimSpace(line), nil
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func runEditor(cmd *cobra.Command, path string) error {
	editor := os.Getenv("GRAFT_EDITOR")
	if editor == "" {
		editor = "/usr/bin/pico"
	}
	run := exec.Command(editor, path)
	run.Stdin = cmd.InOrStdin()
	run.Stdout = cmd.OutOrStdout()
	run.Stderr = cmd.ErrOrStderr()
	if err := run.Run(); err != nil {
		return fmt.Errorf("run editor: %w", err)
	}
	return nil
}

func newMCPAddCommand(opts *appOptions) *cobra.Command {
	var command string
	var description string
	var version string
	var transportType string
	var argsText string
	var tagsText string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a definition to the default library",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			lib, ok := cfg.DefaultLibrary()
			if !ok {
				return fmt.Errorf("no default library configured")
			}
			if !mcpAddFlagChanged(cmd) {
				reader := bufio.NewReader(cmd.InOrStdin())
				if description == "" {
					description, err = promptLine(cmd, reader, "Description: ")
					if err != nil {
						return err
					}
				}
				if version == "" {
					version, err = promptLine(cmd, reader, "Version [0.1.0]: ")
					if err != nil {
						return err
					}
				}
				if transportType == "" {
					transportType, err = promptLine(cmd, reader, "Type [stdio/http/sse]: ")
					if err != nil {
						return err
					}
				}
				if command == "" {
					command, err = promptLine(cmd, reader, "Command: ")
					if err != nil {
						return err
					}
				}
				if argsText == "" {
					argsText, err = promptLine(cmd, reader, "Args: ")
					if err != nil {
						return err
					}
				}
				if tagsText == "" {
					tagsText, err = promptLine(cmd, reader, "Tags: ")
					if err != nil {
						return err
					}
				}
			}
			if version == "" {
				version = "0.1.0"
			}
			def := model.Definition{Name: args[0], Version: version, Description: description, Type: transportType, Command: command, Args: strings.Fields(argsText), Tags: splitCSV(tagsText), Env: map[string]string{}, Headers: map[string]string{}}
			if _, err := library.WriteDefinition(lib, def); err != nil {
				return err
			}
			_, err = (library.GitClient{}).Reindex(lib)
			return err
		},
	}
	cmd.Flags().StringVar(&command, "command", "", "command to run")
	cmd.Flags().StringVar(&description, "description", "", "description")
	cmd.Flags().StringVar(&version, "version", "", "definition version")
	cmd.Flags().StringVar(&transportType, "type", "", "transport type")
	cmd.Flags().StringVar(&argsText, "args", "", "command args")
	cmd.Flags().StringVar(&tagsText, "tags", "", "comma-separated tags")
	return cmd
}

func mcpAddFlagChanged(cmd *cobra.Command) bool {
	for _, name := range []string{"command", "description", "version", "type", "args", "tags"} {
		if cmd.Flags().Changed(name) {
			return true
		}
	}
	return false
}

func newMCPEditCommand(opts *appOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "edit <name>",
		Short: "Open an MCP definition in pico",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			lib, ok := cfg.DefaultLibrary()
			if !ok {
				return fmt.Errorf("no default library configured")
			}
			if err := library.ValidateMCPName(args[0]); err != nil {
				return err
			}
			path := filepath.Join(lib.CachePath, "mcps", args[0]+".json")
			if err := runEditor(cmd, path); err != nil {
				return err
			}
			def, _, err := (library.GitClient{}).Definition(lib, args[0])
			if err != nil {
				return err
			}
			if def.Name != args[0] {
				return fmt.Errorf("edited definition name %q does not match %q", def.Name, args[0])
			}
			_, err = (library.GitClient{}).Reindex(lib)
			return err
		},
	}
}

func newMCPPushCommand(ctx context.Context, opts *appOptions) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Reindex, commit, and push library changes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return fmt.Errorf("--yes is required for non-interactive push")
			}
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			lib, ok := cfg.DefaultLibrary()
			if !ok {
				return fmt.Errorf("no default library configured")
			}
			client := library.GitClient{}
			if _, err := client.Reindex(lib); err != nil {
				return err
			}
			diff, err := client.Diff(ctx, lib.CachePath)
			if err != nil {
				return err
			}
			if diff != "" {
				if err := printf(cmd, "%s", diff); err != nil {
					return err
				}
			}
			sha, err := client.PushAll(ctx, lib.CachePath, "feat(mcps): update library definitions")
			if err != nil {
				return err
			}
			return printf(cmd, "commit %s\n", sha)
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm non-interactive push")
	return cmd
}

func newStatusCommand(opts *appOptions) *cobra.Command {
	return newStatusCommandWithDeps(opts, library.GitClient{})
}

func newStatusCommandWithDeps(opts *appOptions, client library.Client) *cobra.Command {
	var quiet bool
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show graft drift state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			lk, err := (lock.FileStore{}).Load(opts.root)
			if err != nil {
				return err
			}
			if !quiet {
				cfg, err = ensureLockLibrariesRegistered(cmd.Context(), cmd, opts, cfg, lk, client)
				if err != nil {
					return err
				}
			}
			indexes := map[string]model.LibraryIndex{}
			result := status.Resolve(opts.root, cfg, lk, indexes)
			if result.State == status.StateConfigured {
				indexes, err = statusIndexes(cfg, lk, client)
				if err != nil {
					return err
				}
				definitions, err := statusDefinitions(opts.root, cfg, lk, client)
				if err != nil {
					return err
				}
				result = status.ResolveWithDefinitions(opts.root, cfg, lk, indexes, definitions)
			}
			if quiet {
				if result.State == status.StateConfigured {
					return nil
				}
				return fmt.Errorf("%s", result.State)
			}
			if jsonOutput {
				return writeValue(cmd, true, result)
			}
			return writeStatusPlain(cmd, result, lk, indexes)
		},
	}
	cmd.Flags().BoolVar(&quiet, "quiet", false, "exit non-zero unless configured")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "emit JSON")
	return cmd
}

func newSyncCommand(ctx context.Context, opts *appOptions) *cobra.Command {
	return newSyncCommandWithDeps(ctx, opts, library.GitClient{}, nil)
}

func newSyncCommandWithDeps(ctx context.Context, opts *appOptions, client library.Client, adapters []render.AdapterByName) *cobra.Command {
	var noPull bool
	var forcePins bool
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Apply library updates to selected MCPs",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			lk, err := (lock.FileStore{}).Load(opts.root)
			if err != nil {
				return err
			}
			cfg, err = ensureLockLibrariesRegistered(ctx, cmd, opts, cfg, lk, client)
			if err != nil {
				return err
			}
			pullLibraries, err := syncPullLibraries(cfg, lk, args)
			if err != nil {
				return err
			}
			if !noPull {
				for _, lib := range pullLibraries {
					if _, err := client.Pull(ctx, lib); err != nil {
						return err
					}
				}
			}
			if len(lk.MCPs) == 0 {
				return writeValue(cmd, true, graftsync.Result{
					Succeeded: []string{},
					Failed:    []string{},
					Skipped:   []string{},
					Warnings:  []string{},
					Errors:    []string{},
					Lock:      lk,
				})
			}
			activeAdapters := adapters
			if activeAdapters == nil {
				activeAdapters = []render.AdapterByName{
					{Name: "claude", Adapter: render.NewClaudeAdapter(opts.root)},
					{Name: "codex", Adapter: render.NewCodexAdapter(opts.root)},
				}
			}
			result := graftsync.ApplyWithOptions(
				lk,
				cfg,
				client,
				activeAdapters,
				graftsync.Options{Names: args, ForcePins: forcePins, ConfirmPinMismatch: func(diff string) (string, error) {
					if err := eprintf(cmd, "SECURITY WARNING: pin mismatch detected.\n%s\nType 'I understand the risk' to continue: ", diff); err != nil {
						return "", err
					}
					answer, hasInput, err := readPromptAnswer(cmd.InOrStdin())
					if err != nil {
						return "", err
					}
					if !hasInput {
						return "", fmt.Errorf("SECURITY WARNING: forced pin mismatch requires confirmation phrase")
					}
					return answer, nil
				}},
			)
			if err := (lock.FileStore{}).Save(opts.root, result.Lock); err != nil {
				return err
			}
			if err := writeValue(cmd, true, result); err != nil {
				return err
			}
			return syncSecurityError(result)
		},
	}
	cmd.Flags().BoolVar(&noPull, "no-pull", false, "skip pulling libraries before sync")
	cmd.Flags().BoolVar(&forcePins, "force", false, "force pin mismatch after typing the risk confirmation phrase")
	return cmd
}

func writeStatusPlain(cmd *cobra.Command, result status.Result, lk lock.Lock, indexes map[string]model.LibraryIndex) error {
	if len(lk.MCPs) == 0 {
		if err := println(cmd, result.State); err != nil {
			return err
		}
		return writeStatusDetails(cmd, result)
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "ICON\tSTATE\tMCP\tINSTALLED\tLIBRARY"); err != nil {
		return err
	}
	for _, mcp := range lk.MCPs {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", statusIcon(statusRowState(result, indexes, mcp)), statusRowState(result, indexes, mcp), mcp.Name, statusValue(mcp.Version), statusValue(statusLibraryVersion(indexes, mcp))); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	return writeStatusDetails(cmd, result)
}

func writeStatusDetails(cmd *cobra.Command, result status.Result) error {
	for _, detail := range result.Details {
		if err := printf(cmd, "detail: %s\n", detail); err != nil {
			return err
		}
	}
	if hint := statusHint(result.State); hint != "" {
		return printf(cmd, "hint: %s\n", hint)
	}
	return nil
}

func statusRowState(result status.Result, indexes map[string]model.LibraryIndex, mcp lock.InstalledMCP) status.State {
	if mcp.PendingInput {
		return status.StatePendingInput
	}
	if result.State == status.StateDrifted && statusDefinitionDrifted(indexes, mcp) {
		return status.StateDrifted
	}
	if result.State == status.StateConfigured {
		return status.StateConfigured
	}
	for _, detail := range result.Details {
		if statusDetailMatchesMCP(detail, mcp.Name) {
			return result.State
		}
	}
	if result.State == status.StateDrifted || result.State == status.StatePinMismatch || result.State == status.StatePendingInput {
		return status.StateConfigured
	}
	return result.State
}

func statusDetailMatchesMCP(detail string, name string) bool {
	if detail == name {
		return true
	}
	needle := "/" + name
	idx := strings.Index(detail, needle)
	if idx == -1 {
		return false
	}
	after := detail[idx+len(needle):]
	return after == "" || strings.HasPrefix(after, " ") || strings.HasPrefix(after, ":")
}

func statusDefinitionDrifted(indexes map[string]model.LibraryIndex, mcp lock.InstalledMCP) bool {
	index, ok := indexes[mcp.Library]
	if !ok {
		return false
	}
	for _, entry := range index.MCPs {
		if entry.Name == mcp.Name {
			return entry.SHA256 != "" && entry.SHA256 != mcp.DefinitionHash
		}
	}
	return false
}

func statusLibraryVersion(indexes map[string]model.LibraryIndex, mcp lock.InstalledMCP) string {
	index, ok := indexes[mcp.Library]
	if !ok {
		return ""
	}
	for _, entry := range index.MCPs {
		if entry.Name == mcp.Name {
			return entry.Version
		}
	}
	return ""
}

func statusValue(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func statusIcon(state status.State) string {
	switch state {
	case status.StateConfigured:
		return "OK"
	case status.StateDrifted, status.StatePinMismatch:
		return "!!"
	case status.StatePendingInput, status.StateUnknownLibrary:
		return "??"
	case status.StateInitialized, status.StateUninitialized:
		return ".."
	default:
		return "--"
	}
}

func statusHint(state status.State) string {
	switch state {
	case status.StateDrifted:
		return "run graft sync"
	case status.StatePinMismatch:
		return "inspect pin metadata or run graft sync --force"
	case status.StatePendingInput:
		return "run graft sync and provide required input"
	case status.StateUnknownLibrary:
		return "register the missing library with graft library add"
	case status.StateUninitialized:
		return "run graft init"
	default:
		return ""
	}
}

func statusIndexes(cfg config.Config, lk lock.Lock, client library.Client) (map[string]model.LibraryIndex, error) {
	indexes := map[string]model.LibraryIndex{}
	seen := map[string]bool{}
	for _, ref := range lk.Libraries {
		if seen[ref.Name] {
			continue
		}
		seen[ref.Name] = true
		lib, ok := cfg.Library(ref.Name)
		if !ok {
			continue
		}
		index, err := client.Index(lib)
		if err != nil {
			return nil, err
		}
		indexes[ref.Name] = index
	}
	return indexes, nil
}

func statusDefinitions(root string, cfg config.Config, lk lock.Lock, client library.Client) (map[string]map[string]model.Definition, error) {
	defs := map[string]map[string]model.Definition{}
	seen := map[string]bool{}
	for _, mcp := range lk.MCPs {
		if !statusHasRenderedTarget(root, mcp.Target) {
			continue
		}
		key := mcp.Library + "/" + mcp.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		lib, ok := cfg.Library(mcp.Library)
		if !ok {
			continue
		}
		def, _, err := client.Definition(lib, mcp.Name)
		if err != nil {
			return nil, err
		}
		if defs[mcp.Library] == nil {
			defs[mcp.Library] = map[string]model.Definition{}
		}
		defs[mcp.Library][mcp.Name] = def
	}
	return defs, nil
}

func statusHasRenderedTarget(root, target string) bool {
	for _, name := range parseTargets(target) {
		var path string
		switch name {
		case "claude":
			path = filepath.Join(root, ".mcp.json")
		case "codex":
			path = filepath.Join(root, ".codex", "config.toml")
		}
		if path != "" {
			if _, err := os.Stat(path); err == nil {
				return true
			}
		}
	}
	return false
}

func syncSecurityError(result graftsync.Result) error {
	for _, errText := range result.Errors {
		if strings.Contains(errText, "pin:") {
			return fmt.Errorf("%s", errText)
		}
	}
	return nil
}

func ensureLockLibrariesRegistered(ctx context.Context, cmd *cobra.Command, opts *appOptions, cfg config.Config, lk lock.Lock, client library.Client) (config.Config, error) {
	next := cfg
	for _, ref := range lk.Libraries {
		if _, ok := next.Library(ref.Name); ok {
			continue
		}
		if ref.URL == "" {
			return next, fmt.Errorf("library %q is not registered; graft.lock does not include a URL", ref.Name)
		}
		if err := eprintf(cmd, "Library %q from graft.lock is not registered: %s\nRegister and clone it now? [Y/n] ", ref.Name, ref.URL); err != nil {
			return next, err
		}
		answer, hasInput, err := readPromptAnswer(cmd.InOrStdin())
		if err != nil {
			return next, err
		}
		if !acceptRegistration(answer, hasInput) {
			return next, fmt.Errorf("library %q is not registered; run graft library add %s %s", ref.Name, ref.Name, ref.URL)
		}
		updated, err := next.WithLibrary(config.Library{Name: ref.Name, URL: ref.URL})
		if err != nil {
			return next, err
		}
		lib, _ := updated.Library(ref.Name)
		if err := client.Add(ctx, lib); err != nil {
			return next, err
		}
		if err := saveConfig(opts.configPath, updated); err != nil {
			return next, err
		}
		next = updated
	}
	return next, nil
}

func readPromptAnswer(input io.Reader) (string, bool, error) {
	answer, err := bufio.NewReader(input).ReadString('\n')
	if err != nil {
		if errors.Is(err, io.EOF) {
			if answer == "" {
				return "", false, nil
			}
			return strings.TrimSpace(answer), true, nil
		}
		return "", false, err
	}
	return strings.TrimSpace(answer), true, nil
}

func acceptRegistration(answer string, hasInput bool) bool {
	if !hasInput {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "y", "yes":
		return true
	default:
		return false
	}
}

func syncPullLibraries(cfg config.Config, lk lock.Lock, names []string) ([]config.Library, error) {
	wanted := map[string]bool{}
	for _, name := range names {
		wanted[name] = true
	}
	matched := map[string]bool{}
	seenLibraries := map[string]bool{}
	pullLibraries := []config.Library{}
	for _, mcp := range lk.MCPs {
		if len(wanted) > 0 {
			if !wanted[mcp.Name] {
				continue
			}
			matched[mcp.Name] = true
		}
		lib, ok := cfg.Library(mcp.Library)
		if !ok || seenLibraries[lib.Name] {
			continue
		}
		seenLibraries[lib.Name] = true
		pullLibraries = append(pullLibraries, lib)
	}
	for name := range wanted {
		if !matched[name] {
			return nil, fmt.Errorf("MCP %q is not installed", name)
		}
	}
	return pullLibraries, nil
}

func newInstallHooksCommand(opts *appOptions) *cobra.Command {
	var uninstall bool
	var rcPath string
	var gitDir string
	cmd := &cobra.Command{
		Use:   "install-hooks",
		Short: "Install shell and git hooks for drift checks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if rcPath == "" {
				var err error
				rcPath, err = hooks.DefaultRCPath()
				if err != nil {
					return err
				}
			}
			if gitDir == "" {
				gitDir = filepath.Join(opts.root, ".git")
			}
			if uninstall {
				return hooks.Uninstall(rcPath, gitDir)
			}
			if err := hooks.InstallShellHook(rcPath); err != nil {
				return err
			}
			if err := hooks.InstallPostCheckout(gitDir); err != nil {
				return err
			}
			return printf(cmd, "installed hooks in %s and %s\n", rcPath, gitDir)
		},
	}
	cmd.Flags().BoolVar(&uninstall, "uninstall", false, "remove graft-owned hooks")
	cmd.Flags().StringVar(&rcPath, "shell-rc", "", "shell rc file path")
	cmd.Flags().StringVar(&gitDir, "git-dir", "", "git directory")
	return cmd
}

type pickRunner func(context.Context, tui.PickModel) (tui.PickModel, error)

func newPickCommand(ctx context.Context, opts *appOptions) *cobra.Command {
	return newPickCommandWithDeps(ctx, opts, library.GitClient{}, runBubbleteaPick)
}

func newPickCommandWithDeps(ctx context.Context, opts *appOptions, client library.Client, runner pickRunner) *cobra.Command {
	var targets string
	cmd := &cobra.Command{
		Use:   "pick",
		Short: "Select MCPs from registered libraries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, err := normalizePickTarget(targets)
			if err != nil {
				return err
			}
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			lk, err := (lock.FileStore{}).Load(opts.root)
			if err != nil {
				return err
			}
			cfg, err = ensureLockLibrariesRegistered(ctx, cmd, opts, cfg, lk, client)
			if err != nil {
				return err
			}
			if len(cfg.Libraries) == 0 {
				return fmt.Errorf("no libraries configured")
			}
			if len(lk.Libraries) == 0 {
				return fmt.Errorf("run graft init with a library first")
			}
			pickList, err := buildPickList(cfg, client)
			if err != nil {
				return err
			}
			for _, warning := range pickList.Warnings {
				if err := eprintf(cmd, "warning: %s\n", warning); err != nil {
					return err
				}
			}
			preSelected := []string{}
			for _, mcp := range lk.MCPs {
				preSelected = append(preSelected, mcp.Library+"/"+mcp.Name)
			}
			model, err := runner(ctx, tui.NewPickModel(pickList.Items, preSelected))
			if err != nil {
				return err
			}
			if !model.Confirmed {
				return nil
			}
			next, err := applyPickResult(lk, model.Results(), target)
			if err != nil {
				return err
			}
			return (lock.FileStore{}).Save(opts.root, next)
		},
	}
	cmd.Flags().StringVar(&targets, "targets", "both", "claude, codex, or both")
	return cmd
}

// NewPickCommandForTest exposes the dependency-injected pick command to black-box
// BDD tests in the features package.
func NewPickCommandForTest(ctx context.Context, configPath, root string, client library.Client, runner func(context.Context, tui.PickModel) (tui.PickModel, error)) *cobra.Command {
	return newPickCommandWithDeps(ctx, &appOptions{configPath: configPath, root: root}, client, runner)
}

func runBubbleteaPick(ctx context.Context, model tui.PickModel) (tui.PickModel, error) {
	final, err := tea.NewProgram(model, tea.WithContext(ctx)).Run()
	if err != nil {
		return tui.PickModel{}, err
	}
	picker, ok := final.(tui.PickModel)
	if !ok {
		return tui.PickModel{}, fmt.Errorf("picker returned unexpected model %T", final)
	}
	return picker, nil
}

func normalizePickTarget(raw string) (string, error) {
	targets := parseTargets(raw)
	if len(targets) == 0 {
		return "", fmt.Errorf("target is required")
	}
	seen := map[string]bool{}
	for _, target := range targets {
		switch target {
		case "claude", "codex":
			seen[target] = true
		default:
			return "", fmt.Errorf("unknown target %q", target)
		}
	}
	if seen["claude"] && seen["codex"] {
		return "both", nil
	}
	if seen["claude"] {
		return "claude", nil
	}
	return "codex", nil
}

type pickListResult struct {
	Items    []tui.PickItem
	Warnings []string
}

func buildPickList(cfg config.Config, client library.Client) (pickListResult, error) {
	items := []tui.PickItem{}
	warnings := []string{}
	seen := map[string]string{}
	for _, lib := range cfg.Libraries {
		index, err := client.Index(lib)
		if err != nil {
			return pickListResult{}, fmt.Errorf("index library %q: %w", lib.Name, err)
		}
		for _, entry := range index.MCPs {
			if prior, ok := seen[entry.Name]; ok {
				warnings = append(warnings, fmt.Sprintf("duplicate MCP name %q in libraries %s and %s", entry.Name, prior, lib.Name))
			} else {
				seen[entry.Name] = lib.Name
			}
			items = append(items, tui.PickItem{Entry: entry, Library: lib.Name})
		}
	}
	return pickListResult{Items: items, Warnings: warnings}, nil
}

func applyPickResult(lk lock.Lock, results []tui.PickItem, target string) (lock.Lock, error) {
	libraries := map[string]bool{}
	for _, lib := range lk.Libraries {
		libraries[lib.Name] = true
	}
	seenNames := map[string]bool{}
	next := lk
	next.MCPs = []lock.InstalledMCP{}
	for _, result := range results {
		if !libraries[result.Library] {
			return lk, fmt.Errorf("library %q is not in graft.lock", result.Library)
		}
		if seenNames[result.Entry.Name] {
			return lk, fmt.Errorf("duplicate MCP name %q selected; rendered targets are keyed by name", result.Entry.Name)
		}
		seenNames[result.Entry.Name] = true
		next.MCPs = append(next.MCPs, lock.InstalledMCP{
			Name:           result.Entry.Name,
			Library:        result.Library,
			Version:        result.Entry.Version,
			DefinitionHash: result.Entry.SHA256,
			Target:         target,
		})
	}
	return next, nil
}

func loadConfig(path string) (config.Config, error) {
	return (config.FileStore{}).Load(path)
}

func saveConfig(path string, cfg config.Config) error {
	return (config.FileStore{}).Save(path, cfg)
}

func parseTargets(raw string) []string {
	switch raw {
	case "both":
		return []string{"claude", "codex"}
	case "claude", "codex":
		return []string{raw}
	default:
		parts := strings.Split(raw, ",")
		targets := []string{}
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				targets = append(targets, part)
			}
		}
		return targets
	}
}

func createTarget(root, target string) error {
	switch target {
	case "claude":
		path := filepath.Join(root, ".mcp.json")
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		return fileutil.AtomicWriteFile(path, []byte("{\n  \"mcpServers\": {}\n}\n"), 0o600)
	case "codex":
		path := filepath.Join(root, ".codex", "config.toml")
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return fileutil.AtomicWriteFile(path, []byte("[mcp_servers]\n"), 0o600)
	default:
		return fmt.Errorf("unknown target %q", target)
	}
}

func writeValue(cmd *cobra.Command, jsonOutput bool, value any) error {
	if !jsonOutput {
		return println(cmd, value)
	}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func printf(cmd *cobra.Command, format string, args ...any) error {
	_, err := fmt.Fprintf(cmd.OutOrStdout(), format, args...)
	return err
}

func eprintf(cmd *cobra.Command, format string, args ...any) error {
	_, err := fmt.Fprintf(cmd.ErrOrStderr(), format, args...)
	return err
}

func println(cmd *cobra.Command, args ...any) error {
	_, err := fmt.Fprintln(cmd.OutOrStdout(), args...)
	return err
}

func contains(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
