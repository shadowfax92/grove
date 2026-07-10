package cmd

import (
	"strings"
	"testing"

	"grove/internal/syncfile"
)

func TestRenderExportPickerInputShowsRelativePathAndOrigin(t *testing.T) {
	candidates := []syncfile.Candidate{
		{Relative: "hacks/tools/thing", Group: "hacks", Name: "tools/thing", URL: "https://example.com/thing.git"},
		{Relative: "clis/grove", Group: "clis", Name: "grove", URL: "git@example.com:acme/grove.git"},
	}
	lookup := make(map[string]syncfile.Candidate)
	input := renderExportPickerInput(candidates, lookup)
	lines := strings.Split(input, "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "clis/grove") || !strings.Contains(lines[0], "git@example.com:acme/grove.git") {
		t.Fatalf("picker input = %q", input)
	}
	if got := lookup["clis/grove"].URL; got != "git@example.com:acme/grove.git" {
		t.Fatalf("lookup URL = %q", got)
	}
}
