package cmd

import (
	"fmt"
	"strings"

	"grove/internal/shadow"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(promoteCmd)
}

var promoteCmd = &cobra.Command{
	Use:         "promote [name]",
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Promote a ghost (gs/) session to a regular session",
	Long: `Promote the current shadow session to a regular tmux session.

  grove promote         — auto-name from cwd (branch, folder, or "home")
  grove promote admin   — explicit name

Only works when the current session is a gs/ shadow session.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if !tmux.IsInsideTmux() {
			return fmt.Errorf("grove promote must run inside tmux")
		}

		session, err := tmux.CurrentSession()
		if err != nil {
			return fmt.Errorf("reading current session: %w", err)
		}
		if !shadow.IsSession(session) {
			return fmt.Errorf("current session %q is not a ghost (gs/) session", session)
		}

		var name string
		if len(args) == 1 {
			name = args[0]
		} else {
			label, err := autoPaneLabel()
			if err != nil {
				return err
			}
			if label == "" {
				return fmt.Errorf("could not infer a name — pass one explicitly")
			}
			name = label
		}

		if !strings.HasPrefix(name, "g/") {
			name = "g/" + name
		}

		if tmux.SessionExists(name) {
			return fmt.Errorf("session %q already exists", name)
		}

		if err := tmux.RenameSession(session, name); err != nil {
			return fmt.Errorf("renaming session: %w", err)
		}

		// Strip shadow metadata
		for _, key := range []string{"shadow_cwd", "shadow_parent_pane", "shadow_env_version"} {
			_ = tmux.UnsetSessionVar(name, key)
		}

		// Register in grove state so notify/list/jump see it
		paneID, _ := tmux.PaneID()
		cwd, _ := tmux.PaneCwd(paneID)
		wsName := strings.TrimPrefix(name, "g/")
		registerWorkspace(wsName, name, cwd)

		fmt.Printf("promoted %s → %s\n", session, name)
		return nil
	},
}
