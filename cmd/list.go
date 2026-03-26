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
		branchStyle := lipgloss.NewStyle().Foreground(clrCyan)
		rootStyle := lipgloss.NewStyle().Bold(true).Faint(true)
		workspaceBySession := make(map[string]*state.Workspace, len(st.Workspaces))
		sessionNames := make([]string, 0, len(st.Workspaces))
		for i := range st.Workspaces {
			workspaceBySession[st.Workspaces[i].SessionName] = &st.Workspaces[i]
			sessionNames = append(sessionNames, st.Workspaces[i].SessionName)
		}

		var rows [][]string
		for _, row := range buildSessionTreeRows(sessionNames) {
			workspace := workspaceBySession[row.sessionName]
			name := row.label
			switch {
			case row.depth == 0:
				name = rootStyle.Render(row.label)
			case row.hasChild && workspace == nil:
				name = branchStyle.Render(row.label)
			}

			status := ""
			lastUsed := ""
			if workspace != nil {
				if tmux.SessionExists(workspace.SessionName) {
					status = lipgloss.NewStyle().Foreground(clrGreen).Bold(true).Render("running")
				} else {
					status = dim.Render("stopped")
				}

				lastUsed = dim.Render("—")
				if workspace.LastUsedAt != "" {
					lastUsed = dim.Render(state.RelativeTime(workspace.LastUsedAt) + " ago")
				}
				if len(workspace.Notifications) > 0 {
					lastUsed += " " + lipgloss.NewStyle().Foreground(clrYellow).Bold(true).Render("★")
				}
			}

			rows = append(rows, []string{name, status, lastUsed})
		}

		t := table.New().
			Border(lipgloss.HiddenBorder()).
			Headers("SESSION", "STATUS", "LAST USED").
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
