package cmd

import (
	"fmt"
	"strings"

	"grove/internal/state"
	"grove/internal/tmux"

	"github.com/spf13/cobra"
)

func init() {
	notifyCmd.Flags().String("session", "", "Target session name (default: current tmux session)")
	rootCmd.AddCommand(notifyCmd)
}

var notifyCmd = &cobra.Command{
	Use:   "notify <message> | clear",
	Short: "Send a notification to a workspace",
	Long:  `Send a notification to a grove workspace. The notification appears as a ★ badge in the sidebar.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("usage: grove notify <message> | clear")
		}

		sessionFlag, _ := cmd.Flags().GetString("session")

		sessionName, err := resolveSession(sessionFlag)
		if err != nil {
			return err
		}

		mgr, err := state.NewManager()
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
			mgr.ClearNotification(st, sessionName)
		} else {
			message := strings.Join(args, " ")
			mgr.SetNotification(st, sessionName, message)
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
