package cmd

import (
	"fmt"

	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	clrCyan    = lipgloss.Color("6")
	clrHiGreen = lipgloss.Color("10")
	clrYellow  = lipgloss.Color("11")
	clrGreen   = lipgloss.Color("2")
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
			fmt.Println("No workspaces. Run 'grove new' to create one.")
			return nil
		}

		dim := lipgloss.NewStyle().Faint(true)

		var rows [][]string
		for _, ws := range st.Workspaces {
			name := lipgloss.NewStyle().Foreground(clrYellow).Render(ws.Name)
			if ws.Type == "worktree" {
				name = lipgloss.NewStyle().Foreground(clrCyan).Render(ws.Repo) +
					"/" +
					lipgloss.NewStyle().Foreground(clrHiGreen).Render(ws.Branch)
			}

			session := dim.Render(ws.SessionName)

			var status string
			if tmux.SessionExists(ws.SessionName) {
				status = lipgloss.NewStyle().Foreground(clrGreen).Bold(true).Render("running")
			} else {
				status = dim.Render("stopped")
			}

			lastUsed := dim.Render("—")
			if ws.LastUsedAt != "" {
				lastUsed = dim.Render(state.RelativeTime(ws.LastUsedAt) + " ago")
			}
			if len(ws.Notifications) > 0 {
				lastUsed += " " + lipgloss.NewStyle().Foreground(clrYellow).Bold(true).Render("★")
			}

			rows = append(rows, []string{name, session, status, lastUsed})
		}

		t := table.New().
			Border(lipgloss.HiddenBorder()).
			Headers("NAME", "SESSION", "STATUS", "LAST USED").
			Rows(rows...).
			StyleFunc(func(row, col int) lipgloss.Style {
				s := lipgloss.NewStyle().PaddingRight(2)
				if row == table.HeaderRow {
					return s.Bold(true).Faint(true)
				}
				return s
			})

		fmt.Println(t)
		return nil
	},
}
