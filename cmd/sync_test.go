package cmd

import (
	"bytes"
	"strings"
	"testing"

	"grove/internal/syncfile"
)

func TestWriteApplySummaryIncludesCountsAndReasons(t *testing.T) {
	results := []syncfile.ApplyResult{
		{Repo: syncfile.Repo{Group: "clis", Entry: syncfile.Entry{Name: "a"}}, Status: syncfile.ApplyCloned},
		{Repo: syncfile.Repo{Group: "clis", Entry: syncfile.Entry{Name: "b"}}, Status: syncfile.ApplyPresent},
		{Repo: syncfile.Repo{Group: "clis", Entry: syncfile.Entry{Name: "c"}}, Status: syncfile.ApplyFailed, Reason: "occupied"},
	}
	var out bytes.Buffer
	counts := writeApplySummary(&out, results)
	if counts.Cloned != 1 || counts.Present != 1 || counts.Failed != 1 {
		t.Fatalf("counts = %#v", counts)
	}
	for _, want := range []string{"Cloned (1)", "Already present (1)", "Failed (1)", "clis/c: occupied", "1 cloned, 1 already present, 1 failed"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("summary missing %q:\n%s", want, out.String())
		}
	}
}
