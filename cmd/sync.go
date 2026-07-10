package cmd

import (
	"fmt"
	"io"

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
	syncCmd.Flags().Bool("dry-run", false, "Print the clone plan without changing disk")
	syncCmd.Flags().IntP("jobs", "j", defaultSyncJobs, "Max repositories to clone in parallel")
	syncCmd.Flags().String("only", "", "Only sync repositories matching this group/name glob")
	rootCmd.AddCommand(syncCmd)
}

const defaultSyncJobs = 4

var syncCmd = &cobra.Command{
	Use:         "sync",
	Annotations: map[string]string{"group": "Setup:"},
	Short:       "Clone missing repositories from the sync manifest",
	Args:        cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		file, _ := cmd.Flags().GetString("file")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		jobs, _ := cmd.Flags().GetInt("jobs")
		only, _ := cmd.Flags().GetString("only")
		if jobs < 1 {
			return fmt.Errorf("--jobs must be at least 1")
		}

		manifest, err := syncfile.Load(file)
		if err != nil {
			return err
		}
		plan, err := syncfile.PlanApply(manifest, only, nil)
		if err != nil {
			return err
		}
		results := syncfile.RunApply(plan, jobs, dryRun, nil)
		counts := writeApplySummary(cmd.OutOrStdout(), results)
		if counts.Failed > 0 {
			return fmt.Errorf("sync failed for %d repositories", counts.Failed)
		}
		return nil
	},
}

type applyCounts struct {
	Cloned  int
	Present int
	Planned int
	Failed  int
}

func writeApplySummary(out io.Writer, results []syncfile.ApplyResult) applyCounts {
	var counts applyCounts
	for _, result := range results {
		switch result.Status {
		case syncfile.ApplyCloned:
			counts.Cloned++
		case syncfile.ApplyPresent:
			counts.Present++
		case syncfile.ApplyPlanned:
			counts.Planned++
		case syncfile.ApplyFailed:
			counts.Failed++
		}
	}
	writeApplyGroup(out, "Cloned", syncfile.ApplyCloned, results)
	writeApplyGroup(out, "Already present", syncfile.ApplyPresent, results)
	writeApplyGroup(out, "Planned", syncfile.ApplyPlanned, results)
	writeApplyGroup(out, "Failed", syncfile.ApplyFailed, results)
	if counts.Planned > 0 {
		fmt.Fprintf(out, "\nSummary: %d cloned, %d already present, %d failed, %d planned\n", counts.Cloned, counts.Present, counts.Failed, counts.Planned)
	} else {
		fmt.Fprintf(out, "\nSummary: %d cloned, %d already present, %d failed\n", counts.Cloned, counts.Present, counts.Failed)
	}
	return counts
}

func writeApplyGroup(out io.Writer, title string, status syncfile.ApplyResultStatus, results []syncfile.ApplyResult) {
	count := 0
	for _, result := range results {
		if result.Status == status {
			count++
		}
	}
	if count == 0 {
		return
	}
	fmt.Fprintf(out, "%s (%d)\n", title, count)
	for _, result := range results {
		if result.Status != status {
			continue
		}
		marker := "✓"
		if status == syncfile.ApplyFailed {
			marker = "✗"
		} else if status == syncfile.ApplyPlanned || status == syncfile.ApplyPresent {
			marker = "–"
		}
		if result.Reason != "" {
			fmt.Fprintf(out, "  %s %s: %s\n", marker, result.Repo.Key(), result.Reason)
		} else {
			fmt.Fprintf(out, "  %s %s\n", marker, result.Repo.Key())
		}
	}
}
