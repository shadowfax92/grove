package cmd

import (
	"fmt"

	"grove/internal/state"
	"grove/internal/workspaces"

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
		inv, err := workspaces.Build(st, nil)
		if err != nil {
			return err
		}
		if len(inv.Managed) == 0 {
			return fmt.Errorf("no workspaces")
		}

		var ws state.Workspace
		if len(args) == 1 {
			entry, ok := inv.FindManaged(args[0])
			if !ok {
				return fmt.Errorf("workspace %q not found", args[0])
			}
			ws = entry.Workspace
		} else {
			picked, err := pickSessionFzf("cd > ", inv.ManagedByLastUsed())
			if err != nil {
				return err
			}
			entry, ok := inv.FindManagedBySession(picked)
			if !ok {
				return fmt.Errorf("workspace not found")
			}
			ws = entry.Workspace
		}

		fmt.Println(workspaceDir(&ws))
		return nil
	},
}
