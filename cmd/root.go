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
	"sort"
	"strings"
	"text/tabwriter"
	"time"

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
		newInitCommand(ctx, opts),
		newAddCommand(opts),
		newAddInteractiveCommand(opts),
		newLibraryCommand(ctx, opts),
		newMCPCommand(ctx, opts),
		newStatusCommand(opts),
		newSyncCommand(ctx, opts),
		newInstallHooksCommand(opts),
		newPickCommand(ctx, opts),
	)
	return root
}

func checkInitLock(root string, yes bool) error {
	lockPath := filepath.Join(root, "graft.lock")
	_, statErr := os.Stat(lockPath)
	if statErr != nil && !os.IsNotExist(statErr) {
		return fmt.Errorf("stat graft.lock: %w", statErr)
	}
	if statErr == nil && !yes {
		return fmt.Errorf("graft.lock exists; pass --yes to reinitialize")
	}
	return nil
}

func buildInitLock(cfg config.Config, libraryName string) (lock.Lock, error) {
	if libraryName == "" {
		if lib, ok := cfg.DefaultLibrary(); ok {
			libraryName = lib.Name
		}
	}
	lk := lock.Lock{Libraries: []lock.LibraryRef{}, MCPs: []lock.InstalledMCP{}}
	if libraryName == "" {
		return lk, nil
	}
	lib, ok := cfg.Library(libraryName)
	if !ok {
		return lk, fmt.Errorf("library %q is not registered with graft CLI", libraryName)
	}
	lk.Libraries = append(lk.Libraries, lock.LibraryRef{Name: lib.Name, URL: lib.URL})
	return lk, nil
}

func createInitTargets(root string, targetNames []string) ([]string, error) {
	stagePaths := []string{"graft.lock"}
	for _, target := range targetNames {
		path, created, err := createTarget(root, target)
		if err != nil {
			return nil, err
		}
		if created {
			stagePaths = append(stagePaths, path)
		}
	}
	return stagePaths, nil
}

func runPickAfterInit(ctx context.Context, cmd *cobra.Command, opts *appOptions, lk lock.Lock, targets string, client library.Client, runner pickRunner, pRunner placeholderRunner) error {
	if len(lk.Libraries) == 0 {
		return nil
	}
	pickCmd := newPickCommandWithDeps(ctx, opts, client, runner, pRunner)
	pickCmd.SetArgs([]string{"--targets", targets})
	pickCmd.SetIn(cmd.InOrStdin())
	pickCmd.SetOut(cmd.OutOrStdout())
	pickCmd.SetErr(cmd.ErrOrStderr())
	return pickCmd.Execute()
}

func newInitCommand(ctx context.Context, opts *appOptions) *cobra.Command {
	return newInitCommandWithDeps(ctx, opts, library.GitClient{}, runBubbleteaPick, runBubbleteaPlaceholders)
}

func newInitCommandWithDeps(ctx context.Context, opts *appOptions, client library.Client, runner pickRunner, pRunner placeholderRunner) *cobra.Command {
	var libraryName string
	var targets string
	var yes bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize graft in a project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkInitLock(opts.root, yes); err != nil {
				return err
			}
			if _, err := (lock.FileStore{}).Load(opts.root); err != nil {
				return err
			}
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			lk, err := buildInitLock(cfg, libraryName)
			if err != nil {
				return err
			}
			stagePaths, err := createInitTargets(opts.root, parseTargets(targets))
			if err != nil {
				return err
			}
			if err := (lock.FileStore{}).Save(opts.root, lk); err != nil {
				return err
			}
			if err := runPickAfterInit(ctx, cmd, opts, lk, targets, client, runner, pRunner); err != nil {
				return err
			}
			if err := stageInitFiles(opts.root, stagePaths); err != nil {
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
	return newLibraryCommandWithDeps(ctx, opts, library.GitClient{})
}

func newLibraryCommandWithDeps(ctx context.Context, opts *appOptions, client library.Client) *cobra.Command {
	cmd := &cobra.Command{Use: "library", Short: "Manage MCP libraries", Args: cobra.NoArgs}
	cmd.AddCommand(
		newLibraryAddCommand(ctx, opts, client),
		newLibraryListCommand(opts),
		newLibraryPullCommand(ctx, opts, client),
		newLibraryShowCommand(opts, client),
		newLibraryMigrateFromClaudeCommand(ctx, opts),
	)
	return cmd
}

func newLibraryAddCommand(ctx context.Context, opts *appOptions, client library.Client) *cobra.Command {
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
			if err := config.ValidateLibraryName(args[0]); err != nil {
				return err
			}
			if err := config.ValidateLibraryURL(args[1]); err != nil {
				return err
			}
			if _, ok := cfg.Library(args[0]); ok {
				return fmt.Errorf("library %q is already registered", args[0])
			}
			lib := config.Library{Name: args[0], URL: args[1], CachePath: cachePath}
			cfg, err = cfg.WithLibrary(lib)
			if err != nil {
				return err
			}
			lib, _ = cfg.Library(args[0])
			if err := client.Add(ctx, lib); err != nil {
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
				pulledAt := lib.LastPulledAt
				if pulledAt == "" {
					pulledAt = "-"
				}
				if err := printf(cmd, "%s\t%s\t%s\t%s%s\n", lib.Name, config.RedactLibraryURL(lib.URL), lib.CachePath, pulledAt, marker); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func newLibraryPullCommand(ctx context.Context, opts *appOptions, client library.Client) *cobra.Command {
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
			now := time.Now().UTC().Format(time.RFC3339)
			updated := cfg
			for _, lib := range libs {
				sha, err := client.Pull(ctx, lib)
				if err != nil {
					return err
				}
				lib.LastPulledAt = now
				updated, err = updated.WithLibrary(lib)
				if err != nil {
					return err
				}
				if err := saveConfig(opts.configPath, updated); err != nil {
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

func newLibraryShowCommand(opts *appOptions, client library.Client) *cobra.Command {
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
			if len(args) == 2 {
				def, _, err := client.Definition(lib, args[1])
				if err != nil {
					return err
				}
				return writeValue(cmd, true, def)
			}
			index, err := client.Index(lib)
			if err != nil {
				return err
			}
			filtered := filterLibraryIndex(index, tag)
			if jsonOutput {
				return writeValue(cmd, true, filtered)
			}
			for _, entry := range filtered.MCPs {
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
			return nil
		},
	}
	cmd.Flags().StringVar(&tag, "tag", "", "filter by tag")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "emit JSON (detail view always emits JSON)")
	return cmd
}

func filterLibraryIndex(index model.LibraryIndex, tag string) model.LibraryIndex {
	if tag == "" {
		return index
	}
	filtered := model.LibraryIndex{Name: index.Name, MCPs: []model.IndexEntry{}}
	for _, entry := range index.MCPs {
		if contains(entry.Tags, tag) {
			filtered.MCPs = append(filtered.MCPs, entry)
		}
	}
	return filtered
}

func resolveSourcePath(from string) (string, error) {
	if from != "" {
		return from, nil
	}
	return claudecfg.DefaultPath()
}

func writeApprovedMCPs(cmd *cobra.Command, lib config.Library, mcps []claudecfg.MCP) error {
	for _, mcp := range mcps {
		if _, err := library.WriteDefinition(lib, mcp.Definition); err != nil {
			return err
		}
		if err := printf(cmd, "imported %s\n", mcp.Name); err != nil {
			return err
		}
	}
	return nil
}

func runClaudeMigration(ctx context.Context, cmd *cobra.Command, opts *appOptions, lib config.Library, force bool, groups []claudecfg.Group, cfg config.Config) error {
	client := library.GitClient{}
	if err := client.InitLocal(ctx, lib, force); err != nil {
		return err
	}
	approved, err := approveClaudeMCPs(cmd, groups)
	if err != nil {
		return err
	}
	if err := writeApprovedMCPs(cmd, lib, approved); err != nil {
		return err
	}
	if _, err := client.Reindex(lib); err != nil {
		return err
	}
	if err := client.CommitAll(ctx, lib.CachePath, "Initial import from ~/.claude.json"); err != nil {
		return err
	}
	return saveConfig(opts.configPath, cfg)
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
			source, err := resolveSourcePath(from)
			if err != nil {
				return err
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
			if err := runClaudeMigration(ctx, cmd, opts, lib, force, groups, cfg); err != nil {
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

func ensureLibraryCachePath(cfg config.Config, lib config.Library) (config.Config, config.Library, error) {
	if lib.CachePath != "" {
		return cfg, lib, nil
	}
	next, err := cfg.WithLibrary(lib)
	if err != nil {
		return cfg, lib, err
	}
	updated, _ := next.Library(lib.Name)
	return next, updated, nil
}

func ensureLibraryURL(cfg config.Config, lib config.Library) (config.Config, config.Library, error) {
	if lib.URL != "" {
		return cfg, lib, nil
	}
	lib.URL = lib.CachePath
	next, err := cfg.WithLibrary(lib)
	if err != nil {
		return cfg, lib, err
	}
	updated, _ := next.Library(lib.Name)
	return next, updated, nil
}

func prepareLocalLibraryConfig(cfg config.Config, name string) (config.Config, config.Library, error) {
	if err := library.ValidateMCPName(name); err != nil {
		return cfg, config.Library{}, err
	}
	if existing, ok := cfg.Library(name); ok {
		var err error
		cfg, existing, err = ensureLibraryCachePath(cfg, existing)
		if err != nil {
			return cfg, config.Library{}, err
		}
		cfg, existing, err = ensureLibraryURL(cfg, existing)
		if err != nil {
			return cfg, config.Library{}, err
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

func approveMCPsFromGroup(cmd *cobra.Command, reader *bufio.Reader, group claudecfg.Group, seen map[string]string) ([]claudecfg.MCP, error) {
	approved := []claudecfg.MCP{}
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
	return approved, nil
}

func approveClaudeMCPs(cmd *cobra.Command, groups []claudecfg.Group) ([]claudecfg.MCP, error) {
	approved := []claudecfg.MCP{}
	seen := map[string]string{}
	reader := bufio.NewReader(cmd.InOrStdin())
	for _, group := range groups {
		groupApproved, err := approveMCPsFromGroup(cmd, reader, group, seen)
		if err != nil {
			return nil, err
		}
		approved = append(approved, groupApproved...)
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

func newAddCommand(opts *appOptions) *cobra.Command {
	var fromFile string
	var force bool
	cmd := &cobra.Command{
		Use:   "add [name]",
		Short: "Add an MCP to the default library by pasting its JSON",
		Long: "Add an MCP definition to the default library from a full JSON snippet.\n\n" +
			"Paste the JSON on stdin (end with Ctrl-D) or pass --file. Both the wrapped\n" +
			"{\"mcpServers\":{...}} form and a bare single-server object are accepted; for a\n" +
			"bare object the name comes from a \"name\" field or the positional argument.\n\n" +
			"Literal secret-looking values are redacted into ${KEY} environment-variable\n" +
			"placeholders and reported; existing ${VAR} references are kept as-is.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			lib, ok := cfg.DefaultLibrary()
			if !ok {
				return fmt.Errorf("no default library configured")
			}
			data, err := readAddInput(cmd, fromFile)
			if err != nil {
				return err
			}
			name := ""
			if len(args) == 1 {
				name = args[0]
			}
			defs, err := library.ParseMCPJSON(data, name)
			if err != nil {
				return err
			}
			for _, def := range defs {
				if err := library.ValidateDefinition(def); err != nil {
					return err
				}
			}
			if !force {
				for _, def := range defs {
					if library.DefinitionExists(lib, def.Name) {
						return fmt.Errorf("MCP %q already exists; re-run with --force to overwrite or use 'graft mcp edit %s'", def.Name, def.Name)
					}
				}
			}
			for _, def := range defs {
				redacted := library.RedactSecrets(&def)
				if _, err := library.WriteDefinitionFile(lib, def, force); err != nil {
					if strings.Contains(err.Error(), "already exists") {
						return fmt.Errorf("MCP %q already exists; re-run with --force to overwrite or use 'graft mcp edit %s'", def.Name, def.Name)
					}
					return err
				}
				if err := printf(cmd, "added %s\n", def.Name); err != nil {
					return err
				}
				if len(redacted) > 0 {
					if err := printf(cmd, "redacted secret(s): %s — set these env vars before use\n", strings.Join(redacted, ", ")); err != nil {
						return err
					}
				}
			}
			_, err = (library.GitClient{}).Reindex(lib)
			return err
		},
	}
	cmd.Flags().StringVar(&fromFile, "file", "", "read MCP JSON from a file instead of stdin")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing MCP definition")
	return cmd
}

func readAddInput(cmd *cobra.Command, fromFile string) ([]byte, error) {
	if fromFile != "" {
		data, err := os.ReadFile(fromFile)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", fromFile, err)
		}
		return data, nil
	}
	if err := eprintf(cmd, "Paste MCP JSON, then press Ctrl-D:\n"); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(cmd.InOrStdin())
	if err != nil {
		return nil, fmt.Errorf("read pasted JSON: %w", err)
	}
	return data, nil
}

func newAddInteractiveCommand(opts *appOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "add-interactive",
		Short: "Add an MCP to the default library via an interactive wizard",
		Long: "Add an MCP definition to the default library by answering a series of\n" +
			"questions. The wizard asks for the name, description, version, transport\n" +
			"type, and the fields each transport needs (command/args for stdio, url/\n" +
			"headers for http and sse), then environment variables and tags.\n\n" +
			"Literal secret-looking values are redacted into ${KEY} environment-variable\n" +
			"placeholders and reported; existing ${VAR} references are kept as-is.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(opts.configPath)
			if err != nil {
				return err
			}
			lib, ok := cfg.DefaultLibrary()
			if !ok {
				return fmt.Errorf("no default library configured")
			}
			def, err := runAddWizard(cmd, lib)
			if err != nil {
				return err
			}
			redacted := library.RedactSecrets(&def)
			if _, err := library.WriteDefinitionFile(lib, def, true); err != nil {
				return err
			}
			if err := printf(cmd, "added %s\n", def.Name); err != nil {
				return err
			}
			if len(redacted) > 0 {
				if err := printf(cmd, "redacted secret(s): %s — set these env vars before use\n", strings.Join(redacted, ", ")); err != nil {
					return err
				}
			}
			_, err = (library.GitClient{}).Reindex(lib)
			return err
		},
	}
}

// runAddWizard walks the user through authoring an MCP definition field by
// field. It returns the validated definition ready to write; a returned error
// means the user aborted or input ended early.
func runAddWizard(cmd *cobra.Command, lib config.Library) (model.Definition, error) {
	reader := bufio.NewReader(cmd.InOrStdin())

	name, err := promptName(cmd, reader)
	if err != nil {
		return model.Definition{}, err
	}
	description, err := promptLine(cmd, reader, "Description: ")
	if err != nil {
		return model.Definition{}, err
	}
	version, err := promptLine(cmd, reader, "Version [0.1.0]: ")
	if err != nil {
		return model.Definition{}, err
	}
	if version == "" {
		version = "0.1.0"
	}
	transportType, err := promptType(cmd, reader)
	if err != nil {
		return model.Definition{}, err
	}

	def := model.Definition{
		Name:        name,
		Version:     version,
		Description: description,
		Type:        transportType,
		Env:         map[string]string{},
		Headers:     map[string]string{},
	}

	switch transportType {
	case "http", "sse":
		url, err := promptRequired(cmd, reader, "URL: ")
		if err != nil {
			return model.Definition{}, err
		}
		def.URL = url
		headers, err := promptKeyValues(cmd, reader, "Header (KEY=VALUE, blank to finish): ")
		if err != nil {
			return model.Definition{}, err
		}
		def.Headers = headers
	default:
		command, err := promptRequired(cmd, reader, "Command: ")
		if err != nil {
			return model.Definition{}, err
		}
		def.Command = command
		argsText, err := promptLine(cmd, reader, "Args: ")
		if err != nil {
			return model.Definition{}, err
		}
		def.Args = strings.Fields(argsText)
	}

	env, err := promptKeyValues(cmd, reader, "Env var (KEY=VALUE, blank to finish): ")
	if err != nil {
		return model.Definition{}, err
	}
	def.Env = env
	tagsText, err := promptLine(cmd, reader, "Tags (comma-separated): ")
	if err != nil {
		return model.Definition{}, err
	}
	def.Tags = splitCSV(tagsText)

	if err := library.ValidateDefinition(def); err != nil {
		return model.Definition{}, err
	}
	if library.DefinitionExists(lib, def.Name) {
		answer, err := promptLine(cmd, reader, fmt.Sprintf("MCP %q already exists; overwrite? [y/N] ", def.Name))
		if err != nil {
			return model.Definition{}, err
		}
		if strings.ToLower(answer) != "y" && strings.ToLower(answer) != "yes" {
			return model.Definition{}, fmt.Errorf("MCP %q already exists; not overwritten", def.Name)
		}
	}
	return def, nil
}

// promptName loops until the user supplies a non-empty, valid MCP name.
func promptName(cmd *cobra.Command, reader *bufio.Reader) (string, error) {
	for {
		name, err := promptLine(cmd, reader, "Name: ")
		if err != nil {
			return "", err
		}
		if name == "" {
			if err := eprintf(cmd, "name is required\n"); err != nil {
				return "", err
			}
			continue
		}
		if err := library.ValidateMCPName(name); err != nil {
			if err := eprintf(cmd, "%v\n", err); err != nil {
				return "", err
			}
			continue
		}
		return name, nil
	}
}

// promptType loops until the user supplies a recognized transport type (or a
// blank line, which defaults to stdio).
func promptType(cmd *cobra.Command, reader *bufio.Reader) (string, error) {
	for {
		t, err := promptLine(cmd, reader, "Type [stdio/http/sse]: ")
		if err != nil {
			return "", err
		}
		switch t {
		case "", "stdio", "http", "sse":
			return t, nil
		}
		if err := eprintf(cmd, "unknown type %q (want stdio, http, or sse)\n", t); err != nil {
			return "", err
		}
	}
}

// promptRequired loops until the user supplies a non-empty value.
func promptRequired(cmd *cobra.Command, reader *bufio.Reader, label string) (string, error) {
	for {
		value, err := promptLine(cmd, reader, label)
		if err != nil {
			return "", err
		}
		if value != "" {
			return value, nil
		}
		if err := eprintf(cmd, "%s is required\n", strings.TrimRight(label, ": ")); err != nil {
			return "", err
		}
	}
}

// promptKeyValues collects KEY=VALUE pairs until a blank line. Malformed lines
// (missing "=" or empty key) are reported and skipped.
func promptKeyValues(cmd *cobra.Command, reader *bufio.Reader, label string) (map[string]string, error) {
	out := map[string]string{}
	for {
		line, err := promptLine(cmd, reader, label)
		if err != nil {
			return nil, err
		}
		if line == "" {
			return out, nil
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			if err := eprintf(cmd, "expected KEY=VALUE\n"); err != nil {
				return nil, err
			}
			continue
		}
		out[key] = strings.TrimSpace(value)
	}
}

func importDefinition(cmd *cobra.Command, reader *bufio.Reader, lib config.Library, def model.Definition) (bool, error) {
	if hasAuthFields(def) {
		answer, err := promptLine(cmd, reader, fmt.Sprintf("Import auth placeholders for %s? [y/N] ", def.Name))
		if err != nil {
			return false, err
		}
		if strings.ToLower(answer) != "y" && strings.ToLower(answer) != "yes" {
			return false, printf(cmd, "skipped %s\n", def.Name)
		}
	}
	return writeImportedDefinition(cmd, reader, lib, def)
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
				written, err := importDefinition(cmd, reader, lib, def)
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

func resolveDefinitionConflict(cmd *cobra.Command, reader *bufio.Reader, lib config.Library, def model.Definition) (bool, error) {
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

func writeImportedDefinition(cmd *cobra.Command, reader *bufio.Reader, lib config.Library, def model.Definition) (bool, error) {
	if _, err := library.WriteDefinition(lib, def); err == nil {
		return true, nil
	} else if !strings.Contains(err.Error(), "already exists") {
		return false, err
	}
	return resolveDefinitionConflict(cmd, reader, lib, def)
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

type promptField struct {
	value *string
	label string
}

func promptMissingMCPFields(cmd *cobra.Command, reader *bufio.Reader, fields []promptField) error {
	for _, f := range fields {
		if *f.value == "" {
			val, err := promptLine(cmd, reader, f.label)
			if err != nil {
				return err
			}
			*f.value = val
		}
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
				fields := []promptField{
					{&description, "Description: "},
					{&version, "Version [0.1.0]: "},
					{&transportType, "Type [stdio/http/sse]: "},
					{&command, "Command: "},
					{&argsText, "Args: "},
					{&tagsText, "Tags: "},
				}
				if err := promptMissingMCPFields(cmd, reader, fields); err != nil {
					return err
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

func pullSyncLibraries(ctx context.Context, client library.Client, libs []config.Library, noPull bool) error {
	if noPull {
		return nil
	}
	for _, lib := range libs {
		if _, err := client.Pull(ctx, lib); err != nil {
			return err
		}
	}
	return nil
}

func buildActiveSyncAdapters(root string, override []render.AdapterByName) []render.AdapterByName {
	if override != nil {
		return override
	}
	return []render.AdapterByName{
		{Name: "claude", Adapter: render.NewClaudeAdapter(root)},
		{Name: "codex", Adapter: render.NewCodexAdapter(root)},
	}
}

func buildPinMismatchConfirmer(cmd *cobra.Command) func(string) (string, error) {
	return func(diff string) (string, error) {
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
	}
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
			if err := pullSyncLibraries(ctx, client, pullLibraries, noPull); err != nil {
				return err
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
			result := graftsync.ApplyWithOptions(lk, cfg, client, buildActiveSyncAdapters(opts.root, adapters), graftsync.Options{
				Names:              args,
				ForcePins:          forcePins,
				ConfirmPinMismatch: buildPinMismatchConfirmer(cmd),
			})
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
		if err := config.ValidateLibraryName(ref.Name); err != nil {
			return next, err
		}
		if err := config.ValidateLibraryURL(ref.URL); err != nil {
			return next, err
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

type placeholderRunner func(context.Context, tui.PlaceholderModel) (tui.PlaceholderModel, error)

func newPickCommand(ctx context.Context, opts *appOptions) *cobra.Command {
	return newPickCommandWithDeps(ctx, opts, library.GitClient{}, runBubbleteaPick, runBubbleteaPlaceholders)
}

func loadPickDeps(ctx context.Context, cmd *cobra.Command, opts *appOptions, targets string, client library.Client) (string, config.Config, lock.Lock, error) {
	target, err := normalizePickTarget(targets)
	if err != nil {
		return "", config.Config{}, lock.Lock{}, err
	}
	cfg, err := loadConfig(opts.configPath)
	if err != nil {
		return "", config.Config{}, lock.Lock{}, err
	}
	lk, err := (lock.FileStore{}).Load(opts.root)
	if err != nil {
		return "", config.Config{}, lock.Lock{}, err
	}
	cfg, err = ensureLockLibrariesRegistered(ctx, cmd, opts, cfg, lk, client)
	if err != nil {
		return "", config.Config{}, lock.Lock{}, err
	}
	return target, cfg, lk, nil
}

func buildPickPreSelections(lk lock.Lock) []string {
	preSelected := []string{}
	for _, mcp := range lk.MCPs {
		preSelected = append(preSelected, mcp.Library+"/"+mcp.Name)
	}
	return preSelected
}

func printPickWarnings(cmd *cobra.Command, warnings []string) error {
	for _, warning := range warnings {
		if err := eprintf(cmd, "warning: %s\n", warning); err != nil {
			return err
		}
	}
	return nil
}

func newPickCommandWithDeps(ctx context.Context, opts *appOptions, client library.Client, runner pickRunner, pRunner placeholderRunner) *cobra.Command {
	var targets string
	cmd := &cobra.Command{
		Use:   "pick",
		Short: "Select MCPs from registered libraries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			target, cfg, lk, err := loadPickDeps(ctx, cmd, opts, targets, client)
			if err != nil {
				return err
			}
			if len(cfg.Libraries) == 0 {
				return fmt.Errorf("no libraries configured")
			}
			lk = lockWithConfiguredLibraries(lk, cfg)
			pickList, err := buildPickList(cfg, client)
			if err != nil {
				return err
			}
			if err := printPickWarnings(cmd, pickList.Warnings); err != nil {
				return err
			}
			model, err := runner(ctx, tui.NewPickModel(pickList.Items, buildPickPreSelections(lk)))
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
			return savePickResultWithSideEffects(ctx, opts.root, cfg, client, lk, next, pRunner)
		},
	}
	cmd.Flags().StringVar(&targets, "targets", "both", "claude, codex, or both")
	return cmd
}

// NewPickCommandForTest exposes the dependency-injected pick command to black-box
// BDD tests in the features package.
func NewPickCommandForTest(ctx context.Context, configPath, root string, client library.Client, runner func(context.Context, tui.PickModel) (tui.PickModel, error)) *cobra.Command {
	return newPickCommandWithDeps(ctx, &appOptions{configPath: configPath, root: root}, client, runner, runBubbleteaPlaceholders)
}

// NewPickCommandForTestWithPlaceholders is like NewPickCommandForTest but also
// injects the placeholder-resolution prompt runner.
func NewPickCommandForTestWithPlaceholders(ctx context.Context, configPath, root string, client library.Client, runner func(context.Context, tui.PickModel) (tui.PickModel, error), pRunner func(context.Context, tui.PlaceholderModel) (tui.PlaceholderModel, error)) *cobra.Command {
	return newPickCommandWithDeps(ctx, &appOptions{configPath: configPath, root: root}, client, runner, pRunner)
}

// NewInitCommandForTest exposes the dependency-injected init command to BDD
// tests that need to avoid a real terminal picker or git-backed library cache.
func NewInitCommandForTest(ctx context.Context, configPath, root string, client library.Client, runner func(context.Context, tui.PickModel) (tui.PickModel, error)) *cobra.Command {
	return newInitCommandWithDeps(ctx, &appOptions{configPath: configPath, root: root}, client, runner, runBubbleteaPlaceholders)
}

// NewLibraryCommandForTest exposes the dependency-injected library command to
// BDD tests that need deterministic git-backed library behavior.
func NewLibraryCommandForTest(ctx context.Context, configPath, root string, client library.Client) *cobra.Command {
	return newLibraryCommandWithDeps(ctx, &appOptions{configPath: configPath, root: root}, client)
}

// NewStatusCommandForTest exposes the dependency-injected status command to
// BDD tests that need to avoid real git-backed library caches.
func NewStatusCommandForTest(configPath, root string, client library.Client) *cobra.Command {
	return newStatusCommandWithDeps(&appOptions{configPath: configPath, root: root}, client)
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

func runBubbleteaPlaceholders(ctx context.Context, model tui.PlaceholderModel) (tui.PlaceholderModel, error) {
	final, err := tea.NewProgram(model, tea.WithContext(ctx)).Run()
	if err != nil {
		return tui.PlaceholderModel{}, err
	}
	prompt, ok := final.(tui.PlaceholderModel)
	if !ok {
		return tui.PlaceholderModel{}, fmt.Errorf("placeholder prompt returned unexpected model %T", final)
	}
	return prompt, nil
}

func lockWithConfiguredLibraries(lk lock.Lock, cfg config.Config) lock.Lock {
	next := lk
	seen := map[string]bool{}
	for _, lib := range next.Libraries {
		seen[lib.Name] = true
	}
	for _, lib := range cfg.Libraries {
		if seen[lib.Name] {
			continue
		}
		next.Libraries = append(next.Libraries, lock.LibraryRef{Name: lib.Name, URL: lib.URL})
		seen[lib.Name] = true
	}
	return next
}

func buildMCPMap(mcps []lock.InstalledMCP) map[string]lock.InstalledMCP {
	m := map[string]lock.InstalledMCP{}
	for _, mcp := range mcps {
		m[pickLockKey(mcp.Library, mcp.Name)] = mcp
	}
	return m
}

func removeStalePickTargets(adapters []render.AdapterByName, oldMCPs, nextMCPs map[string]lock.InstalledMCP) error {
	for key, oldMCP := range oldMCPs {
		nextMCP, ok := nextMCPs[key]
		if !ok || oldMCP.Target != nextMCP.Target || oldMCP.DefinitionHash != nextMCP.DefinitionHash {
			if err := removePickTargets(adapters, oldMCP); err != nil {
				return err
			}
		}
	}
	return nil
}

func savePickResultWithSideEffects(ctx context.Context, root string, cfg config.Config, client library.Client, old lock.Lock, next lock.Lock, pRunner placeholderRunner) error {
	adapters := []render.AdapterByName{
		{Name: "claude", Adapter: render.NewClaudeAdapter(root)},
		{Name: "codex", Adapter: render.NewCodexAdapter(root)},
	}
	snapshots, err := snapshotPickAdapters(adapters)
	if err != nil {
		return err
	}
	if err := removeStalePickTargets(adapters, buildMCPMap(old.MCPs), buildMCPMap(next.MCPs)); err != nil {
		return rollbackPickSideEffects(snapshots, err)
	}
	resultLock := next
	if syncNames := pickSyncNames(old, next); len(syncNames) > 0 {
		items, prefetched, err := collectPlaceholderItems(cfg, client, old, next)
		if err != nil {
			return rollbackPickSideEffects(snapshots, err)
		}
		overrides := map[string]model.PlaceholderOverrides{}
		if len(items) > 0 {
			if pRunner == nil {
				return rollbackPickSideEffects(snapshots, fmt.Errorf("placeholder prompt runner is not configured"))
			}
			prompt, err := pRunner(ctx, tui.NewPlaceholderModel(items))
			if err != nil {
				return rollbackPickSideEffects(snapshots, err)
			}
			if !prompt.Confirmed {
				// Cancel is a clean no-op: restore touched targets, write nothing.
				return restorePickSnapshots(snapshots)
			}
			overrides = prompt.Results()
		}
		result := graftsync.ApplyWithOptions(preparePickSyncLock(old, next), cfg, client, adapters, graftsync.Options{Names: syncNames, Placeholders: overrides, Prefetched: prefetched})
		if err := pickSyncError(result); err != nil {
			return rollbackPickSideEffects(snapshots, err)
		}
		resultLock = result.Lock
	}
	if err := (lock.FileStore{}).Save(root, resultLock); err != nil {
		return rollbackPickSideEffects(snapshots, err)
	}
	return nil
}

// collectPlaceholderItems returns, for MCPs new to the project, the ordered and
// deduped list of ${...} placeholder tokens (env and headers across each
// requested target) that need a reference name from the user. It also returns
// the definitions it fetched, keyed by "library/name", so the subsequent sync
// can reuse them instead of fetching again.
func collectPlaceholderItems(cfg config.Config, client library.Client, old lock.Lock, next lock.Lock) ([]tui.PlaceholderItem, map[string]graftsync.DefinitionResult, error) {
	oldKeys := map[string]bool{}
	for _, mcp := range old.MCPs {
		oldKeys[pickLockKey(mcp.Library, mcp.Name)] = true
	}
	items := []tui.PlaceholderItem{}
	prefetched := map[string]graftsync.DefinitionResult{}
	seen := map[string]bool{}
	for _, mcp := range next.MCPs {
		if oldKeys[pickLockKey(mcp.Library, mcp.Name)] {
			continue
		}
		lib, ok := cfg.Library(mcp.Library)
		if !ok {
			continue
		}
		def, hash, err := client.Definition(lib, mcp.Name)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: definition: %w", mcp.Name, err)
		}
		prefetched[pickLockKey(mcp.Library, mcp.Name)] = graftsync.DefinitionResult{Definition: def, Hash: hash}
		for _, target := range parseTargets(mcp.Target) {
			adapter := def.Adapter(target)
			items = appendPlaceholderItems(items, seen, mcp.Name, "env", adapter.Env)
			items = appendPlaceholderItems(items, seen, mcp.Name, "header", adapter.Headers)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].MCP != items[j].MCP {
			return items[i].MCP < items[j].MCP
		}
		if items[i].Scope != items[j].Scope {
			return items[i].Scope < items[j].Scope // "env" before "header"
		}
		return items[i].Key < items[j].Key
	})
	return items, prefetched, nil
}

func appendPlaceholderItems(items []tui.PlaceholderItem, seen map[string]bool, mcp, scope string, values map[string]string) []tui.PlaceholderItem {
	for key, value := range values {
		if !library.IsPlaceholder(value) {
			continue
		}
		dedupeKey := mcp + "\x00" + scope + "\x00" + key
		if seen[dedupeKey] {
			continue
		}
		seen[dedupeKey] = true
		items = append(items, tui.PlaceholderItem{MCP: mcp, Scope: scope, Key: key, DefaultVar: library.PlaceholderName(value)})
	}
	return items
}

// restorePickSnapshots restores adapter targets without surfacing an error,
// used when the user cancels the placeholder prompt.
func restorePickSnapshots(snapshots []pickAdapterSnapshot) error {
	var errs []error
	for idx := len(snapshots) - 1; idx >= 0; idx-- {
		if err := snapshots[idx].adapter.Restore(snapshots[idx].snapshot); err != nil {
			errs = append(errs, fmt.Errorf("restore %s target: %w", snapshots[idx].name, err))
		}
	}
	return errors.Join(errs...)
}

func pickSyncNames(old lock.Lock, next lock.Lock) []string {
	oldMCPs := map[string]lock.InstalledMCP{}
	for _, mcp := range old.MCPs {
		oldMCPs[pickLockKey(mcp.Library, mcp.Name)] = mcp
	}
	names := []string{}
	for _, mcp := range next.MCPs {
		oldMCP, ok := oldMCPs[pickLockKey(mcp.Library, mcp.Name)]
		if !ok || oldMCP.Target != mcp.Target || oldMCP.DefinitionHash != mcp.DefinitionHash {
			names = append(names, mcp.Name)
		}
	}
	return names
}

func preparePickSyncLock(old lock.Lock, next lock.Lock) lock.Lock {
	oldMCPs := map[string]lock.InstalledMCP{}
	for _, mcp := range old.MCPs {
		oldMCPs[pickLockKey(mcp.Library, mcp.Name)] = mcp
	}
	prepared := next
	for idx, mcp := range prepared.MCPs {
		oldMCP, ok := oldMCPs[pickLockKey(mcp.Library, mcp.Name)]
		if !ok {
			prepared.MCPs[idx].Version = ""
			prepared.MCPs[idx].DefinitionHash = ""
			continue
		}
		prepared.MCPs[idx].Version = oldMCP.Version
		prepared.MCPs[idx].DefinitionHash = oldMCP.DefinitionHash
		if oldMCP.Target != mcp.Target || oldMCP.DefinitionHash != mcp.DefinitionHash {
			prepared.MCPs[idx].DefinitionHash = ""
		}
	}
	return prepared
}

func pickSyncError(result graftsync.Result) error {
	if len(result.Errors) > 0 {
		return errors.New(strings.Join(result.Errors, "; "))
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "auth warning") {
			return errors.New(warning)
		}
	}
	return nil
}

func snapshotPickAdapters(adapters []render.AdapterByName) ([]pickAdapterSnapshot, error) {
	snapshots := []pickAdapterSnapshot{}
	for _, adapter := range adapters {
		restorable, ok := adapter.Adapter.(pickRestorableAdapter)
		if !ok {
			return nil, fmt.Errorf("adapter %q does not support rollback", adapter.Name)
		}
		snapshot, err := restorable.Snapshot()
		if err != nil {
			return nil, fmt.Errorf("snapshot %s target: %w", adapter.Name, err)
		}
		snapshots = append(snapshots, pickAdapterSnapshot{name: adapter.Name, adapter: restorable, snapshot: snapshot})
	}
	return snapshots, nil
}

func rollbackPickSideEffects(snapshots []pickAdapterSnapshot, cause error) error {
	var rollbackErrs []error
	for idx := len(snapshots) - 1; idx >= 0; idx-- {
		if err := snapshots[idx].adapter.Restore(snapshots[idx].snapshot); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("restore %s target: %w", snapshots[idx].name, err))
		}
	}
	if rollbackErr := errors.Join(rollbackErrs...); rollbackErr != nil {
		return fmt.Errorf("pick side effects: %w; rollback: %w", cause, rollbackErr)
	}
	return cause
}

type pickRestorableAdapter interface {
	Snapshot() (any, error)
	Restore(any) error
}

type pickAdapterSnapshot struct {
	name     string
	adapter  pickRestorableAdapter
	snapshot any
}

func pickLockKey(libraryName, mcpName string) string {
	return libraryName + "/" + mcpName
}

func removePickTargets(adapters []render.AdapterByName, mcp lock.InstalledMCP) error {
	for _, target := range parseTargets(mcp.Target) {
		adapter, ok := pickAdapter(adapters, target)
		if !ok {
			return fmt.Errorf("no render adapter for target %q", target)
		}
		if err := adapter.Remove(mcp.Name); err != nil {
			return err
		}
	}
	return nil
}

func pickAdapter(adapters []render.AdapterByName, name string) (render.Adapter, bool) {
	for _, adapter := range adapters {
		if adapter.Name == name {
			return adapter.Adapter, true
		}
	}
	return nil, false
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
	oldMCPs := map[string]lock.InstalledMCP{}
	for _, mcp := range lk.MCPs {
		oldMCPs[pickLockKey(mcp.Library, mcp.Name)] = mcp
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
		selected := lock.InstalledMCP{
			Name:           result.Entry.Name,
			Library:        result.Library,
			Version:        result.Entry.Version,
			DefinitionHash: result.Entry.SHA256,
			Target:         target,
		}
		if oldMCP, ok := oldMCPs[pickLockKey(result.Library, result.Entry.Name)]; ok && oldMCP.Target == target && oldMCP.DefinitionHash == result.Entry.SHA256 {
			selected = oldMCP
		}
		next.MCPs = append(next.MCPs, selected)
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

func stageInitFiles(root string, paths []string) error {
	if _, statErr := os.Stat(filepath.Join(root, ".git")); os.IsNotExist(statErr) {
		return nil
	} else if statErr != nil {
		return fmt.Errorf("stat git directory: %w", statErr)
	}
	args := append([]string{"-C", root, "add", "--"}, paths...)
	if err := exec.Command("git", args...).Run(); err != nil {
		return fmt.Errorf("stage init files: %w", err)
	}
	return nil
}

func createTarget(root, target string) (string, bool, error) {
	switch target {
	case "claude":
		path := filepath.Join(root, ".mcp.json")
		if _, err := os.Stat(path); err == nil {
			return ".mcp.json", false, nil
		} else if !os.IsNotExist(err) {
			return "", false, fmt.Errorf("stat claude target: %w", err)
		}
		return ".mcp.json", true, fileutil.AtomicWriteFile(path, []byte("{\n  \"mcpServers\": {}\n}\n"), 0o600)
	case "codex":
		path := filepath.Join(root, ".codex", "config.toml")
		if _, err := os.Stat(path); err == nil {
			return ".codex/config.toml", false, nil
		} else if !os.IsNotExist(err) {
			return "", false, fmt.Errorf("stat codex target: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", false, err
		}
		return ".codex/config.toml", true, fileutil.AtomicWriteFile(path, []byte("[mcp_servers]\n"), 0o600)
	default:
		return "", false, fmt.Errorf("unknown target %q", target)
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
