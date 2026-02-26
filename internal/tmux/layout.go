package tmux

import (
	"fmt"
	"strconv"
	"strings"

	"grove/internal/config"
)

// CreateSessionWithLayout creates a tmux session and applies a layout if provided.
func CreateSessionWithLayout(name, startDir string, layout *config.LayoutConfig) error {
	if err := NewSession(name, startDir); err != nil {
		return err
	}
	if layout == nil || len(layout.Windows) == 0 {
		return nil
	}
	return applyLayout(name, startDir, layout.Windows)
}

// ApplyLayoutToCurrentSession applies a layout to an existing session.
func ApplyLayoutToCurrentSession(sessionName, startDir string, layout *config.LayoutConfig) error {
	if layout == nil || len(layout.Windows) == 0 {
		return fmt.Errorf("layout has no windows")
	}
	return applyLayout(sessionName, startDir, layout.Windows)
}

func applyLayout(sessionName, startDir string, windows []config.WindowConfig) error {
	for i, win := range windows {
		if i == 0 {
			if _, err := run("rename-window", "-t", sessionName+":0", win.Name); err != nil {
				return fmt.Errorf("renaming window: %w", err)
			}
		} else {
			if _, err := run("new-window", "-t", sessionName, "-n", win.Name, "-c", startDir); err != nil {
				return fmt.Errorf("creating window %s: %w", win.Name, err)
			}
		}

		if err := applyPanes(sessionName, i, startDir, win); err != nil {
			return err
		}
	}

	run("select-window", "-t", sessionName+":0")
	run("select-pane", "-t", sessionName+":0.0")

	return nil
}

func applyPanes(sessionName string, windowIdx int, startDir string, win config.WindowConfig) error {
	if len(win.Panes) <= 1 {
		if len(win.Panes) == 1 && win.Panes[0].Cmd != "" {
			sendCommand(sessionName, windowIdx, 0, win.Panes[0].Cmd)
		}
		return nil
	}

	sizes := computeSizes(win.Panes)

	splitFlag := "-h" // horizontal = panes side by side
	if win.Split == "vertical" {
		splitFlag = "-v"
	}

	// Create panes 1..n-1 by splitting. After each split the new pane is active,
	// so the next split divides the remaining space.
	for i := 1; i < len(win.Panes); i++ {
		remainingSum := 0
		for j := i; j < len(sizes); j++ {
			remainingSum += sizes[j]
		}
		currentSum := sizes[i-1] + remainingSum
		p := remainingSum * 100 / currentSum

		target := fmt.Sprintf("%s:%d", sessionName, windowIdx)
		if _, err := run("split-window", splitFlag, "-t", target, "-p", strconv.Itoa(p), "-c", startDir); err != nil {
			return fmt.Errorf("splitting pane %d in window %s: %w", i, win.Name, err)
		}
	}

	// Send commands to each pane
	for i, pane := range win.Panes {
		if pane.Cmd != "" {
			sendCommand(sessionName, windowIdx, i, pane.Cmd)
		}
	}

	// Select first pane
	run("select-pane", "-t", fmt.Sprintf("%s:%d.0", sessionName, windowIdx))

	return nil
}

func sendCommand(sessionName string, windowIdx, paneIdx int, cmd string) {
	target := fmt.Sprintf("%s:%d.%d", sessionName, windowIdx, paneIdx)
	run("send-keys", "-t", target, "-l", cmd)
	run("send-keys", "-t", target, "Enter")
}

func computeSizes(panes []config.PaneConfig) []int {
	sizes := make([]int, len(panes))
	totalSpecified := 0
	unspecifiedCount := 0

	for i, p := range panes {
		if p.Size != "" {
			sizes[i] = parseSize(p.Size)
			totalSpecified += sizes[i]
		} else {
			unspecifiedCount++
		}
	}

	if unspecifiedCount > 0 {
		remaining := 100 - totalSpecified
		if remaining < 0 {
			remaining = 0
		}
		each := remaining / unspecifiedCount
		extra := remaining % unspecifiedCount
		for i := range sizes {
			if sizes[i] == 0 {
				sizes[i] = each
				if extra > 0 {
					sizes[i]++
					extra--
				}
			}
		}
	}

	return sizes
}

func parseSize(s string) int {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "%")
	n, _ := strconv.Atoi(s)
	return n
}
