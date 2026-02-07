package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"grove/internal/config"
	"grove/internal/git"
	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	newCmd.Flags().BoolP("plain", "p", false, "Create a plain workspace (not tied to a repo)")
	newCmd.Flags().String("path", "", "Working directory for plain workspace (default: $HOME)")
	newCmd.Flags().Bool("no-switch", false, "Don't switch to the new session after creation")
	rootCmd.AddCommand(newCmd)
}

var newCmd = &cobra.Command{
	Use:   "new <repo> <branch> | --plain <name>",
	Short: "Create a new workspace",
	RunE: func(cmd *cobra.Command, args []string) error {
		plain, _ := cmd.Flags().GetBool("plain")
		noSwitch, _ := cmd.Flags().GetBool("no-switch")

		cfg, err := config.Load()
		if err != nil {
			return err
		}

		mgr, err := state.NewManager()
		if err != nil {
			return err
		}
		if err := mgr.Lock(); err != nil {
			return err
		}
		defer mgr.Unlock()

		st, err := mgr.Load()
		if err != nil {
			return err
		}

		if plain {
			return createPlainWorkspace(cmd, args, cfg, mgr, st, noSwitch)
		}
		return createWorktreeWorkspace(cmd, args, cfg, mgr, st, noSwitch)
	},
}

func createPlainWorkspace(cmd *cobra.Command, args []string, cfg *config.Config, mgr *state.StateManager, st *state.State, noSwitch bool) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: grove new --plain <name>")
	}
	name := args[0]
	sessionName := fmt.Sprintf("grove/%s", name)

	if mgr.FindBySession(st, sessionName) != nil {
		return fmt.Errorf("workspace %q already exists", name)
	}

	dir, _ := cmd.Flags().GetString("path")
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}

	if err := tmux.NewSession(sessionName, dir); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	ws := state.Workspace{
		Name:        name,
		Type:        "plain",
		Path:        dir,
		SessionName: sessionName,
	}
	mgr.AddWorkspace(st, ws)

	if err := mgr.Save(st); err != nil {
		return err
	}

	fmt.Printf("Created plain workspace %q\n", name)

	if !noSwitch && tmux.IsInsideTmux() {
		return tmux.SwitchClient(sessionName)
	}
	return nil
}

func createWorktreeWorkspace(cmd *cobra.Command, args []string, cfg *config.Config, mgr *state.StateManager, st *state.State, noSwitch bool) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: grove new <repo> <branch>")
	}
	repoName := args[0]
	branch := args[1]

	repo := cfg.FindRepo(repoName)
	if repo == nil {
		return fmt.Errorf("repo %q not found in config", repoName)
	}

	sessionName := fmt.Sprintf("grove/%s/%s", repo.Name, branch)
	if mgr.FindBySession(st, sessionName) != nil {
		return fmt.Errorf("workspace %q already exists", repo.Name+"/"+branch)
	}

	worktreePath := filepath.Join(repo.Path, ".grove", "worktrees", branch)

	if err := git.EnsureGitignore(repo.Path); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update .gitignore: %v\n", err)
	}

	if err := git.AddWorktree(repo.Path, worktreePath, branch); err != nil {
		return fmt.Errorf("creating worktree: %w", err)
	}

	// Run setup commands
	for _, setupCmd := range repo.Setup {
		fmt.Printf("Running: %s\n", setupCmd)
		c := exec.Command("sh", "-c", setupCmd)
		c.Dir = worktreePath
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		if err := c.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: setup command failed: %v\n", err)
		}
	}

	if err := tmux.NewSession(sessionName, worktreePath); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	ws := state.Workspace{
		Name:         fmt.Sprintf("%s/%s", repo.Name, branch),
		Type:         "worktree",
		Repo:         repo.Name,
		RepoPath:     repo.Path,
		WorktreePath: worktreePath,
		Branch:       branch,
		SessionName:  sessionName,
	}
	mgr.AddWorkspace(st, ws)

	if err := mgr.Save(st); err != nil {
		return err
	}

	fmt.Printf("Created worktree workspace %s/%s\n", repo.Name, branch)

	if !noSwitch && tmux.IsInsideTmux() {
		return tmux.SwitchClient(sessionName)
	}
	return nil
}
