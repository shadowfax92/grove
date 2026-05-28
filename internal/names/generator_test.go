package names

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateBranchFormat(t *testing.T) {
	branch := GenerateBranch(nil)

	if !strings.HasPrefix(branch, "fix/") {
		t.Fatalf("GenerateBranch() = %q, want fix/ prefix", branch)
	}
	suffix := "-" + time.Now().Format("02-01-06")
	if !strings.HasSuffix(branch, suffix) {
		t.Fatalf("GenerateBranch() = %q, want %q suffix", branch, suffix)
	}
	animal := strings.TrimSuffix(strings.TrimPrefix(branch, "fix/"), suffix)
	if animal == "" {
		t.Fatalf("GenerateBranch() = %q, missing animal segment", branch)
	}
}

func TestGenerateBranchAvoidsExisting(t *testing.T) {
	first := GenerateBranch(nil)
	second := GenerateBranch([]string{first})

	if first == second {
		t.Fatalf("GenerateBranch() returned %q twice despite it being in existing", first)
	}
}
