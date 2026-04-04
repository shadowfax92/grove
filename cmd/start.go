package cmd

import (
	"fmt"
	"os"
	"strconv"

	"grove/internal/config"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(startCmd)
}

var startCmd = &cobra.Command{
	Use:         "start",
	Annotations: map[string]string{"group": "Setup:"},
	Short:       "Bind popup keys for shadow vim/shell sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.Load()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		selfPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("finding executable path: %w", err)
		}
		selfCmd := strconv.Quote(selfPath)

		// Bind shadow vim popup key
		shadowVimCmd := fmt.Sprintf(`%s shadow toggle vim "#{client_name}" "#{session_name}" "#{pane_id}" >/dev/null 2>&1 || true`, selfCmd)
		if err := tmux.BindKeyRaw("-n", cfg.Shadow.Keys.Vim, "run-shell", "-b", shadowVimCmd); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to bind shadow vim key: %v\n", err)
		}

		// Bind shadow shell popup key
		shadowShellCmd := fmt.Sprintf(`%s shadow toggle shell "#{client_name}" "#{session_name}" "#{pane_id}" >/dev/null 2>&1 || true`, selfCmd)
		if err := tmux.BindKeyRaw("-n", cfg.Shadow.Keys.Shell, "run-shell", "-b", shadowShellCmd); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to bind shadow shell key: %v\n", err)
		}

		// Clean up orphaned shadow sessions when panes die
		cleanupHook := fmt.Sprintf("run-shell '%s shadow cleanup >/dev/null 2>&1 || true'", selfCmd)
		if err := tmux.SetHook("after-kill-pane", cleanupHook); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to set cleanup hook: %v\n", err)
		}

		fmt.Printf("Bound shadow keys: vim=%s, shell=%s\n", cfg.Shadow.Keys.Vim, cfg.Shadow.Keys.Shell)
		return nil
	},
}
