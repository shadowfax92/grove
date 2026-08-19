package picker

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

var ErrCancelled = errors.New("selection cancelled")

type Item struct {
	Key   string
	Label string
}

func Select(prompt string, items []Item) (string, error) {
	selected, err := selectItems(prompt, items, false)
	if err != nil {
		return "", err
	}
	return selected[0], nil
}

func SelectMany(prompt string, items []Item) ([]string, error) {
	return selectItems(prompt, items, true)
}

func selectItems(prompt string, items []Item, multi bool) ([]string, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("no worktrees")
	}
	args := []string{"--read0", "--print0", "--delimiter=\t", "--with-nth=2..", "--prompt", prompt}
	if multi {
		args = append(args, "--multi", "--header", "tab/shift-tab select · enter confirm")
	}
	cmd := exec.Command("fzf", args...)
	cmd.Stdin = bytes.NewReader(encodeItems(items))
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && (exitErr.ExitCode() == 1 || exitErr.ExitCode() == 130) {
			return nil, ErrCancelled
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, fmt.Errorf("fzf is required for interactive selection")
		}
		return nil, fmt.Errorf("fzf: %w", err)
	}
	return decodeSelections(out, items)
}

func Interactive() bool {
	stdin, err := os.Stdin.Stat()
	if err != nil || stdin.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	stderr, err := os.Stderr.Stat()
	return err == nil && stderr.Mode()&os.ModeCharDevice != 0
}

func encodeItems(items []Item) []byte {
	var encoded bytes.Buffer
	for index, item := range items {
		label := strings.NewReplacer("\x00", " ", "\n", " ", "\r", " ", "\t", " ").Replace(item.Label)
		fmt.Fprintf(&encoded, "%d\t%s", index, label)
		encoded.WriteByte(0)
	}
	return encoded.Bytes()
}

func decodeSelection(output []byte, items []Item) (string, error) {
	record := bytes.TrimSuffix(output, []byte{0})
	separator := bytes.IndexByte(record, '\t')
	if separator < 1 {
		return "", fmt.Errorf("invalid fzf selection")
	}
	index, err := strconv.Atoi(string(record[:separator]))
	if err != nil || index < 0 || index >= len(items) {
		return "", fmt.Errorf("invalid fzf selection index %q", record[:separator])
	}
	return items[index].Key, nil
}

func decodeSelections(output []byte, items []Item) ([]string, error) {
	records := bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0})
	selected := make([]string, 0, len(records))
	for _, record := range records {
		if len(record) == 0 {
			continue
		}
		key, err := decodeSelection(record, items)
		if err != nil {
			return nil, err
		}
		selected = append(selected, key)
	}
	if len(selected) == 0 {
		return nil, ErrCancelled
	}
	return selected, nil
}
