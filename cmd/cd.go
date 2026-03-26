package cmd

import (
	"fmt"

	"grove/internal/state"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(cdCmd)
}

var cdCmd = &cobra.Command{
	Use:         "cd [workspace]",
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Print the path for an existing workspace",
	Long: `Print the path for an existing workspace.

  grove cd             — pick existing workspace via fzf and print its path
  grove cd <workspace> — print the path for a specific workspace`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		mgr, err := state.NewManager()
		if err != nil {
			return err
		}

		st, err := mgr.Load()
		if err != nil {
			return err
		}
		if len(st.Workspaces) == 0 {
			return fmt.Errorf("no workspaces")
		}

		var ws *state.Workspace
		if len(args) == 1 {
			ws = mgr.FindWorkspace(st, args[0])
			if ws == nil {
				return fmt.Errorf("workspace %q not found", args[0])
			}
		} else {
			picked, err := pickSessionFzf("cd > ", st)
			if err != nil {
				return err
			}
			ws = mgr.FindBySession(st, picked)
			if ws == nil {
				return fmt.Errorf("workspace not found")
			}
		}

		fmt.Println(workspaceDir(ws))
		return nil
	},
}
