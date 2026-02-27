package cmd

import (
	"fmt"
	"strings"

	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var (
	notifBadgeColor   = color.New(color.FgYellow, color.Bold)
	notifSessionColor = color.New(color.FgCyan, color.Bold)
	notifMsgColor     = color.New(color.FgWhite)
	notifAgeColor     = color.New(color.Faint)
	notifDimColor     = color.New(color.Faint)
)

func init() {
	notifyCmd.Flags().String("session", "", "Target session name (default: current tmux session)")
	rootCmd.AddCommand(notifyCmd)
}

var notifyCmd = &cobra.Command{
	Use:   "notify [message | clear]",
	Short: "Show or send notifications for a workspace",
	Long: `Manage notifications for a grove workspace.

  grove notify              — show notifications for current session
  grove notify <message>    — append a notification
  grove notify clear        — clear all notifications`,
	RunE: func(cmd *cobra.Command, args []string) error {
		sessionFlag, _ := cmd.Flags().GetString("session")

		mgr, err := state.NewManager()
		if err != nil {
			return err
		}

		// Show mode: no args
		if len(args) == 0 {
			st, err := mgr.Load()
			if err != nil {
				return err
			}

			// If --session given or inside a grove tmux session, show that session
			// Otherwise show all notifications across workspaces
			sessionName, sessionErr := resolveSession(sessionFlag)
			if sessionErr == nil {
				ws := mgr.FindBySession(st, sessionName)
				if ws == nil {
					return fmt.Errorf("no grove workspace for session %q", sessionName)
				}
				if len(ws.Notifications) == 0 {
					fmt.Println(notifDimColor.Sprint("No notifications."))
					return nil
				}
				fmt.Printf("%s %s\n", notifBadgeColor.Sprint("★"), notifSessionColor.Sprint(sessionName))
				for _, n := range ws.Notifications {
					age := state.RelativeTime(n.CreatedAt)
					fmt.Printf("  %-40s %s\n", notifMsgColor.Sprint(n.Message), notifAgeColor.Sprintf("%s ago", age))
				}
				return nil
			}

			// No session context — show all
			found := false
			for _, ws := range st.Workspaces {
				if len(ws.Notifications) == 0 {
					continue
				}
				found = true
				fmt.Printf("%s %s\n", notifBadgeColor.Sprint("★"), notifSessionColor.Sprint(ws.SessionName))
				for _, n := range ws.Notifications {
					age := state.RelativeTime(n.CreatedAt)
					fmt.Printf("  %-40s %s\n", notifMsgColor.Sprint(n.Message), notifAgeColor.Sprintf("%s ago", age))
				}
			}
			if !found {
				fmt.Println(notifDimColor.Sprint("No notifications."))
			}
			return nil
		}

		// Mutating modes need a session
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

		if args[0] == "clear" {
			mgr.ClearNotifications(st, sessionName)
			fmt.Println(notifDimColor.Sprint("Notifications cleared."))
		} else {
			message := strings.Join(args, " ")
			mgr.AppendNotification(st, sessionName, message)
			fmt.Printf("%s %s\n", notifBadgeColor.Sprint("★"), notifMsgColor.Sprint(message))
		}

		return mgr.Save(st)
	},
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
