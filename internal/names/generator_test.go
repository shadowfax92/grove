package names

import (
	"regexp"
	"testing"
)

func TestGenerateBranchFormat(t *testing.T) {
	branch := GenerateBranch(nil)

	pattern := regexp.MustCompile(`^feat/[a-z][a-z0-9]*$`)
	if !pattern.MatchString(branch) {
		t.Fatalf("GenerateBranch() = %q, want feat/<animal>", branch)
	}
}

func TestGenerateBranchAvoidsExisting(t *testing.T) {
	first := GenerateBranch(nil)
	second := GenerateBranch([]string{first})

	if first == second {
		t.Fatalf("GenerateBranch() returned %q twice despite it being in existing", first)
	}
}
