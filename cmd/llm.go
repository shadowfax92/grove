package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"grove/internal/catalog"
	"grove/internal/config"
	"grove/internal/llmstore"

	"github.com/spf13/cobra"
)

// llmRuntime is the configured store plus the archive policy grove applies
// around it. The store itself only archives when asked.
type llmRuntime struct {
	store        *llmstore.Store
	archiveAfter time.Duration
}

type llmLinkOutput struct {
	Version    int           `json:"version"`
	Repository string        `json:"repository"`
	Worktree   string        `json:"worktree"`
	Link       llmstore.Link `json:"link"`
}

type llmSyncOutput struct {
	Version int           `json:"version"`
	DryRun  bool          `json:"dry_run"`
	Links   []llmSyncLink `json:"links"`
}

type llmSyncLink struct {
	Repository string        `json:"repository"`
	Worktree   string        `json:"worktree"`
	Link       llmstore.Link `json:"link"`
	Error      string        `json:"error,omitempty"`
}

type llmArchiveOutput struct {
	Version int               `json:"version"`
	DryRun  bool              `json:"dry_run"`
	Cutoff  time.Time         `json:"cutoff"`
	Stores  []llmArchiveStore `json:"stores"`
}

type llmArchiveStore struct {
	Store    llmstore.Target     `json:"store"`
	Archived []llmstore.Archived `json:"archived"`
}

func (a *application) llmCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "llm",
		Short: "Link this worktree's llm store and print its path",
		Long: `Link this worktree's llm store and print its path.

Each repository keeps one store under llm.root, linked into every worktree as .llm
and as each name in llm.aliases. Set llm: <key> on config rows to make separate
clones share a store, or llm: false to opt a repository out.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runLLM(cmd)
		},
	}
	command.AddCommand(a.llmSyncCommand(), a.llmArchiveCommand())
	return command
}

func (a *application) llmSyncCommand() *cobra.Command {
	var dryRun bool
	command := &cobra.Command{
		Use:   "sync",
		Short: "Link every worktree of every repository to its llm store",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runLLMSync(cmd, dryRun)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would change")
	return command
}

func (a *application) llmArchiveCommand() *cobra.Command {
	var dryRun bool
	var olderThan string
	command := &cobra.Command{
		Use:   "archive [repository]",
		Short: "Move idle llm store entries into the store's archive/",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runLLMArchive(cmd, args, olderThan, dryRun)
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would move")
	command.Flags().StringVar(&olderThan, "older-than", "", "Idle age such as 30d or 4w (default: llm.archive_after)")
	return command
}

func (a *application) runLLM(cmd *cobra.Command) error {
	context, err := a.loadContext(cmd)
	if err != nil {
		return err
	}
	runtime, err := requireLLM(context.config)
	if err != nil {
		return err
	}
	entry, err := context.inventory.Resolve(".", context.directory)
	if err != nil {
		return err
	}
	link, err := runtime.store.Link(llmRepo(entry.Repository), entry.Worktree.Path, false)
	// A blocked anchor still leaves a usable store, so callers like gv llm can
	// cd there; anything else means the store itself is unavailable.
	if errors.Is(err, llmstore.ErrBlocked) {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: llm: %v\n", err)
	} else if err != nil {
		return err
	}
	reportRepointed(cmd, link)
	if a.jsonOutput {
		return writeJSON(cmd, llmLinkOutput{Version: 1, Repository: entry.Repository.Name, Worktree: entry.Worktree.Path, Link: link})
	}
	return a.writePath(cmd, link.Store.Path)
}

func (a *application) runLLMSync(cmd *cobra.Command, dryRun bool) error {
	context, err := a.loadContext(cmd)
	if err != nil {
		return err
	}
	runtime, err := requireLLM(context.config)
	if err != nil {
		return err
	}
	document := llmSyncOutput{Version: 1, DryRun: dryRun, Links: []llmSyncLink{}}
	failed := 0
	for _, entry := range context.inventory.Entries {
		if entry.Worktree.Prunable || entry.Worktree.Bare {
			continue
		}
		link, err := runtime.store.Link(llmRepo(entry.Repository), entry.Worktree.Path, dryRun)
		if errors.Is(err, llmstore.ErrDisabled) {
			continue
		}
		item := llmSyncLink{Repository: entry.Repository.Name, Worktree: entry.Worktree.Path, Link: link}
		if err != nil {
			// errors.Join separates per-anchor failures with newlines.
			item.Error = strings.ReplaceAll(err.Error(), "\n", "; ")
			failed++
		}
		document.Links = append(document.Links, item)
	}
	if a.jsonOutput {
		if err := writeJSON(cmd, document); err != nil {
			return err
		}
	} else {
		writeSyncSummary(cmd, a.style(cmd.OutOrStdout()), document, context)
	}
	if failed > 0 {
		return fmt.Errorf("%d worktree(s) could not be linked", failed)
	}
	return nil
}

func writeSyncSummary(cmd *cobra.Command, style outputStyle, document llmSyncOutput, context *commandContext) {
	out := cmd.OutOrStdout()
	selectors := make(map[string]string, len(context.inventory.Entries))
	for _, entry := range context.inventory.Entries {
		selectors[entry.Worktree.Path] = entry.Selector()
	}
	unchanged := 0
	for _, item := range document.Links {
		label := selectors[item.Worktree]
		changed := false
		for _, anchor := range item.Link.Anchors {
			if anchor.Action == llmstore.Linked || anchor.Action == llmstore.Blocked {
				continue
			}
			changed = true
			action := string(anchor.Action)
			if document.DryRun {
				action = "would be " + action
			}
			detail := ""
			if anchor.Previous != "" {
				detail = style.muted("  (was " + anchor.Previous + ")")
			}
			fmt.Fprintf(out, "%s  %s %s → %s%s\n", style.info(action), style.branch(label), filepath.Base(anchor.Path), item.Link.Store.Path, detail)
		}
		if item.Error != "" {
			fmt.Fprintf(out, "%s  %s  %s\n", style.danger("failed"), style.branch(label), item.Error)
		} else if !changed {
			unchanged++
		}
	}
	fmt.Fprintln(out, style.muted(fmt.Sprintf("%d already linked", unchanged)))
}

func (a *application) runLLMArchive(cmd *cobra.Command, args []string, olderThanValue string, dryRun bool) error {
	context, err := a.loadContext(cmd)
	if err != nil {
		return err
	}
	runtime, err := requireLLM(context.config)
	if err != nil {
		return err
	}
	olderThan := runtime.archiveAfter
	if olderThanValue != "" {
		age, err := parseOlderThan(olderThanValue)
		if err != nil {
			return err
		}
		olderThan = age.duration
	}
	if olderThan == 0 {
		return fmt.Errorf("archiving is off; pass --older-than or set llm.archive_after")
	}
	repositories := context.catalog.Repositories
	if len(args) == 1 {
		repository, _, err := context.catalog.FindRepository(args[0])
		if err != nil {
			return err
		}
		repositories = []*catalog.Repository{repository}
	}

	cutoff := time.Now().Add(-olderThan)
	document := llmArchiveOutput{Version: 1, DryRun: dryRun, Cutoff: cutoff, Stores: []llmArchiveStore{}}
	// Linked clones share one store; archive each store once.
	seen := make(map[string]bool)
	for _, repository := range repositories {
		target, err := runtime.store.Target(llmRepo(repository))
		if errors.Is(err, llmstore.ErrDisabled) {
			continue
		}
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: repo %s: %v\n", repository.Name, err)
			continue
		}
		if seen[target.Path] {
			continue
		}
		seen[target.Path] = true
		archived, err := runtime.store.Archive(target, cutoff, dryRun)
		if len(archived) > 0 {
			document.Stores = append(document.Stores, llmArchiveStore{Store: target, Archived: archived})
		}
		if err != nil {
			return err
		}
	}
	if a.jsonOutput {
		return writeJSON(cmd, document)
	}
	style := a.style(cmd.OutOrStdout())
	if len(document.Stores) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "Nothing idle for "+relativeAge(olderThan)+".")
		return nil
	}
	now := time.Now()
	for index, store := range document.Stores {
		if index > 0 {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		fmt.Fprintln(cmd.OutOrStdout(), style.heading(store.Store.Path))
		for _, item := range store.Archived {
			verb := "archived"
			if dryRun {
				verb = "would archive"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  %s %s %s\n", style.info(verb), item.Name, style.muted("idle "+relativeAge(now.Sub(item.LastChanged))))
		}
	}
	return nil
}

// attachLLM links a new or reused worktree, and its main checkout, to the
// repository's store before setup runs, then archives idle entries. Store
// problems only warn: a worktree without .llm is still a usable worktree.
func (a *application) attachLLM(cmd *cobra.Command, cfg *config.Config, repository *catalog.Repository, worktree string) string {
	runtime, err := openLLM(cfg)
	if runtime == nil {
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: llm: %v\n", err)
		}
		return ""
	}
	repo := llmRepo(repository)
	paths := []string{worktree}
	if worktree != repository.Git.MainPath {
		paths = append(paths, repository.Git.MainPath)
	}
	var target llmstore.Target
	for _, path := range paths {
		link, err := runtime.store.Link(repo, path, false)
		if errors.Is(err, llmstore.ErrDisabled) {
			return ""
		}
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: llm: %v\n", err)
		}
		reportRepointed(cmd, link)
		// A blocked alias still leaves a usable store; only an unresolved
		// target (no store path) skips archiving.
		if link.Store.Path != "" {
			target = link.Store
		}
	}
	if target.Path == "" || runtime.archiveAfter == 0 {
		return target.Path
	}
	archived, err := runtime.store.Archive(target, time.Now().Add(-runtime.archiveAfter), false)
	if len(archived) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "llm: archived %d idle entr%s into %s/archive\n", len(archived), plural(len(archived), "y", "ies"), target.Path)
	}
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: llm archive: %v\n", err)
	}
	return target.Path
}

func reportRepointed(cmd *cobra.Command, link llmstore.Link) {
	for _, anchor := range link.Anchors {
		if anchor.Action == llmstore.Repointed {
			fmt.Fprintf(cmd.ErrOrStderr(), "llm: repointed %s → %s (was %s)\n", anchor.Path, link.Store.Path, anchor.Previous)
		}
	}
}

func requireLLM(cfg *config.Config) (*llmRuntime, error) {
	runtime, err := openLLM(cfg)
	if err != nil {
		return nil, err
	}
	if runtime == nil {
		return nil, fmt.Errorf("llm stores are off; set llm.root in the grove config")
	}
	return runtime, nil
}

// openLLM returns nil without error when llm.root is unset, which keeps the
// feature opt-in. Config values are parsed here, not at load, so a bad llm block
// fails only llm work instead of every grove command.
func openLLM(cfg *config.Config) (*llmRuntime, error) {
	if cfg == nil || cfg.LLM.Root == "" {
		return nil, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	store, err := llmstore.Open(llmstore.Settings{Root: cfg.LLM.Root, Dir: cfg.LLM.Dir, Aliases: cfg.LLM.Aliases, Keep: cfg.LLM.Keep}, home)
	if err != nil {
		return nil, err
	}
	archiveAfter := llmstore.DefaultArchiveAfter
	switch value := strings.TrimSpace(cfg.LLM.ArchiveAfter); value {
	case "":
	case "0", "off", "never":
		archiveAfter = 0
	default:
		age, err := parseOlderThan(value)
		if err != nil {
			return nil, fmt.Errorf("invalid llm.archive_after %q; use a duration such as 30d or 4w, or 0 to turn archiving off", value)
		}
		archiveAfter = age.duration
	}
	return &llmRuntime{store: store, archiveAfter: archiveAfter}, nil
}

func llmRepo(repository *catalog.Repository) llmstore.Repo {
	return llmstore.Repo{Git: repository.Git, Key: repository.LLM.Key, Disabled: repository.LLM.Disabled}
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}
