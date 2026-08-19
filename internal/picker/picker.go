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
	if len(items) == 0 {
		return "", fmt.Errorf("no worktrees")
	}
	cmd := exec.Command("fzf", "--read0", "--print0", "--delimiter=\t", "--with-nth=2..", "--prompt", prompt)
	cmd.Stdin = bytes.NewReader(encodeItems(items))
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && (exitErr.ExitCode() == 1 || exitErr.ExitCode() == 130) {
			return "", ErrCancelled
		}
		if errors.Is(err, exec.ErrNotFound) {
			return "", fmt.Errorf("fzf is required for interactive selection")
		}
		return "", fmt.Errorf("fzf: %w", err)
	}
	return decodeSelection(out, items)
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
