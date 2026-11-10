package inuse

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestSnapshotFindsChildProcess(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("cwd snapshots unsupported")
	}
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	child := exec.Command("sleep", "30")
	child.Dir = nested
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	snapshot := Take(ctx)
	for _, path := range []string{root, nested, alias} {
		got := snapshot.Lookup(path)
		if got.State != InUse {
			t.Fatalf("lookup %s: %+v", path, got)
		}
		found := false
		for _, process := range got.Processes {
			if process.PID == child.Process.Pid && process.Command != "" {
				found = true
			}
		}
		if !found {
			t.Fatalf("child missing: %+v", got)
		}
	}
	if got := snapshot.Lookup(t.TempDir()); got.State != Free {
		t.Fatalf("unrelated directory: %+v", got)
	}
}
func TestUnknownIsDistinctFromFree(t *testing.T) {
	if got := (Snapshot{}).Lookup(t.TempDir()); got.State != Unknown || got.Error == "" {
		t.Fatalf("zero snapshot: %+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := Take(ctx).Lookup(t.TempDir()); got.State != Unknown || got.Error == "" {
		t.Fatalf("failed snapshot: %+v", got)
	}
}
func TestLookupDoesNotMatchPathPrefix(t *testing.T) {
	root := t.TempDir()
	sibling := root + "-sibling"
	snapshot := Snapshot{known: true, processes: []processCWD{{Process: Process{PID: 1, Command: "fixture"}, path: sibling}}}
	if got := snapshot.Lookup(root); got.State != Free {
		t.Fatalf("prefix collision: %+v", got)
	}
}
