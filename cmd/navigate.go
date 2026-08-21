package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"grove/internal/inventory"
	"grove/internal/picker"

	"github.com/spf13/cobra"
)

type worktreeOutput struct {
	Version    int    `json:"version"`
	Repository string `json:"repository"`
	Branch     string `json:"branch,omitempty"`
	Path       string `json:"path"`
	Main       bool   `json:"main"`
}

func (a *application) cdCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "cd [selector]",
		Short: "Print a worktree path",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runNavigate(cmd, args)
		},
	}
}

func (a *application) runNavigate(cmd *cobra.Command, args []string) error {
	context, err := a.loadContext(cmd)
	if err != nil {
		return err
	}
	var entry *inventory.Entry
	if len(args) == 1 {
		entry, err = context.inventory.Resolve(args[0], context.directory)
	} else {
		entry, err = a.pickWorktree(context, "worktree > ")
	}
	if err != nil {
		return err
	}
	if err := a.writeWorktree(cmd, entry); err != nil {
		return err
	}
	if !a.jsonOutput {
		// Grove exits before the Fish wrapper changes its parent shell directory.
		// Successfully emitting the target is therefore the last reliable handoff
		// for recording a visit; recency failure must not turn a valid cd into one.
		if err := a.dependencies.markVisited(entry.Worktree.Path); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: recording navigation recency: %v\n", err)
		}
	}
	return nil
}

func (a *application) pickWorktree(context *commandContext, prompt string) (*inventory.Entry, error) {
	if a.noInput || !a.dependencies.interactive() {
		return nil, fmt.Errorf("selector is required in non-interactive mode")
	}
	items := a.navigationPickerItems(context.inventory)
	path, err := a.dependencies.pick(prompt, items)
	if err != nil {
		return nil, err
	}
	return context.inventory.Resolve(path, context.directory)
}

type navigationCandidate struct {
	entry    *inventory.Entry
	rankedAt time.Time
	ranked   bool
}

// navigationPickerItems owns human navigation presentation: paths remain opaque
// keys, while visible repository/worktree labels are ranked by durable visits and
// then the same creation-time proxy used by list and age cleanup.
func (a *application) navigationPickerItems(inv *inventory.Inventory) []picker.Item {
	candidates := make([]navigationCandidate, 0, len(inv.Entries))
	repositoryWidth := 0
	for _, entry := range inv.Entries {
		if entry.Worktree.Prunable {
			continue
		}
		candidate := navigationCandidate{entry: entry}
		if visitedAt, ok := a.dependencies.lastVisited(entry.Worktree.Path); ok {
			candidate.rankedAt, candidate.ranked = visitedAt, true
		} else if !entry.Worktree.Main {
			createdAt, ok := worktreeCreatedAt(entry.Worktree.Path)
			if ok {
				candidate.rankedAt, candidate.ranked = createdAt, true
			}
		}
		candidates = append(candidates, candidate)
		if width := len(entry.Repository.Name); width > repositoryWidth {
			repositoryWidth = width
		}
	}

	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].ranked != candidates[right].ranked {
			return candidates[left].ranked
		}
		if !candidates[left].ranked {
			return false
		}
		return candidates[left].rankedAt.After(candidates[right].rankedAt)
	})

	items := make([]picker.Item, 0, len(candidates))
	for _, candidate := range candidates {
		entry := candidate.entry
		worktreeName := entry.Worktree.Branch
		if worktreeName == "" {
			worktreeName = "@" + shortSHA(entry.Worktree.Head)
		}
		items = append(items, picker.Item{
			Key:   entry.Worktree.Path,
			Label: fmt.Sprintf("%-*s  %s", repositoryWidth, entry.Repository.Name, worktreeName),
		})
	}
	return items
}

func (a *application) writeWorktree(cmd *cobra.Command, entry *inventory.Entry) error {
	if a.jsonOutput {
		return writeJSON(cmd, worktreeOutput{
			Version:    1,
			Repository: entry.Repository.Name,
			Branch:     entry.Worktree.Branch,
			Path:       entry.Worktree.Path,
			Main:       entry.Worktree.Main,
		})
	}
	return a.writePath(cmd, entry.Worktree.Path)
}

func (a *application) writePath(cmd *cobra.Command, path string) error {
	if err := a.validatePathOutput(path); err != nil {
		return err
	}
	if a.nullOutput {
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s%c", path, byte(0))
		return err
	}
	_, err := fmt.Fprintln(cmd.OutOrStdout(), path)
	return err
}

func (a *application) validatePathOutput(path string) error {
	if !a.jsonOutput && !a.nullOutput && strings.ContainsAny(path, "\r\n") {
		return fmt.Errorf("path contains a newline; use --null or --json for unambiguous output")
	}
	return nil
}

func writeJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
