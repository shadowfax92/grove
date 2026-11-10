package inuse

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func collect(ctx context.Context) ([]processCWD, error) {
	cmd := exec.CommandContext(ctx, "lsof", "-a", "-d", "cwd", "-F0pcn")
	// Bound pipe draining too: a killed helper can leave a child holding stdout.
	cmd.WaitDelay = 100 * time.Millisecond
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
	// NUL fields preserve framing; filename contents still use lsof escaping.
	// lsof inserts a newline
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
			candidates, err := lsofPaths(string(field[1:]))
			if err != nil {
				return nil, err
			}
			seenPaths := make(map[string]bool)
			for _, candidate := range candidates {
				path, err := canonical(candidate)
				// Vanished directories cannot contain an existing worktree. Escaped
				// names must be decoded before making this decision, or a live process
				// in a newline-named checkout would be silently classified as absent.
				if os.IsNotExist(err) {
					continue
				}
				if err != nil {
					return nil, fmt.Errorf("resolving process %d cwd: %w", process.PID, err)
				}
				if !seenPaths[path] {
					result = append(result, processCWD{Process: process, path: path})
					seenPaths[path] = true
				}
			}

		}
	}
	if !seen || len(result) == 0 {
		return nil, fmt.Errorf("empty process snapshot")
	}
	return result, nil
}

// lsof escapes non-printable bytes even in -F0 output, but leaves printable
// backslashes/carets literal. Retain both interpretations when ambiguous so a
// literal "\\n" directory cannot hide a process in a newline directory (or vice
// versa). If ambiguity explodes, the snapshot becomes unknown rather than free.
func lsofPaths(path string) ([]string, error) {
	if !strings.ContainsAny(path, `\^`) {
		return []string{path}, nil
	}
	variants := []string{""}
	for i := 0; i < len(path); {
		length := 1
		decoded := byte(0)
		escaped := false
		if path[i] == '\\' && i+1 < len(path) {
			switch path[i+1] {
			case 'b':
				decoded = '\b'
				escaped = true
			case 'f':
				decoded = '\f'
				escaped = true
			case 'n':
				decoded = '\n'
				escaped = true
			case 'r':
				decoded = '\r'
				escaped = true
			case 't':
				decoded = '\t'
				escaped = true
			case '\\':
				decoded = '\\'
				escaped = true
			case 'x':
				if i+3 < len(path) {
					value, err := strconv.ParseUint(path[i+2:i+4], 16, 8)
					if err == nil {
						decoded = byte(value)
						escaped = true
						length = 4
					}
				}
			}
			if escaped && length == 1 {
				length = 2
			}
		} else if path[i] == '^' && i+1 < len(path) {
			next := path[i+1]
			if next >= '@' && next <= '_' {
				decoded = next & 31
				escaped = true
				length = 2
			} else if next == '?' {
				decoded = 127
				escaped = true
				length = 2
			}
		}
		literal := path[i : i+length]
		original := len(variants)
		for j := range original {
			prefix := variants[j]
			variants[j] += literal
			if escaped && decoded != 0 {
				variants = append(variants, prefix+string(decoded))
			}
		}
		if len(variants) > 128 {
			return nil, fmt.Errorf("ambiguous escaped cwd in process snapshot")
		}
		i += length
	}
	return variants, nil
}
