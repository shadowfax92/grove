//go:build !darwin && !linux

package trash

import (
	"context"
	"fmt"
)

func directoryID(path string) (string, error) {
	return "", fmt.Errorf("fast removal is unsupported on this platform")
}
func renameNoReplace(source, destination string) error {
	return fmt.Errorf("fast removal is unsupported on this platform")
}
func deleteTree(ctx context.Context, path string) error {
	return fmt.Errorf("reaping is unsupported on this platform")
}
func (m *Manager) Resume() error { return nil }

func (m *Manager) Reap(ctx context.Context) error {
	return fmt.Errorf("reaping is unsupported on this platform")
}
func (m *Manager) ReapInherited(ctx context.Context) error { return m.Reap(ctx) }
