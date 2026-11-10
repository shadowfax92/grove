package inuse

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func collect(ctx context.Context) ([]processCWD, error) {
	cmd := exec.CommandContext(ctx, "lsof", "-a", "-d", "cwd", "-F0pcn")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("process snapshot: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	// lsof may return partial data with warnings. Do not call an incomplete
	// snapshot free, even when the command itself exited successfully.
	if stderr.Len() != 0 {
		return nil, fmt.Errorf("process snapshot incomplete: %s", strings.TrimSpace(stderr.String()))
	}
	return parseLSOF(out)
}

func parseLSOF(out []byte) ([]processCWD, error) {
	var result []processCWD
	var process Process
	seen := false
	// NUL fields preserve spaces and newlines in paths. lsof inserts a newline
	// between process/file sets, before the next field tag, not inside the field.
	for _, raw := range bytes.Split(out, []byte{0}) {
		field := bytes.TrimLeft(raw, "\n")
		if len(field) == 0 {
			continue
		}
		switch field[0] {
		case 'p':
			pid, err := strconv.Atoi(string(field[1:]))
			if err != nil || pid <= 0 {
				return nil, fmt.Errorf("invalid process id in lsof snapshot")
			}
			process = Process{PID: pid}
			seen = true
		case 'c':
			process.Command = string(field[1:])
		case 'n':
			if process.PID == 0 {
				return nil, fmt.Errorf("cwd without process in lsof snapshot")
			}
			path, err := canonical(string(field[1:]))
			// A vanished cwd cannot be inside a currently existing worktree.
			// Ignore exited/deleted directories; permission and other failures remain
			// unknown for the whole snapshot rather than hiding inaccessible processes.
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("resolving process %d cwd: %w", process.PID, err)
			}
			result = append(result, processCWD{Process: process, path: path})
		}
	}
	if !seen || len(result) == 0 {
		return nil, fmt.Errorf("empty process snapshot")
	}
	return result, nil
}
