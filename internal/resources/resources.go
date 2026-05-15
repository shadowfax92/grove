package resources

import (
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

type Pane struct {
	Target      string
	Session     string
	WindowIndex int
	WindowName  string
	PaneIndex   int
	PID         int
	Command     string
	Path        string
}

type Process struct {
	PID     int
	PPID    int
	CPU     float64
	RSSKB   int64
	Command string
}

type WindowUsage struct {
	Target       string
	Session      string
	WindowIndex  int
	WindowName   string
	Shadow       bool
	PaneCount    int
	ProcessCount int
	CPU          float64
	RSSKB        int64
	TopProcess   Process
	Path         string
}

// CollectProcessTable reads the current process table in a form that can be
// joined with tmux pane root PIDs. RSS is reported in KiB by ps on macOS.
func CollectProcessTable() ([]Process, error) {
	cmd := exec.Command("ps", "-axo", "pid=,ppid=,%cpu=,rss=,command=")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("ps process table: %s (%w)", strings.TrimSpace(string(out)), err)
	}
	return ParsePSOutput(string(out))
}

// ParsePSOutput turns `ps` output into process records while preserving the
// command column, which can contain spaces.
func ParsePSOutput(raw string) ([]Process, error) {
	var processes []Process
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("parse pid %q: %w", fields[0], err)
		}
		ppid, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("parse ppid %q: %w", fields[1], err)
		}
		cpu, err := strconv.ParseFloat(fields[2], 64)
		if err != nil {
			return nil, fmt.Errorf("parse cpu %q: %w", fields[2], err)
		}
		rssKB, err := strconv.ParseInt(fields[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse rss %q: %w", fields[3], err)
		}
		command := ""
		if len(fields) > 4 {
			command = strings.Join(fields[4:], " ")
		}
		processes = append(processes, Process{
			PID:     pid,
			PPID:    ppid,
			CPU:     cpu,
			RSSKB:   rssKB,
			Command: command,
		})
	}
	return processes, nil
}

// BuildWindowUsages aggregates each tmux window by walking every pane root's
// descendant process tree and de-duplicating PIDs within the window.
func BuildWindowUsages(panes []Pane, processes []Process) []WindowUsage {
	childrenByParent := make(map[int][]int)
	processByPID := make(map[int]Process, len(processes))
	for _, process := range processes {
		processByPID[process.PID] = process
		childrenByParent[process.PPID] = append(childrenByParent[process.PPID], process.PID)
	}

	usageByTarget := make(map[string]*WindowUsage)
	seenByTarget := make(map[string]map[int]bool)
	for _, pane := range panes {
		target := windowTarget(pane.Session, pane.WindowIndex)
		usage := usageByTarget[target]
		if usage == nil {
			usage = &WindowUsage{
				Target:      target,
				Session:     pane.Session,
				WindowIndex: pane.WindowIndex,
				WindowName:  pane.WindowName,
				Shadow:      strings.HasPrefix(pane.Session, "gs/"),
				Path:        pane.Path,
			}
			usageByTarget[target] = usage
			seenByTarget[target] = make(map[int]bool)
		}
		usage.PaneCount++
		if usage.Path == "" && pane.Path != "" {
			usage.Path = pane.Path
		}
		if pane.PID <= 0 {
			continue
		}
		for _, pid := range descendantPIDs(pane.PID, childrenByParent) {
			if seenByTarget[target][pid] {
				continue
			}
			seenByTarget[target][pid] = true
			process, ok := processByPID[pid]
			if !ok {
				continue
			}
			usage.ProcessCount++
			usage.CPU += process.CPU
			usage.RSSKB += process.RSSKB
			if processMoreExpensive(process, usage.TopProcess) {
				usage.TopProcess = process
			}
		}
	}

	usages := make([]WindowUsage, 0, len(usageByTarget))
	for _, usage := range usageByTarget {
		usages = append(usages, *usage)
	}
	sort.SliceStable(usages, func(i, j int) bool {
		if usages[i].RSSKB != usages[j].RSSKB {
			return usages[i].RSSKB > usages[j].RSSKB
		}
		if usages[i].CPU != usages[j].CPU {
			return usages[i].CPU > usages[j].CPU
		}
		return usages[i].Target < usages[j].Target
	})
	return usages
}

func windowTarget(session string, index int) string {
	return fmt.Sprintf("%s:%d", session, index)
}

func descendantPIDs(root int, childrenByParent map[int][]int) []int {
	var result []int
	stack := []int{root}
	seen := map[int]bool{}
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		result = append(result, pid)
		stack = append(stack, childrenByParent[pid]...)
	}
	return result
}

func processMoreExpensive(candidate, current Process) bool {
	if current.PID == 0 {
		return true
	}
	if candidate.RSSKB != current.RSSKB {
		return candidate.RSSKB > current.RSSKB
	}
	return candidate.CPU > current.CPU
}
