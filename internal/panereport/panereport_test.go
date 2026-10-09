package panereport

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorktreeReportingRequiresCallerOptIn(t *testing.T) {
	for _, test := range []struct {
		name, env, pane, report string
	}{
		{name: "outside Herdr", pane: "w1:p1"},
		{name: "Herdr disabled", env: "0", pane: "w1:p1"},
		{name: "exact environment gate", env: "true", pane: "w1:p1"},
		{name: "no caller pane", env: "1"},
		{name: "launcher opted out", env: "1", pane: "w1:p1", report: "0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			binary, record := reporterScript(t, "printf invoked > \"$GROVE_TEST_REPORT\"\n")
			t.Setenv("HERDR_ENV", test.env)
			t.Setenv("HERDR_PANE_ID", test.pane)
			t.Setenv("GROVE_PANE_REPORT", test.report)
			t.Setenv("HERDR_BIN_PATH", binary)
			Worktree(context.Background(), "/worktree")
			if _, err := os.Stat(record); !os.IsNotExist(err) {
				t.Fatalf("reporter ran despite caller gate: %v", err)
			}
		})
	}
}

func TestWorktreeReportingUsesPathFallbackAndOpaquePaths(t *testing.T) {
	binary, record := reporterScript(t, "printf '%s\\0' \"$@\" > \"$GROVE_TEST_REPORT\"\n")
	t.Setenv("HERDR_BIN_PATH", "")
	t.Setenv("PATH", filepath.Dir(binary))
	Worktree(context.Background(), "/worktree with spaces/line\nbreak=$(literal)")
	got, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	want := "pane\x00report-metadata\x00w1:p1\x00--source\x00grove\x00--token\x00grove_worktree=/worktree with spaces/line\nbreak=$(literal)\x00"
	if string(got) != want {
		t.Fatalf("report arguments = %q, want %q", got, want)
	}
}

func TestWorktreeReportingHasBoundedLifetime(t *testing.T) {
	binary, _ := reporterScript(t, "exec /bin/sleep 30\n")
	t.Setenv("HERDR_BIN_PATH", binary)
	started := time.Now()
	Worktree(context.Background(), "/worktree")
	if elapsed := time.Since(started); elapsed < 800*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("reporting took %s, want approximately one second", elapsed)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started = time.Now()
	Worktree(ctx, "/worktree")
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("reporting ignored command cancellation for %s", elapsed)
	}
}

func reporterScript(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	binary, record := filepath.Join(dir, "herdr"), filepath.Join(dir, "report")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	t.Setenv("GROVE_PANE_REPORT", "")
	t.Setenv("GROVE_TEST_REPORT", record)
	return binary, record
}
