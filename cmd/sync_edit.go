package cmd

import (
	"os"
	"os/exec"

	"grove/internal/syncfile"

	"github.com/spf13/cobra"
)

func init() {
	syncCmd.AddCommand(syncEditCmd)
}

var syncEditCmd = &cobra.Command{
	Use:   "edit",
	Short: "Open the sync manifest in $EDITOR",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		file, _ := cmd.Flags().GetString("file")
		if err := syncfile.Ensure(file); err != nil {
			return err
		}
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}
		edit := exec.Command(editor, file)
		edit.Stdin = os.Stdin
		edit.Stdout = os.Stdout
		edit.Stderr = os.Stderr
		return edit.Run()
	},
}
