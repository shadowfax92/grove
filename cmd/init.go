package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"grove/internal/config"
	"grove/internal/git"

	"github.com/spf13/cobra"
)

func init() {
	initCmd.Flags().String("name", "", "Repo name to store in config")
	initCmd.Flags().String("default-branch", "", "Default branch to checkout before creating worktrees")
	rootCmd.AddCommand(initCmd)
}

var initCmd = &cobra.Command{
	Use:         "init",
	Annotations: map[string]string{"group": "Setup:"},
	Short:       "Add the current git repo to grove config",
	Args:        cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		name, _ := cmd.Flags().GetString("name")
		defaultBranch, _ := cmd.Flags().GetString("default-branch")

		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		repo, err := buildInitRepo(cwd, name, defaultBranch)
		if err != nil {
			return err
		}

		configPath, err := config.DefaultConfigPath()
		if err != nil {
			return err
		}
		if err := config.AddRepoToFile(configPath, repo); err != nil {
			return err
		}

		fmt.Printf("Added %s (%s) to %s\n", repo.Name, repo.Path, configPath)
		return nil
	},
}

// buildInitRepo infers the config entry for the git repository containing dir.
// Flags override inferred values so unusual repo names and default branches stay explicit.
func buildInitRepo(dir, nameOverride, defaultBranchOverride string) (config.RepoConfig, error) {
	repoPath := git.RepoRoot(dir)
	if repoPath == "" {
		return config.RepoConfig{}, fmt.Errorf("not inside a git repository")
	}

	name := strings.TrimSpace(nameOverride)
	if name == "" {
		name = filepath.Base(repoPath)
	}

	defaultBranch := strings.TrimSpace(defaultBranchOverride)
	if defaultBranch == "" {
		defaultBranch = git.DefaultBranch(repoPath)
	}
	if defaultBranch == "" {
		return config.RepoConfig{}, fmt.Errorf("could not infer default branch; pass --default-branch")
	}

	return config.NewWorktreeRepo(repoPath, name, defaultBranch), nil
}
