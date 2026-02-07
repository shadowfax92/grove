package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(listCmd)
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all grove-managed workspaces",
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
			fmt.Println("No workspaces. Run 'grove start' or 'grove new' to create one.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "REPO\tWORKTREE\tSESSION\tSTATUS")

		for _, ws := range st.Workspaces {
			repo := "—"
			worktree := ws.Name
			if ws.Type == "worktree" {
				repo = ws.Repo
				worktree = ws.Branch
			}

			status := "stopped"
			if tmux.SessionExists(ws.SessionName) {
				status = "running"
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", repo, worktree, ws.SessionName, status)
		}

		return w.Flush()
	},
}
