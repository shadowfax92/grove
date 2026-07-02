package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"grove/internal/state"
	"grove/internal/workspaces"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	clrCyan    = lipgloss.Color("6")
	clrHiGreen = lipgloss.Color("10")
	clrYellow  = lipgloss.Color("11")
)

func init() {
	listCmd.Flags().Bool("json", false, "Print workspaces as JSON")
	rootCmd.AddCommand(listCmd)
}

var listCmd = &cobra.Command{
	Use:         "list",
	Aliases:     []string{"ls", "l"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "List all workspaces",
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonOut, _ := cmd.Flags().GetBool("json")

		mgr, err := state.NewManager()
		if err != nil {
			return err
		}

		st, err := mgr.Load()
		if err != nil {
			return err
		}
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(listWorkspaceJSON(st.Workspaces))
		}
		inv, err := workspaces.Build(st, nil)
		if err != nil {
			return err
		}

		if len(inv.Managed) == 0 {
			fmt.Println("No workspaces. Run 'grove new' to create one.")
			return nil
		}

		dim := lipgloss.NewStyle().Faint(true)
		branchStyle := lipgloss.NewStyle().Foreground(clrCyan)
		rootStyle := lipgloss.NewStyle().Bold(true).Faint(true)
		workspaceBySession := make(map[string]workspaces.ManagedEntry, len(inv.Managed))
		sessionNames := make([]string, 0, len(inv.Managed))
		for _, entry := range inv.Managed {
			workspaceBySession[entry.Workspace.SessionName] = entry
			sessionNames = append(sessionNames, entry.Workspace.SessionName)
		}

		var rows [][]string
		for _, row := range buildSessionTreeRows(sessionNames) {
			entry, ok := workspaceBySession[row.sessionName]
			name := row.label
			switch {
			case row.depth == 0:
				name = rootStyle.Render(row.label)
			case row.hasChild && !ok:
				name = branchStyle.Render(row.label)
			}

			lastUsed := ""
			if ok {
				workspace := entry.Workspace
				lastUsed = dim.Render("—")
				if workspace.LastUsedAt != "" {
					lastUsed = dim.Render(state.RelativeTime(workspace.LastUsedAt) + " ago")
				}
			}

			rows = append(rows, []string{name, lastUsed})
		}

		t := table.New().
			Border(lipgloss.HiddenBorder()).
			Headers("SESSION", "LAST USED").
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

type listWorkspaceJSONOutput struct {
	Name         string `json:"name"`
	Repo         string `json:"repo"`
	RepoPath     string `json:"repo_path"`
	Branch       string `json:"branch"`
	WorktreePath string `json:"worktree_path"`
	SessionName  string `json:"session_name"`
	CreatedAt    string `json:"created_at"`
	LastUsedAt   string `json:"last_used_at"`
}

func listWorkspaceJSON(items []state.Workspace) []listWorkspaceJSONOutput {
	out := make([]listWorkspaceJSONOutput, 0, len(items))
	for _, ws := range items {
		out = append(out, listWorkspaceJSONOutput{
			Name:         ws.Name,
			Repo:         ws.Repo,
			RepoPath:     ws.RepoPath,
			Branch:       ws.Branch,
			WorktreePath: ws.WorktreePath,
			SessionName:  ws.SessionName,
			CreatedAt:    ws.CreatedAt,
			LastUsedAt:   ws.LastUsedAt,
		})
	}
	return out
}
