package cmd

import (
	"encoding/json"
	"fmt"

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
	return a.writeWorktree(cmd, entry)
}

func (a *application) pickWorktree(context *commandContext, prompt string) (*inventory.Entry, error) {
	if a.noInput || !a.dependencies.interactive() {
		return nil, fmt.Errorf("selector is required in non-interactive mode")
	}
	available := context.inventory.PickerItems()
	items := make([]picker.Item, 0, len(available))
	for _, item := range available {
		items = append(items, picker.Item{Key: item.Path, Label: item.Label})
	}
	path, err := a.dependencies.pick(prompt, items)
	if err != nil {
		return nil, err
	}
	return context.inventory.Resolve(path, context.directory)
}

func (a *application) writeWorktree(cmd *cobra.Command, entry *inventory.Entry) error {
	if !a.jsonOutput {
		fmt.Fprintln(cmd.OutOrStdout(), entry.Worktree.Path)
		return nil
	}
	return writeJSON(cmd, worktreeOutput{
		Version:    1,
		Repository: entry.Repository.Name,
		Branch:     entry.Worktree.Branch,
		Path:       entry.Worktree.Path,
		Main:       entry.Worktree.Main,
	})
}

func writeJSON(cmd *cobra.Command, value any) error {
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
