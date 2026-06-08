package cmd

import (
	"fmt"
	"os"

	"grove/internal/config"
	"grove/internal/state"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(whichCmd)
}

var whichCmd = &cobra.Command{
	Use:         "which [path]",
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Print the registered repo name for a path",
	Args:        cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := ""
		if len(args) == 1 {
			path = args[0]
		} else {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			path = cwd
		}

		cfg, err := config.LoadFast()
		if err != nil {
			return err
		}
		mgr, err := state.NewManager()
		if err != nil {
			return err
		}
		st, err := mgr.Load()
		if err != nil {
			return err
		}

		name, err := repoNameForPath(cfg, st, path)
		if err != nil {
			return err
		}
		fmt.Println(name)
		return nil
	},
}

// repoNameForPath maps a filesystem path to the registered Grove repo that owns it.
func repoNameForPath(cfg *config.Config, st *state.State, path string) (string, error) {
	target := cleanAbsPath(path)
	bestName := ""
	bestRootLen := -1

	consider := func(name, root string) error {
		if name == "" || root == "" {
			return nil
		}
		root = cleanAbsPath(root)
		if !pathWithin(root, target) {
			return nil
		}
		if len(root) > bestRootLen {
			bestName = name
			bestRootLen = len(root)
			return nil
		}
		if len(root) == bestRootLen && bestName != name {
			return fmt.Errorf("path %q matches multiple repos: %s, %s", path, bestName, name)
		}
		return nil
	}

	if cfg != nil {
		for _, repo := range cfg.Repos {
			if repo.Type == "plain" {
				continue
			}
			if err := consider(repo.Name, repo.Path); err != nil {
				return "", err
			}
		}
	}
	if st != nil {
		for _, ws := range st.Workspaces {
			if ws.Repo == "" || ws.WorktreePath == "" {
				continue
			}
			if err := consider(ws.Repo, ws.WorktreePath); err != nil {
				return "", err
			}
		}
	}

	if bestName == "" {
		return "", fmt.Errorf("unregistered path: %s", path)
	}
	return bestName, nil
}
