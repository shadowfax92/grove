package inuse

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func collect(ctx context.Context) ([]processCWD, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var result []processCWD
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		root := filepath.Join("/proc", entry.Name())
		path, err := os.Readlink(filepath.Join(root, "cwd"))
		if os.IsNotExist(err) {
			continue
		} // exited process or kernel thread
		if err != nil {
			return nil, fmt.Errorf("reading process %d cwd: %w", pid, err)
		}
		command, err := os.ReadFile(filepath.Join(root, "comm"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		path, err = canonical(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		result = append(result, processCWD{Process: Process{PID: pid, Command: strings.TrimSuffix(string(command), "\n")}, path: path})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("empty process snapshot")
	}
	return result, nil
}
