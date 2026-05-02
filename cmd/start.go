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

		// Bind shadow git (lazygit) popup key
		shadowGitCmd := fmt.Sprintf(`%s shadow toggle git "#{client_name}" "#{session_name}" "#{pane_id}" >/dev/null 2>&1 || true`, selfCmd)
		if err := tmux.BindKeyRaw("-n", cfg.Shadow.Keys.Git, "run-shell", "-b", shadowGitCmd); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to bind shadow git key: %v\n", err)
		}

		// Bind shadow gitui popup key
		shadowGituiCmd := fmt.Sprintf(`%s shadow toggle gitui "#{client_name}" "#{session_name}" "#{pane_id}" >/dev/null 2>&1 || true`, selfCmd)
		if err := tmux.BindKeyRaw("-n", cfg.Shadow.Keys.Gitui, "run-shell", "-b", shadowGituiCmd); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to bind shadow gitui key: %v\n", err)
		}

		// Bind nvim Diffview popup key — opens nvim DiffviewOpen on SHA from clipboard.
		// Workflow: in gitui press `y` to copy SHA, then this key opens Diffview in a popup.
		diffviewScript := `sha=$(pbpaste 2>/dev/null | tr -d '[:space:]'); ` +
			`if printf '%s' "$sha" | grep -Eq '^[0-9a-fA-F]{7,40}$'; then ` +
			`nvim -c "DiffviewOpen $sha~..$sha"; ` +
			`else ` +
			`printf 'Clipboard is not a SHA: %s\n(In gitui: hover commit, press y, then this key.)\nPress any key...' "$sha"; read -n 1 -s; ` +
			`fi`
		// Bound to the tmux prefix key (no `-n`), so trigger is `prefix + <key>` (default: Ctrl-B U).
		if err := tmux.BindKeyRaw(cfg.Shadow.Keys.Diffview,
			"display-popup", "-E", "-w", "90%", "-h", "90%",
			"-d", "#{pane_current_path}", diffviewScript); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to bind diffview key: %v\n", err)
		}

		// Bind shadow delete key
		shadowDeleteCommand := fmt.Sprintf(`%s shadow delete "#{client_name}" "#{session_name}" "#{pane_id}" >/dev/null 2>&1 || true`, selfCmd)
		if err := tmux.BindKeyRaw("-n", cfg.Shadow.Keys.Delete, "run-shell", "-b", shadowDeleteCommand); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to bind shadow delete key: %v\n", err)
		}

		// Clean up orphaned shadow sessions when panes die
		cleanupHook := fmt.Sprintf("run-shell '%s shadow cleanup >/dev/null 2>&1 || true'", selfCmd)
		if err := tmux.SetHook("after-kill-pane", cleanupHook); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to set cleanup hook: %v\n", err)
		}

		fmt.Printf("Bound shadow keys: vim=%s, shell=%s, git=%s, gitui=%s, diffview=prefix+%s, delete=%s\n",
			cfg.Shadow.Keys.Vim, cfg.Shadow.Keys.Shell, cfg.Shadow.Keys.Git,
			cfg.Shadow.Keys.Gitui, cfg.Shadow.Keys.Diffview, cfg.Shadow.Keys.Delete)
		return nil
	},
}
