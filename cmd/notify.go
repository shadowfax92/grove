package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"grove/internal/config"
	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

func init() {
	notifyCmd.Flags().String("session", "", "Target session name (default: current tmux session)")
	rootCmd.AddCommand(notifyCmd)
}

var notifyCmd = &cobra.Command{
	Use:         "notify [message | clear]",
	Aliases:     []string{"notif", "no"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Show or send notifications for a workspace",
	Long: `Manage notifications for a grove workspace.

  grove notify              — show all notifications
  grove notify <message>    — append a notification to current session
  grove notify clear        — pick sessions via fzf and clear their notifications`,
	RunE: func(cmd *cobra.Command, args []string) error {
		sessionFlag, _ := cmd.Flags().GetString("session")

		mgr, err := state.NewManager()
		if err != nil {
			return err
		}

		// Show mode: no args — always show all notifications
		if len(args) == 0 {
			st, err := mgr.Load()
			if err != nil {
				return err
			}
			return showAllNotifications(st)
		}

		// Clear mode: fzf multi-select
		if args[0] == "clear" {
			return clearNotificationsFzf(mgr)
		}

		// Append mode: needs a session
		sessionName, err := resolveSession(sessionFlag)
		if err != nil {
			return err
		}

		if err := mgr.Lock(); err != nil {
			return err
		}
		defer mgr.Unlock()

		st, err := mgr.Load()
		if err != nil {
			return err
		}

		if mgr.FindBySession(st, sessionName) == nil {
			return fmt.Errorf("no grove workspace for session %q", sessionName)
		}

		message := strings.Join(args, " ")
		mgr.AppendNotification(st, sessionName, message)
		badge := lipgloss.NewStyle().Foreground(clrYellow).Bold(true).Render("★")
		fmt.Printf("%s %s\n", badge, message)
		forwardNotify(sessionName, message)

		return mgr.Save(st)
	},
}

func showAllNotifications(st *state.State) error {
	dim := lipgloss.NewStyle().Faint(true)
	sessionStyle := lipgloss.NewStyle().Foreground(clrCyan).Bold(true)
	badgeStyle := lipgloss.NewStyle().Foreground(clrYellow).Bold(true)

	found := false
	for _, ws := range st.Workspaces {
		if len(ws.Notifications) == 0 {
			continue
		}
		found = true
		fmt.Printf("%s %s\n", badgeStyle.Render("★"), sessionStyle.Render(ws.SessionName))
		for _, n := range ws.Notifications {
			age := state.RelativeTime(n.CreatedAt) + " ago"
			msg := n.Message
			const maxMsg = 80
			if len(msg) > maxMsg {
				msg = msg[:maxMsg-3] + "..."
			}
			fmt.Printf("    %-*s  %s\n", maxMsg, msg, dim.Render(age))
		}
	}

	if !found {
		fmt.Println(dim.Render("No notifications."))
	}
	return nil
}

func clearNotificationsFzf(mgr *state.StateManager) error {
	st, err := mgr.Load()
	if err != nil {
		return err
	}

	var lines []string
	for _, ws := range st.Workspaces {
		if len(ws.Notifications) == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s\t%s (%d)", ws.SessionName, ws.Name, len(ws.Notifications)))
	}

	if len(lines) == 0 {
		fmt.Println(lipgloss.NewStyle().Faint(true).Render("No notifications to clear."))
		return nil
	}

	fzfCmd := exec.Command("fzf", "--multi", "--prompt", "clear > ",
		"--height", "~80%", "--reverse",
		"--delimiter", "\t", "--with-nth", "2")
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return ErrCancelled
		}
		return fmt.Errorf("fzf failed: %w", err)
	}

	if err := mgr.Lock(); err != nil {
		return err
	}
	defer mgr.Unlock()

	st, err = mgr.Load()
	if err != nil {
		return err
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "\t"); idx >= 0 {
			sessionName := line[:idx]
			mgr.ClearNotifications(st, sessionName)
			fmt.Printf("Cleared notifications for %s\n", sessionName)
		}
	}

	return mgr.Save(st)
}

func forwardNotify(sessionName, message string) {
	cfg, err := config.LoadFast()
	if err != nil || len(cfg.Notify.Forward) == 0 {
		return
	}
	r := strings.NewReplacer("$SESSION", sessionName, "$MESSAGE", message)
	for _, tmpl := range cfg.Notify.Forward {
		cmd := r.Replace(tmpl)
		_ = exec.Command("sh", "-c", cmd).Run()
	}
}

func resolveSession(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	session, err := tmux.CurrentSession()
	if err != nil {
		return "", fmt.Errorf("not in a tmux session (use --session to specify): %w", err)
	}
	if !strings.HasPrefix(session, "g/") {
		return "", fmt.Errorf("current session %q is not a grove workspace (use --session to specify)", session)
	}
	return session, nil
}
