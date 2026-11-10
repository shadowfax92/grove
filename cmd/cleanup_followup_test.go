package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"grove/internal/config"
	"grove/internal/inuse"
	"grove/internal/picker"
)

func setCleanupActivity(t *testing.T, path string, age time.Duration) {
	t.Helper()
	when := time.Now().Add(-age)
	index := v2GitOutput(t, path, "rev-parse", "--path-format=absolute", "--git-path", "index")
	for _, p := range []string{filepath.Join(path, "README"), index} {
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
}
func setCleanupGrace(t *testing.T, value string) {
	t.Helper()
	path, _ := config.DefaultConfigPath()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("cleanup:\n  grace_period: " + value + "\n")
	closeErr := f.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func TestFreshTriviallyMergedWorktreeStaysVisibleButUnselected(t *testing.T) {
	for _, command := range []string{"cleanup", "rm"} {
		t.Run(command, func(t *testing.T) {
			_, linked, deps := cleanupFixture(t)
			setCleanupGrace(t, "2h")
			deps.interactive = func() bool { return true }
			// The branch and creation date stay fresh. Only source/index activity ages;
			// past grace it qualifies through merged evidence, before the 3d idle cutoff.
			for _, age := range []time.Duration{0, 3 * time.Hour} {
				setCleanupActivity(t, linked, age)
				seen := false
				deps.pickMany = func(prompt string, items []picker.Item) ([]string, error) {
					if prompt == "projects > " {
						return []string{items[0].Key}, nil
					}
					if len(items) != 1 || items[0].Key != linked || !strings.Contains(items[0].Label, "merged:") {
						t.Fatalf("worktree missing or not merged: %#v", items)
					}
					if items[0].Selected != (age > 2*time.Hour) {
						t.Fatalf("activity age %s: selected=%v", age, items[0].Selected)
					}
					seen = true
					return nil, picker.ErrCancelled
				}
				_, _, err := executeV2(newRootCommand(deps), command)
				if !errors.Is(err, picker.ErrCancelled) || !seen {
					t.Fatal(seen, err)
				}
				if _, err := os.Stat(linked); err != nil {
					t.Fatal("picker cancellation removed tree", err)
				}
			}
		})
	}
}
func TestHeadlessMergedCleanupRequiresGrace(t *testing.T) {
	for _, command := range []string{"cleanup", "rm"} {
		t.Run(command, func(t *testing.T) {
			_, linked, deps := cleanupFixture(t)
			setCleanupGrace(t, "2h")
			for _, tc := range []struct {
				age      time.Duration
				inactive string
				count    int
			}{{0, "", 0}, {3 * time.Hour, "", 1}, {0, "1m", 0}, {3 * time.Hour, "4h", 0}} {
				setCleanupActivity(t, linked, tc.age)
				args := []string{"--json", command, "--merged", "--dry-run", "--forge=false"}
				if tc.inactive != "" {
					args = append(args, "--inactive-for", tc.inactive)
				}
				out, _, err := executeV2(newRootCommand(deps), args...)
				if err != nil {
					t.Fatal(err)
				}
				var doc removeOutput
				if err := json.Unmarshal([]byte(out), &doc); err != nil || len(doc.WouldRemove) != tc.count {
					t.Fatalf("%+v: %s %v", tc, out, err)
				}
			}
			setCleanupActivity(t, linked, 0)
			if _, _, err := executeV2(newRootCommand(deps), command, "--merged", "--discard", "--forge=false"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(linked); err != nil {
				t.Fatal("discard overrode grace", err)
			}
			setCleanupActivity(t, linked, 3*time.Hour)
			if _, _, err := executeV2(newRootCommand(deps), command, "--merged", "--forge=false"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(linked); !os.IsNotExist(err) {
				t.Fatal("aged merged tree was not removed", err)
			}
		})
	}
}
func TestHeadlessMergedCleanupRechecksGraceAtDetach(t *testing.T) {
	_, linked, deps := cleanupFixture(t)
	setCleanupGrace(t, "2h")
	setCleanupActivity(t, linked, 3*time.Hour)
	calls := 0
	deps.inUse = func(string) inuse.Result {
		calls++
		if calls == 3 {
			setCleanupActivity(t, linked, 0)
		}
		return inuse.Result{State: inuse.Free}
	}
	_, _, err := executeV2(newRootCommand(deps), "cleanup", "--merged", "--forge=false")
	if err == nil || !strings.Contains(err.Error(), "no longer idle") {
		t.Fatal(err)
	}
	if _, err := os.Stat(linked); err != nil {
		t.Fatal(err)
	}
}

// A channel recorder lets the test inspect progress while the collector is
// blocked, without reading a bytes.Buffer concurrently with command writes.
type cleanupProgressWriter struct{ messages chan string }

func (w cleanupProgressWriter) Write(p []byte) (int, error) {
	w.messages <- string(p)
	return len(p), nil
}
func TestCleanupReportsProgressBeforePickerAndDuringSignalWait(t *testing.T) {
	_, _, deps := cleanupFixture(t)
	gate := make(chan struct{})
	entered := make(chan struct{})
	var once sync.Once
	defer func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	}()
	deps.inUse = func(string) inuse.Result {
		once.Do(func() { close(entered) })
		<-gate
		return inuse.Result{State: inuse.Free}
	}
	deps.interactive = func() bool { return true }
	deps.pickMany = func(string, []picker.Item) ([]string, error) { return nil, picker.ErrCancelled }
	root := newRootCommand(deps)
	var stdout bytes.Buffer
	messages := make(chan string, 32)
	root.SetOut(&stdout)
	root.SetErr(cleanupProgressWriter{messages})
	root.SetArgs([]string{"cleanup", "--forge=false"})
	done := make(chan error, 1)
	go func() { done <- root.Execute() }()
	select {
	case first := <-messages:
		if !strings.Contains(first, "Discovering worktrees") {
			t.Fatal(first)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("silent before discovery")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("collector did not enter")
	}
	heartbeat := false
	deadline := time.After(3 * time.Second)
	for !heartbeat {
		select {
		case line := <-messages:
			heartbeat = strings.Contains(line, "Collecting cleanup signals:") && strings.Contains(line, "activity 0/1") && strings.Contains(line, "merges")
		case <-deadline:
			t.Fatal("silent while signals blocked")
		}
	}
	close(gate)
	select {
	case err := <-done:
		if !errors.Is(err, picker.ErrCancelled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("collector did not finish")
	}
	if stdout.Len() != 0 {
		t.Fatalf("progress corrupted stdout: %q", stdout.String())
	}
	summary := false
	for len(messages) > 0 {
		line := <-messages
		if strings.Contains(line, "Collected cleanup signals in") && strings.Contains(line, "overlapping") {
			summary = true
		}
	}
	if !summary {
		t.Fatal("no phase timing summary")
	}
}
