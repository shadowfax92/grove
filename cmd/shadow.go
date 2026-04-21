package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"

	"grove/internal/config"
	"grove/internal/shadow"
	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	shadowCmd.AddCommand(shadowToggleCmd)
	shadowCmd.AddCommand(shadowCleanupCmd)
	rootCmd.AddCommand(shadowCmd)
}

var shadowCmd = &cobra.Command{
	Use:         "shadow",
	Aliases:     []string{"sh"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Toggle persistent popup sessions (vim, shell) for the current pane",
}

var shadowToggleCmd = &cobra.Command{
	Use:   "toggle <vim|shell> <client_name> <session_name> <pane_id>",
	Short: "Toggle a shadow popup for the current tmux client",
	Args:  cobra.ExactArgs(4),
	RunE: func(cmd *cobra.Command, args []string) error {
		typ, err := normalizeShadowType(args[0])
		if err != nil {
			return err
		}

		cfg, err := config.LoadFast()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		clientName := args[1]
		currentSession := args[2]
		activePane := args[3]

		popupClient, err := shadow.PopupClient(currentSession, clientName)
		if err != nil {
			return err
		}

		parentPane, err := shadow.ParentPane(currentSession, activePane)
		if err != nil {
			return err
		}

		targetSession := shadow.Name(parentPane, typ)
		if shadow.IsSession(currentSession) {
			if err := tmux.ClosePopup(popupClient); err != nil {
				return fmt.Errorf("closing popup: %w", err)
			}
			if currentSession == targetSession {
				return nil
			}
		}

		if !tmux.PaneExists(parentPane) {
			return shadow.CleanupOrphans()
		}

		paneCwd, err := tmux.PaneCwd(parentPane)
		if err != nil {
			return fmt.Errorf("getting pane cwd: %w", err)
		}

		if err := shadow.Ensure(targetSession, paneCwd, typ, parentPane); err != nil {
			return err
		}
		if err := tmux.SetSessionVar(targetSession, "shadow_client_name", popupClient); err != nil {
			return fmt.Errorf("storing shadow client: %w", err)
		}

		command := fmt.Sprintf("exec tmux attach-session -t '=%s'", targetSession)
		return tmux.DisplayPopup(popupClient, cfg.Shadow.Popup.Width, cfg.Shadow.Popup.Height, command)
	},
}

var shadowCleanupCmd = &cobra.Command{
	Use:     "cleanup",
	Aliases: []string{"clean"},
	Short:   "Remove stale shadow sessions",
	RunE: func(cmd *cobra.Command, args []string) error {
		opts, err := shadowCleanupOptionsFromFlags(cmd)
		if err != nil {
			return err
		}

		report, err := runShadowCleanup(opts)
		printShadowCleanupReport(cmd.OutOrStdout(), report, opts.DryRun)
		return err
	},
}

var runShadowCleanup = shadow.Cleanup

func init() {
	shadowCleanupCmd.Flags().Bool("dry-run", false, "Show matching shadow sessions without removing them")
	shadowCleanupCmd.Flags().Bool("all", false, "Remove all shadow sessions")
	shadowCleanupCmd.Flags().String("inactive", "", "Remove shadow sessions inactive longer than this threshold (for example: 1h, 1d)")
}

func normalizeShadowType(typ string) (string, error) {
	switch typ {
	case "vim":
		return "vim", nil
	case "shell", "sh":
		return "sh", nil
	default:
		return "", fmt.Errorf("invalid shadow type %q", typ)
	}
}

func shadowCleanupOptionsFromFlags(cmd *cobra.Command) (shadow.CleanupOptions, error) {
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	removeAll, _ := cmd.Flags().GetBool("all")
	inactiveRaw, _ := cmd.Flags().GetString("inactive")

	if removeAll && strings.TrimSpace(inactiveRaw) != "" {
		return shadow.CleanupOptions{}, fmt.Errorf("--all cannot be combined with --inactive")
	}

	inactiveOlderThan, err := shadow.ParseInactiveThreshold(inactiveRaw)
	if err != nil {
		return shadow.CleanupOptions{}, err
	}

	return shadow.CleanupOptions{
		DryRun:            dryRun,
		RemoveAll:         removeAll,
		InactiveOlderThan: inactiveOlderThan,
	}, nil
}

func printShadowCleanupReport(w io.Writer, report shadow.CleanupReport, dryRun bool) {
	if len(report.Matched) == 0 {
		fmt.Fprintln(w, "No shadow sessions matched cleanup criteria.")
		return
	}

	if dryRun {
		fmt.Fprintf(w, "Would remove %d shadow sessions:\n", len(report.Matched))
		for _, candidate := range report.Matched {
			fmt.Fprintf(w, "  %-12s %-8s last active %s ago\n", candidate.SessionName, candidate.Reason, shadowCleanupAge(candidate.LastActiveAt))
		}
		return
	}

	fmt.Fprintf(w, "Removed %d shadow sessions:\n", len(report.Removed))
	for _, candidate := range report.Removed {
		fmt.Fprintf(w, "  %-12s %s\n", candidate.SessionName, candidate.Reason)
	}
	if len(report.Failed) > 0 {
		fmt.Fprintf(w, "Failed to remove %d shadow sessions:\n", len(report.Failed))
		for _, failure := range report.Failed {
			fmt.Fprintf(w, "  %-12s %v\n", failure.Candidate.SessionName, failure.Err)
		}
	}
}

func shadowCleanupAge(ts time.Time) string {
	if ts.IsZero() {
		return "unknown"
	}
	return state.RelativeTime(ts.UTC().Format(time.RFC3339))
}
