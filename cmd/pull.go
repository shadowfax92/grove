package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"grove/internal/syncfile"

	"github.com/spf13/cobra"
)

const defaultPullJobs = 8

func init() {
	pullCmd.Flags().Bool("all", false, "Pull every manifest repository")
	pullCmd.Flags().IntP("jobs", "j", defaultPullJobs, "Max repositories to pull in parallel")
	pullCmd.Flags().String("only", "", "Only pull repositories matching this group/name glob")
	pullCmd.Flags().Bool("dry-run", false, "Print the pull plan without using the network")
	rootCmd.AddCommand(pullCmd)
}

var pullCmd = &cobra.Command{
	Use:         "pull",
	Annotations: map[string]string{"group": "Setup:"},
	Short:       "Safely fast-forward repository default branches",
	Args:        cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		all, _ := cmd.Flags().GetBool("all")
		jobs, _ := cmd.Flags().GetInt("jobs")
		only, _ := cmd.Flags().GetString("only")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		if jobs < 1 {
			return fmt.Errorf("--jobs must be at least 1")
		}

		manifest, err := syncfile.Load(defaultSyncFile)
		if err != nil {
			return err
		}
		repos, err := syncfile.FilterRepos(manifest.Repos(), only)
		if err != nil {
			return err
		}
		targets := syncfile.InspectRepos(repos, jobs, nil)
		if !all {
			present := make([]syncfile.PullTarget, 0, len(targets))
			for _, target := range targets {
				if target.State.Exists {
					present = append(present, target)
				}
			}
			if len(present) == 0 {
				return fmt.Errorf("no manifest repositories are present — run grove sync")
			}
			targets, err = pickPullFzf(present)
			if err != nil {
				return err
			}
		}
		syncfile.ResolveDefaultBranches(targets, nil)
		results := syncfile.RunPull(targets, jobs, dryRun, nil)
		counts := writePullSummary(cmd.OutOrStdout(), results)
		if counts.Failed > 0 {
			return fmt.Errorf("pull failed for %d repositories", counts.Failed)
		}
		return nil
	},
}

func pickPullFzf(targets []syncfile.PullTarget) ([]syncfile.PullTarget, error) {
	lookup := make(map[string]syncfile.PullTarget, len(targets))
	input := renderPullPickerInput(targets, lookup)
	fzfCmd := exec.Command(
		"fzf",
		"--multi",
		"--bind", "ctrl-a:select-all",
		"--prompt", "pull > ",
		"--height", "100%",
		"--reverse",
		"--delimiter", "\t",
		"--accept-nth", "1",
		"--with-nth", "2,3,4",
	)
	fzfCmd.Stdin = strings.NewReader(input)
	fzfCmd.Stderr = os.Stderr
	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return nil, ErrCancelled
		}
		return nil, fmt.Errorf("fzf failed: %w (is fzf installed?)", err)
	}

	var selected []syncfile.PullTarget
	for _, id := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if target, ok := lookup[strings.TrimSpace(id)]; ok {
			selected = append(selected, target)
		}
	}
	if len(selected) == 0 {
		return nil, ErrCancelled
	}
	return selected, nil
}

func renderPullPickerInput(targets []syncfile.PullTarget, lookup map[string]syncfile.PullTarget) string {
	sorted := append([]syncfile.PullTarget(nil), targets...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Repo.Key() < sorted[j].Repo.Key() })
	maxKey := 0
	for _, target := range sorted {
		if len(target.Repo.Key()) > maxKey {
			maxKey = len(target.Repo.Key())
		}
	}
	lines := make([]string, 0, len(sorted))
	for _, target := range sorted {
		key := target.Repo.Key()
		lookup[key] = target
		branch := target.State.CurrentBranch
		switch {
		case !target.State.Git:
			branch = "not a git repository"
		case target.State.Detached:
			branch = "detached"
		case branch == "":
			branch = "unknown"
		}
		dirty := ""
		if target.State.Dirty {
			dirty = "!"
		}
		lines = append(lines, fmt.Sprintf("%s\t%-*s\t%-16s\t%s", key, maxKey, key, branch, dirty))
	}
	return strings.Join(lines, "\n")
}

type pullCounts struct {
	Updated int
	Current int
	Skipped int
	Failed  int
}

func writePullSummary(out io.Writer, results []syncfile.PullResult) pullCounts {
	var counts pullCounts
	for _, result := range results {
		switch result.Status {
		case syncfile.PullUpdated:
			counts.Updated++
		case syncfile.PullCurrent:
			counts.Current++
		case syncfile.PullSkipped:
			counts.Skipped++
		case syncfile.PullFailed:
			counts.Failed++
		}
	}
	writePullGroup(out, "Updated", syncfile.PullUpdated, results)
	writePullGroup(out, "Already current", syncfile.PullCurrent, results)
	writePullGroup(out, "Skipped", syncfile.PullSkipped, results)
	writePullGroup(out, "Failed", syncfile.PullFailed, results)
	fmt.Fprintf(out, "\nSummary: %d updated, %d already current, %d skipped, %d failed\n", counts.Updated, counts.Current, counts.Skipped, counts.Failed)
	return counts
}

func writePullGroup(out io.Writer, title string, status syncfile.PullResultStatus, results []syncfile.PullResult) {
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
		if status == syncfile.PullFailed {
			marker = "✗"
		} else if status == syncfile.PullSkipped {
			marker = "–"
		}
		fmt.Fprintf(out, "  %s %s: %s\n", marker, result.Repo.Key(), result.Reason)
	}
}
