package cmd

import (
	"fmt"
	"strings"

	"grove/internal/catalog"
	"grove/internal/inventory"

	"github.com/spf13/cobra"
)

type listDocument struct {
	Version      int              `json:"version"`
	Repositories []listRepository `json:"repositories"`
}

type listRepository struct {
	Name          string         `json:"name"`
	Aliases       []string       `json:"aliases"`
	Path          string         `json:"path"`
	DefaultBranch string         `json:"default_branch"`
	Worktrees     []listWorktree `json:"worktrees"`
}

type listWorktree struct {
	Branch      string `json:"branch,omitempty"`
	Head        string `json:"head"`
	Path        string `json:"path"`
	Main        bool   `json:"main"`
	Detached    bool   `json:"detached"`
	Locked      bool   `json:"locked"`
	LockReason  string `json:"lock_reason,omitempty"`
	Prunable    bool   `json:"prunable"`
	Dirty       *bool  `json:"dirty,omitempty"`
	Ahead       *int   `json:"ahead,omitempty"`
	Behind      *int   `json:"behind,omitempty"`
	StatusError string `json:"status_error,omitempty"`
}

func (a *application) listCommand() *cobra.Command {
	var status bool
	command := &cobra.Command{
		Use:   "list",
		Short: "List repositories and worktrees",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runList(cmd, status)
		},
	}
	command.Flags().BoolVar(&status, "status", false, "Check dirty and ahead/behind status")
	return command
}

func (a *application) runList(cmd *cobra.Command, includeStatus bool) error {
	context, err := a.loadContext(cmd)
	if err != nil {
		return err
	}
	document := buildListDocument(context.catalog, context.inventory, includeStatus)
	if a.jsonOutput {
		return writeJSON(cmd, document)
	}
	if len(document.Repositories) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No repositories. Run 'grove new' inside a Git repository.")
		return nil
	}
	for index, repository := range document.Repositories {
		if index > 0 {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		aliases := aliasesSuffix(repository.Name, repository.Aliases)
		fmt.Fprintf(cmd.OutOrStdout(), "%s%s  %s\n", repository.Name, aliases, repository.Path)
		for worktreeIndex, worktree := range repository.Worktrees {
			connector := "├──"
			if worktreeIndex == len(repository.Worktrees)-1 {
				connector = "└──"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s %-28s %s%s\n", connector, worktreeLabel(worktree), worktree.Path, statusSuffix(worktree))
		}
	}
	return nil
}

func buildListDocument(cat *catalog.Catalog, inv *inventory.Inventory, includeStatus bool) listDocument {
	document := listDocument{Version: 1, Repositories: make([]listRepository, 0, len(cat.Repositories))}
	byRepository := make(map[*catalog.Repository][]*inventory.Entry)
	for _, entry := range inv.Entries {
		byRepository[entry.Repository] = append(byRepository[entry.Repository], entry)
	}
	for _, repository := range cat.Repositories {
		item := listRepository{
			Name:          repository.Name,
			Aliases:       repository.Aliases(),
			Path:          repository.Git.MainPath,
			DefaultBranch: repository.DefaultBranch,
			Worktrees:     []listWorktree{},
		}
		for _, entry := range byRepository[repository] {
			worktree := listWorktree{
				Branch:     entry.Worktree.Branch,
				Head:       entry.Worktree.Head,
				Path:       entry.Worktree.Path,
				Main:       entry.Worktree.Main,
				Detached:   entry.Worktree.Detached,
				Locked:     entry.Worktree.Locked,
				LockReason: entry.Worktree.LockReason,
				Prunable:   entry.Worktree.Prunable,
			}
			if includeStatus && !worktree.Prunable {
				dirty, err := repository.Git.Dirty(worktree.Path)
				if err != nil {
					worktree.StatusError = err.Error()
				} else {
					worktree.Dirty = &dirty
					ahead, behind, err := repository.Git.AheadBehind(worktree.Path, repository.DefaultBranch)
					if err != nil {
						worktree.StatusError = err.Error()
					} else {
						worktree.Ahead = &ahead
						worktree.Behind = &behind
					}
				}
			}
			item.Worktrees = append(item.Worktrees, worktree)
		}
		document.Repositories = append(document.Repositories, item)
	}
	return document
}

func aliasesSuffix(name string, aliases []string) string {
	others := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		if alias != name {
			others = append(others, alias)
		}
	}
	if len(others) == 0 {
		return ""
	}
	return " (" + strings.Join(others, ", ") + ")"
}

func worktreeLabel(worktree listWorktree) string {
	label := worktree.Branch
	if label == "" {
		label = "@" + shortSHA(worktree.Head)
	}
	if worktree.Main {
		label += " [main]"
	}
	if worktree.Locked {
		label += " [locked]"
	}
	if worktree.Prunable {
		label += " [missing]"
	}
	return label
}

func statusSuffix(worktree listWorktree) string {
	if worktree.Dirty == nil && worktree.StatusError == "" {
		return ""
	}
	var labels []string
	if worktree.Dirty != nil && *worktree.Dirty {
		labels = append(labels, "dirty")
	}
	if worktree.Ahead != nil && *worktree.Ahead > 0 {
		labels = append(labels, fmt.Sprintf("↑%d", *worktree.Ahead))
	}
	if worktree.Behind != nil && *worktree.Behind > 0 {
		labels = append(labels, fmt.Sprintf("↓%d", *worktree.Behind))
	}
	if worktree.StatusError != "" {
		labels = append(labels, "status unavailable")
	}
	if len(labels) == 0 {
		labels = append(labels, "clean")
	}
	return "  [" + strings.Join(labels, ", ") + "]"
}

func shortSHA(head string) string {
	if len(head) > 8 {
		return head[:8]
	}
	return head
}
