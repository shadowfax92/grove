package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"grove/internal/shadow"

	"github.com/spf13/cobra"
)

func TestShadowCleanupOptionsRejectInvalidFlags(t *testing.T) {
	cmd := newShadowCleanupCmdForTest()
	if err := cmd.Flags().Set("all", "true"); err != nil {
		t.Fatalf("Set(all) error = %v", err)
	}
	if err := cmd.Flags().Set("inactive", "1h"); err != nil {
		t.Fatalf("Set(inactive) error = %v", err)
	}

	if _, err := shadowCleanupOptionsFromFlags(cmd); err == nil {
		t.Fatal("shadowCleanupOptionsFromFlags() error = nil, want invalid combination")
	}
}

func TestShadowCleanupOptionsParsesInactiveThreshold(t *testing.T) {
	cmd := newShadowCleanupCmdForTest()
	if err := cmd.Flags().Set("inactive", "1d"); err != nil {
		t.Fatalf("Set(inactive) error = %v", err)
	}

	got, err := shadowCleanupOptionsFromFlags(cmd)
	if err != nil {
		t.Fatalf("shadowCleanupOptionsFromFlags() error = %v", err)
	}
	if got.InactiveOlderThan != 24*time.Hour {
		t.Fatalf("InactiveOlderThan = %v, want %v", got.InactiveOlderThan, 24*time.Hour)
	}
}

func TestPrintShadowCleanupReportDryRun(t *testing.T) {
	var buf bytes.Buffer

	printShadowCleanupReport(&buf, shadow.CleanupReport{
		Matched: []shadow.CleanupCandidate{{
			SessionName:  "gs/sh/1",
			Reason:       shadow.CleanupReasonOrphan,
			LastActiveAt: time.Now().Add(-2 * time.Hour),
		}},
	}, true)

	out := buf.String()
	if !strings.Contains(out, "Would remove 1 shadow sessions:") {
		t.Fatalf("dry-run output missing header: %q", out)
	}
	if !strings.Contains(out, "gs/sh/1") {
		t.Fatalf("dry-run output missing session name: %q", out)
	}
}

func TestPrintShadowCleanupReportNoMatches(t *testing.T) {
	var buf bytes.Buffer

	printShadowCleanupReport(&buf, shadow.CleanupReport{}, true)

	if got := buf.String(); !strings.Contains(got, "No shadow sessions matched cleanup criteria.") {
		t.Fatalf("unexpected output: %q", got)
	}
}

func newShadowCleanupCmdForTest() *cobra.Command {
	cmd := &cobra.Command{Use: shadowCleanupCmd.Use}
	cmd.Flags().Bool("dry-run", false, "")
	cmd.Flags().Bool("all", false, "")
	cmd.Flags().String("inactive", "", "")
	return cmd
}
