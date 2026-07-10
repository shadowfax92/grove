package cmd

import (
	"fmt"

	"grove/internal/syncfile"

	"github.com/spf13/cobra"
)

var defaultSyncFile = func() string {
	path, err := syncfile.DefaultPath()
	if err != nil {
		return "~/.config/grove/sync.yaml"
	}
	return path
}()

func init() {
	syncCmd.PersistentFlags().StringP("file", "f", defaultSyncFile, "Sync manifest file")
	rootCmd.AddCommand(syncCmd)
}

var syncCmd = &cobra.Command{
	Use:         "sync",
	Annotations: map[string]string{"group": "Setup:"},
	Short:       "Clone missing repositories from the sync manifest",
	Args:        cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("sync apply is not available yet")
	},
}
