// Package inuse takes one process-cwd snapshot for a whole inventory. Callers
// must distinguish an unavailable snapshot from a worktree with no processes.
package inuse

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

type State string

const (
	InUse   State = "in_use"
	Free    State = "free"
	Unknown State = "unknown"
)

type Process struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
}

// Result separates a proven empty match from unavailable process visibility.
type Result struct {
	State     State     `json:"state"`
	Processes []Process `json:"processes,omitempty"`
	Error     string    `json:"error,omitempty"`
}

type processCWD struct {
	Process
	path string
}

// Snapshot is immutable after collection and safe for concurrent lookups. A zero
// Snapshot is unknown; a failed collector can never authorize later cleanup.
type Snapshot struct {
	processes []processCWD
	known     bool
	err       error
}

func Take(ctx context.Context) Snapshot {
	processes, err := collect(ctx)
	if err != nil {
		return Snapshot{err: err}
	}
	return Snapshot{processes: processes, known: true}
}

func (s Snapshot) Lookup(path string) Result {
	if !s.known {
		message := "process snapshot unavailable"
		if s.err != nil {
			message = s.err.Error()
		}
		return Result{State: Unknown, Error: message}
	}
	root, err := canonical(path)
	if err != nil {
		return Result{State: Unknown, Error: fmt.Sprintf("resolving worktree: %v", err)}
	}
	result := Result{State: Free}
	seen := make(map[int]bool)
	for _, process := range s.processes {
		relative, err := filepath.Rel(root, process.path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			result.State = InUse
			if !seen[process.PID] {
				result.Processes = append(result.Processes, process.Process)
				seen[process.PID] = true
			}
		}
	}
	sort.Slice(result.Processes, func(i, j int) bool { return result.Processes[i].PID < result.Processes[j].PID })
	return result
}

func canonical(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(path)
}
