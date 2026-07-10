package cmd

import (
	"fmt"
	"io"

	"grove/internal/syncfile"

	"github.com/spf13/cobra"
)

func init() {
	syncCmd.AddCommand(syncStatusCmd)
}

var syncStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show local repository presence and dirty state",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		file, _ := cmd.Flags().GetString("file")
		manifest, err := syncfile.Load(file)
		if err != nil {
			return err
		}
		targets := syncfile.InspectRepos(manifest.Repos(), defaultPullJobs, nil)
		writeSyncStatus(cmd.OutOrStdout(), targets)
		return nil
	},
}

func writeSyncStatus(out io.Writer, targets []syncfile.PullTarget) {
	lastGroup := ""
	for _, target := range targets {
		if target.Repo.Group != lastGroup {
			if lastGroup != "" {
				fmt.Fprintln(out)
			}
			lastGroup = target.Repo.Group
			fmt.Fprintln(out, lastGroup)
		}
		marker := "✓"
		note := ""
		switch {
		case !target.State.Exists:
			marker = "✗"
		case !target.State.Git:
			marker = "✗"
			note = " (not a git repository)"
		case target.State.Dirty:
			marker = "!"
		}
		fmt.Fprintf(out, "  %s %s%s\n", marker, target.Repo.Name, note)
	}
}
