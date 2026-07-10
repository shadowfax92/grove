package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"grove/internal/syncfile"

	"github.com/spf13/cobra"
)

const defaultExportJobs = 8

func init() {
	syncCmd.AddCommand(syncExportCmd)
}

var syncExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Select local repositories to append to the sync manifest",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		file, _ := cmd.Flags().GetString("file")
		rootRaw := syncfile.DefaultRoot
		root, err := syncfile.ExpandRoot(rootRaw)
		if err != nil {
			return err
		}
		manifest := &syncfile.Manifest{Root: root, Groups: map[string][]syncfile.Entry{}}
		if _, statErr := os.Stat(file); statErr == nil {
			manifest, err = syncfile.Load(file)
			if err != nil {
				return err
			}
			root = manifest.Root
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("stat sync manifest: %w", statErr)
		}

		candidates, warnings, err := syncfile.Scan(root, defaultExportJobs, nil)
		if err != nil {
			return err
		}
		for _, warning := range warnings {
			rel := warning.Path
			if candidateRel, relErr := filepathRel(root, warning.Path); relErr == nil {
				rel = candidateRel
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s excluded: %s\n", rel, warning.Reason)
		}
		candidates = syncfile.FilterNewCandidates(candidates, manifest)
		if len(candidates) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No new repositories found.")
			return nil
		}

		selected, err := pickExportFzf(candidates)
		if err != nil {
			return err
		}
		if err := syncfile.Append(file, rootRaw, syncfile.GroupCandidates(selected)); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Added %d repositories to %s\n", len(selected), file)
		return nil
	},
}

func pickExportFzf(candidates []syncfile.Candidate) ([]syncfile.Candidate, error) {
	lookup := make(map[string]syncfile.Candidate, len(candidates))
	input := renderExportPickerInput(candidates, lookup)
	fzfCmd := exec.Command(
		"fzf",
		"--multi",
		"--bind", "ctrl-a:select-all",
		"--prompt", "sync export > ",
		"--height", "100%",
		"--reverse",
		"--delimiter", "\t",
		"--accept-nth", "1",
		"--with-nth", "2,3",
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

	var selected []syncfile.Candidate
	for _, id := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if candidate, ok := lookup[strings.TrimSpace(id)]; ok {
			selected = append(selected, candidate)
		}
	}
	if len(selected) == 0 {
		return nil, ErrCancelled
	}
	return selected, nil
}

func renderExportPickerInput(candidates []syncfile.Candidate, lookup map[string]syncfile.Candidate) string {
	sorted := append([]syncfile.Candidate(nil), candidates...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Relative < sorted[j].Relative })
	maxPath := 0
	for _, candidate := range sorted {
		if len(candidate.Relative) > maxPath {
			maxPath = len(candidate.Relative)
		}
	}
	lines := make([]string, 0, len(sorted))
	for _, candidate := range sorted {
		lookup[candidate.Relative] = candidate
		lines = append(lines, fmt.Sprintf("%s\t%-*s\t%s", candidate.Relative, maxPath, candidate.Relative, candidate.URL))
	}
	return strings.Join(lines, "\n")
}

func filepathRel(root, target string) (string, error) {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(rel), nil
}
