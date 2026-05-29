package names

import (
	"regexp"
	"testing"
)

func TestGenerateBranchFormat(t *testing.T) {
	branch := GenerateBranch(nil)

	// fix/<mmdd>-<hhmm>-<animal>
	pattern := regexp.MustCompile(`^fix/\d{4}-\d{4}-[a-z]`)
	if !pattern.MatchString(branch) {
		t.Fatalf("GenerateBranch() = %q, want fix/<mmdd>-<hhmm>-<animal>", branch)
	}
}

func TestGenerateBranchAvoidsExisting(t *testing.T) {
	first := GenerateBranch(nil)
	second := GenerateBranch([]string{first})

	if first == second {
		t.Fatalf("GenerateBranch() returned %q twice despite it being in existing", first)
	}
}
