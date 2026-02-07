package tui

import (
	"fmt"
	"strings"

	"grove/internal/config"
	"grove/internal/state"
)

type NodeKind int

const (
	NodeRepo      NodeKind = iota
	NodeWorkspace
)

type TreeNode struct {
	Kind        NodeKind
	RepoName    string
	Workspace   *state.Workspace
	DisplayName string
}

func (n TreeNode) IsRepo() bool {
	return n.Kind == NodeRepo
}

func buildTree(st *state.State, cfg *config.Config, currentSession string) []TreeNode {
	var nodes []TreeNode

	// Group worktree workspaces by repo, in config order
	repoWorkspaces := make(map[string][]state.Workspace)
	for _, ws := range st.Workspaces {
		if ws.Type == "worktree" {
			repoWorkspaces[ws.Repo] = append(repoWorkspaces[ws.Repo], ws)
		}
	}

	for _, repo := range cfg.Repos {
		wsList := repoWorkspaces[repo.Name]
		if len(wsList) == 0 {
			continue
		}
		nodes = append(nodes, TreeNode{
			Kind:        NodeRepo,
			RepoName:    repo.Name,
			DisplayName: repo.Name,
		})
		for i := range wsList {
			nodes = append(nodes, TreeNode{
				Kind:        NodeWorkspace,
				RepoName:    repo.Name,
				Workspace:   &wsList[i],
				DisplayName: wsList[i].Branch,
			})
		}
	}

	// Also include repos from state that might not be in config order
	// (already handled above via config order)

	// Plain workspaces at the bottom
	for i := range st.Workspaces {
		ws := &st.Workspaces[i]
		if ws.Type == "plain" {
			nodes = append(nodes, TreeNode{
				Kind:        NodeWorkspace,
				Workspace:   ws,
				DisplayName: ws.Name,
			})
		}
	}

	return nodes
}

func renderTree(nodes []TreeNode, cursor int, expanded map[string]bool, currentSession string, filter string, styles Styles) string {
	var b strings.Builder

	visible := visibleNodes(nodes, expanded, filter)

	for i, vn := range visible {
		node := vn.node
		isCursor := vn.originalIdx == cursor

		line := ""
		switch node.Kind {
		case NodeRepo:
			count := countRepoChildren(nodes, node.RepoName, filter)
			arrow := "▾"
			if !expanded[node.RepoName] {
				arrow = "▸"
			}
			label := fmt.Sprintf(" %s %s (%d)", arrow, node.DisplayName, count)
			if isCursor {
				line = styles.Cursor.Render(">" + label)
			} else {
				line = styles.Repo.Render(" " + label)
			}

		case NodeWorkspace:
			isActive := node.Workspace != nil && node.Workspace.SessionName == currentSession
			hasNotif := node.Workspace != nil && node.Workspace.Notification != ""
			prefix := "  "
			if node.RepoName != "" {
				prefix = "    "
			}

			marker := " "
			if isActive {
				marker = "●"
			}

			label := node.DisplayName
			badge := ""
			if hasNotif {
				badge = " " + styles.Notification.Render("★")
			}

			if isCursor {
				line = styles.Cursor.Render(fmt.Sprintf(">%s%s %s", prefix[1:], marker, label)) + badge
			} else if isActive {
				line = styles.Active.Render(fmt.Sprintf("%s%s %s", prefix, marker, label)) + badge
			} else {
				line = styles.Workspace.Render(fmt.Sprintf("%s%s %s", prefix, marker, label)) + badge
			}
		}

		b.WriteString(line)
		if i < len(visible)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

type visibleNode struct {
	node        TreeNode
	originalIdx int
}

func visibleNodes(nodes []TreeNode, expanded map[string]bool, filter string) []visibleNode {
	var result []visibleNode
	skipRepo := ""

	for i, node := range nodes {
		if node.Kind == NodeRepo {
			skipRepo = ""
			if filter != "" {
				if !repoHasMatchingChild(nodes, node.RepoName, filter) {
					skipRepo = node.RepoName
					continue
				}
			}
			result = append(result, visibleNode{node, i})
			if !expanded[node.RepoName] {
				skipRepo = node.RepoName
			}
			continue
		}

		if skipRepo != "" && node.RepoName == skipRepo {
			continue
		}

		if filter != "" && !matchesFilter(node, filter) {
			continue
		}

		result = append(result, visibleNode{node, i})
	}

	return result
}

func repoHasMatchingChild(nodes []TreeNode, repoName, filter string) bool {
	for _, n := range nodes {
		if n.Kind == NodeWorkspace && n.RepoName == repoName && matchesFilter(n, filter) {
			return true
		}
	}
	return false
}

func matchesFilter(node TreeNode, filter string) bool {
	if filter == "" {
		return true
	}
	return strings.Contains(strings.ToLower(node.DisplayName), strings.ToLower(filter))
}

func countRepoChildren(nodes []TreeNode, repoName, filter string) int {
	count := 0
	for _, n := range nodes {
		if n.Kind == NodeWorkspace && n.RepoName == repoName {
			if matchesFilter(n, filter) {
				count++
			}
		}
	}
	return count
}

// findNodeAtCursor maps a cursor position to the actual index in the full nodes slice
func findVisibleCursorIndex(nodes []TreeNode, expanded map[string]bool, filter string, cursor int) int {
	visible := visibleNodes(nodes, expanded, filter)
	for i, vn := range visible {
		if vn.originalIdx == cursor {
			return i
		}
	}
	return -1
}

func nextVisibleCursor(nodes []TreeNode, expanded map[string]bool, filter string, cursor int, direction int) int {
	visible := visibleNodes(nodes, expanded, filter)
	if len(visible) == 0 {
		return cursor
	}

	currentVisIdx := -1
	for i, vn := range visible {
		if vn.originalIdx == cursor {
			currentVisIdx = i
			break
		}
	}

	if currentVisIdx == -1 {
		return visible[0].originalIdx
	}

	next := currentVisIdx + direction
	if next < 0 {
		next = 0
	}
	if next >= len(visible) {
		next = len(visible) - 1
	}

	return visible[next].originalIdx
}
