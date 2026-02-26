package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	listHeaderColor  = color.New(color.Bold, color.Faint)
	listRepoColor    = color.New(color.FgCyan)
	listBranchColor  = color.New(color.FgHiGreen)
	listSessionColor = color.New(color.Faint)
	listRunningColor = color.New(color.FgGreen, color.Bold)
	listStoppedColor = color.New(color.Faint)
	listPlainColor   = color.New(color.FgYellow)
)

func init() {
	rootCmd.AddCommand(listCmd)
}

var listCmd = &cobra.Command{
	Use:         "list",
	Aliases:     []string{"ls", "l"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "List all workspaces",
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
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			listHeaderColor.Sprint("REPO"),
			listHeaderColor.Sprint("WORKTREE"),
			listHeaderColor.Sprint("SESSION"),
			listHeaderColor.Sprint("STATUS"),
		)

		for _, ws := range st.Workspaces {
			repo := listPlainColor.Sprint("—")
			worktree := ws.Name
			if ws.Type == "worktree" {
				repo = listRepoColor.Sprint(ws.Repo)
				worktree = listBranchColor.Sprint(ws.Branch)
			} else {
				worktree = listPlainColor.Sprint(ws.Name)
			}

			session := listSessionColor.Sprint(ws.SessionName)

			var status string
			if tmux.SessionExists(ws.SessionName) {
				status = listRunningColor.Sprint("running")
			} else {
				status = listStoppedColor.Sprint("stopped")
			}

			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", repo, worktree, session, status)
		}

		return w.Flush()
	},
}
