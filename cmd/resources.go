package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	resourceusage "grove/internal/resources"
	"grove/internal/tmux"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/spf13/cobra"
)

var (
	listResourcePanes        = tmux.ListPaneInfo
	collectResourceProcesses = resourceusage.CollectProcessTable
	killResourceWindow       = tmux.KillWindow
)

func init() {
	resourcesCmd.Flags().Bool("cleanup", false, "Pick expensive tmux windows via fzf and kill them")
	resourcesCmd.Flags().BoolP("force", "f", false, "Skip confirmation in cleanup mode")
	rootCmd.AddCommand(resourcesCmd)
}

var resourcesCmd = &cobra.Command{
	Use:         "resources",
	Aliases:     []string{"res", "usage"},
	Annotations: map[string]string{"group": "Workspaces:"},
	Short:       "Show tmux window CPU and memory usage",
	Long: `Show aggregate CPU and memory usage for tmux windows.

Each row includes all descendant processes for every pane in the window, so
server processes launched inside a pane are counted with that tmux window.

  grove resources
  grove resources --cleanup   — pick expensive windows via fzf and kill them`,
	RunE: func(cmd *cobra.Command, args []string) error {
		usages, err := collectWindowResourceUsages()
		if err != nil {
			return err
		}

		cleanup, _ := cmd.Flags().GetBool("cleanup")
		if !cleanup {
			printResourceTable(cmd.OutOrStdout(), usages)
			return nil
		}

		if len(usages) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "No tmux windows found.")
			return nil
		}
		selected, err := pickResourceCleanupFzf(usages)
		if err != nil {
			return err
		}
		if len(selected) == 0 {
			return nil
		}

		force, _ := cmd.Flags().GetBool("force")
		if !force && !confirmResourceCleanup(selected) {
			fmt.Fprintln(cmd.OutOrStdout(), "Cancelled.")
			return nil
		}
		return cleanupResourceWindows(cmd.OutOrStdout(), selected)
	},
}

// collectWindowResourceUsages joins tmux pane roots with the OS process table
// so each window is charged for the processes currently running under it.
func collectWindowResourceUsages() ([]resourceusage.WindowUsage, error) {
	tmuxPanes, err := listResourcePanes()
	if err != nil {
		return nil, err
	}
	if len(tmuxPanes) == 0 {
		return nil, nil
	}

	processes, err := collectResourceProcesses()
	if err != nil {
		return nil, err
	}

	panes := make([]resourceusage.Pane, 0, len(tmuxPanes))
	for _, pane := range tmuxPanes {
		panes = append(panes, resourceusage.Pane{
			Target:      pane.Target,
			Session:     pane.Session,
			WindowIndex: pane.WindowIndex,
			WindowName:  pane.WindowName,
			PaneIndex:   pane.PaneIndex,
			PID:         pane.PID,
			Command:     pane.Command,
			Path:        pane.Path,
		})
	}
	return resourceusage.BuildWindowUsages(panes, processes), nil
}

func printResourceTable(w io.Writer, usages []resourceusage.WindowUsage) {
	if len(usages) == 0 {
		fmt.Fprintln(w, "No tmux windows found.")
		return
	}

	rows := make([][]string, 0, len(usages))
	for _, usage := range usages {
		session := usage.Session
		if usage.Shadow {
			session = lipgloss.NewStyle().Foreground(clrYellow).Render(session)
		}
		rows = append(rows, []string{
			resourceUsageType(usage),
			formatResourceRSS(usage.RSSKB),
			formatResourceCPU(usage.CPU),
			strconv.Itoa(usage.ProcessCount),
			strconv.Itoa(usage.PaneCount),
			session,
			resourceWindowName(usage),
			resourceTopCommand(usage.TopProcess),
			usage.Path,
		})
	}

	t := table.New().
		Border(lipgloss.HiddenBorder()).
		Headers("TYPE", "MEM", "CPU", "PROCS", "PANES", "SESSION", "WINDOW", "TOP PROCESS", "PATH").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().PaddingRight(2)
			if row == table.HeaderRow {
				return s.Bold(true).Faint(true)
			}
			return s
		})

	fmt.Fprintln(w, t)
}

func pickResourceCleanupFzf(usages []resourceusage.WindowUsage) ([]resourceusage.WindowUsage, error) {
	lines := make([]string, 0, len(usages))
	for i, usage := range usages {
		lines = append(lines, fmt.Sprintf("%d\t%s\t%s\t%-32s\t%-24s\t%s",
			i,
			formatResourceRSS(usage.RSSKB),
			formatResourceCPU(usage.CPU),
			resourceCleanupTarget(usage),
			resourceTopCommand(usage.TopProcess),
			usage.Path,
		))
	}

	fzfCmd := exec.Command("fzf",
		"--multi",
		"--prompt", "cleanup resources > ",
		"--header", "Select tmux windows to kill (Tab to multi-select)",
		"--height", "100%",
		"--reverse",
		"--delimiter", "\t",
		"--with-nth", "2..",
	)
	fzfCmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
	fzfCmd.Stderr = os.Stderr

	out, err := fzfCmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 130 {
			return nil, ErrCancelled
		}
		return nil, fmt.Errorf("fzf failed: %w", err)
	}
	return parseResourceCleanupSelection(usages, string(out)), nil
}

func parseResourceCleanupSelection(usages []resourceusage.WindowUsage, raw string) []resourceusage.WindowUsage {
	var selected []resourceusage.WindowUsage
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		idx, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil || idx < 0 || idx >= len(usages) {
			continue
		}
		selected = append(selected, usages[idx])
	}
	return selected
}

func confirmResourceCleanup(selected []resourceusage.WindowUsage) bool {
	if len(selected) == 1 {
		fmt.Printf("Kill tmux window %s? [y/N] ", selected[0].Target)
	} else {
		fmt.Printf("Kill %d tmux windows?\n", len(selected))
		for _, usage := range selected {
			fmt.Printf("  %s  %s  %s\n", usage.Target, formatResourceRSS(usage.RSSKB), resourceTopCommand(usage.TopProcess))
		}
		fmt.Print("[y/N] ")
	}
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	return strings.TrimSpace(strings.ToLower(answer)) == "y"
}

func cleanupResourceWindows(w io.Writer, selected []resourceusage.WindowUsage) error {
	var failed []string
	for _, usage := range selected {
		if err := killResourceWindow(usage.Target); err != nil {
			failed = append(failed, fmt.Sprintf("%s: %v", usage.Target, err))
			continue
		}
		fmt.Fprintf(w, "Killed %s (%s, %s)\n", usage.Target, formatResourceRSS(usage.RSSKB), formatResourceCPU(usage.CPU))
	}
	if len(failed) > 0 {
		return fmt.Errorf("failed to kill %d tmux windows: %s", len(failed), strings.Join(failed, "; "))
	}
	return nil
}

func formatResourceRSS(kb int64) string {
	switch {
	case kb >= 1024*1024:
		return fmt.Sprintf("%.1f GB", float64(kb)/(1024*1024))
	case kb >= 1024:
		return fmt.Sprintf("%.1f MB", float64(kb)/1024)
	default:
		return fmt.Sprintf("%d KB", kb)
	}
}

func formatResourceCPU(cpu float64) string {
	return fmt.Sprintf("%.1f%%", cpu)
}

func resourceUsageType(usage resourceusage.WindowUsage) string {
	if usage.Shadow {
		return "shadow"
	}
	return "tmux"
}

func resourceCleanupTarget(usage resourceusage.WindowUsage) string {
	if usage.Shadow {
		return usage.Target + " [shadow]"
	}
	return usage.Target
}

func resourceWindowName(usage resourceusage.WindowUsage) string {
	if usage.WindowName == "" {
		return strconv.Itoa(usage.WindowIndex)
	}
	return fmt.Sprintf("%d:%s", usage.WindowIndex, usage.WindowName)
}

func resourceTopCommand(process resourceusage.Process) string {
	if process.Command == "" {
		return "-"
	}
	if len(process.Command) <= 64 {
		return process.Command
	}
	return process.Command[:61] + "..."
}
