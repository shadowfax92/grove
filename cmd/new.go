package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"grove/internal/catalog"
	"grove/internal/config"
	gitx "grove/internal/git"
	"grove/internal/names"

	"github.com/spf13/cobra"
)

type newOutput struct {
	Version    int    `json:"version"`
	Repository string `json:"repository"`
	Branch     string `json:"branch"`
	Path       string `json:"path"`
	Created    bool   `json:"created"`
}

func (a *application) newCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "new [branch]",
		Short: "Create or find a worktree",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runNew(cmd, args)
		},
	}
}

func (a *application) runNew(cmd *cobra.Command, args []string) error {
	context, err := a.loadContext(cmd)
	if err != nil {
		return err
	}
	raw := ""
	if len(args) == 1 {
		raw = args[0]
	}
	repository, profile, branch, err := resolveNewTarget(context.catalog, raw)
	if err != nil {
		return err
	}
	if branch == "" {
		branch = generateBranch(repository)
	} else if !strings.Contains(branch, "/") {
		branch = "feat/" + branch
	}
	if context.catalog.Current == repository && !context.catalog.CurrentRegistered {
		if err := registerRepository(context.catalog, repository); err != nil {
			return fmt.Errorf("registering repository: %w", err)
		}
		context.catalog.CurrentRegistered = true
	}

	startPoint := ""
	if !repository.Git.RefExists("refs/heads/"+branch) && !repository.Git.RefExists("refs/remotes/origin/"+branch) {
		startPoint, err = repository.Git.BaseRef(repository.DefaultBranch)
		if err != nil {
			return err
		}
	}
	path, created, err := repository.Git.CreateWorktree(branch, startPoint)
	if err != nil {
		return fmt.Errorf("creating worktree: %w", err)
	}
	if created {
		runSetup(cmd, path, profile)
	}
	if a.jsonOutput {
		return writeJSON(cmd, newOutput{Version: 1, Repository: repository.Name, Branch: branch, Path: path, Created: created})
	}
	return a.writePath(cmd, path)
}

func resolveNewTarget(cat *catalog.Catalog, raw string) (*catalog.Repository, *catalog.Profile, string, error) {
	if strings.Contains(raw, ":") {
		parts := strings.SplitN(raw, ":", 2)
		if parts[0] == "" {
			return nil, nil, "", fmt.Errorf("repository name is required before ':'")
		}
		repository, profile, err := cat.FindRepository(parts[0])
		return repository, profile, parts[1], err
	}
	if cat.Current == nil {
		return nil, nil, "", fmt.Errorf("not inside a Git repository; use repo:branch")
	}
	return cat.Current, cat.Current.DefaultProfile(), raw, nil
}

func generateBranch(repository *catalog.Repository) string {
	existing := make([]string, 0)
	if worktrees, err := repository.Git.Worktrees(); err == nil {
		for _, worktree := range worktrees {
			if worktree.Branch != "" {
				existing = append(existing, worktree.Branch)
			}
		}
	}
	for {
		branch := names.GenerateBranch(existing)
		if !repository.Git.RefExists("refs/heads/"+branch) && !repository.Git.RefExists("refs/remotes/origin/"+branch) {
			return branch
		}
		existing = append(existing, branch)
	}
}

func registerRepository(cat *catalog.Catalog, repository *catalog.Repository) error {
	path, err := config.DefaultConfigPath()
	if err != nil {
		return err
	}
	name := cat.UniqueName(filepath.Base(repository.Git.MainPath))
	defaultBranch := repository.DefaultBranch
	if defaultBranch == "" {
		defaultBranch = gitx.DefaultBranch(repository.Git.MainPath)
	}
	return config.AddRepoToFile(path, config.NewWorktreeRepo(repository.Git.MainPath, name, defaultBranch))
}

func runSetup(cmd *cobra.Command, worktreePath string, profile *catalog.Profile) {
	if profile == nil || len(profile.Setup) == 0 {
		return
	}
	directory, err := setupDirectory(worktreePath, profile.Workdir)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: setup skipped: %v\n", err)
		return
	}
	for _, instruction := range profile.Setup {
		fmt.Fprintf(cmd.ErrOrStderr(), "setup: %s\n", instruction)
		process := exec.Command("sh", "-c", instruction)
		process.Dir = directory
		process.Stdout = cmd.ErrOrStderr()
		process.Stderr = cmd.ErrOrStderr()
		process.Stdin = os.Stdin
		if err := process.Run(); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: setup command failed: %v\n", err)
		}
	}
}

func setupDirectory(worktreePath, workdir string) (string, error) {
	root, err := filepath.EvalSymlinks(worktreePath)
	if err != nil {
		return "", fmt.Errorf("resolving worktree: %w", err)
	}
	if workdir == "" {
		return root, nil
	}
	if filepath.IsAbs(workdir) {
		return "", fmt.Errorf("workdir must be relative: %s", workdir)
	}
	directory := filepath.Clean(filepath.Join(root, workdir))
	rel, err := filepath.Rel(root, directory)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workdir escapes the worktree: %s", workdir)
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", fmt.Errorf("workdir %s: %w", workdir, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workdir is not a directory: %s", workdir)
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", fmt.Errorf("resolving workdir %s: %w", workdir, err)
	}
	rel, err = filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workdir escapes the worktree through a symlink: %s", workdir)
	}
	return resolved, nil
}
