package names

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestGenerateBranchFormat(t *testing.T) {
	branch := generateBranchAt(nil, time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC))

	pattern := regexp.MustCompile(`^feat/07-30-([a-z]+)-([a-z]+)$`)
	match := pattern.FindStringSubmatch(branch)
	if match == nil {
		t.Fatalf("GenerateBranch() = %q, want feat/07-30-<adjective>-<animal>", branch)
	}
	if !contains(warmAdjectives, match[1]) {
		t.Fatalf("GenerateBranch() adjective = %q, want curated warm adjective", match[1])
	}
	if !contains(cuteAnimals, match[2]) {
		t.Fatalf("GenerateBranch() animal = %q, want curated cute animal", match[2])
	}
}

func TestGenerateBranchUsesPacificDateBoundary(t *testing.T) {
	tests := []struct {
		name   string
		now    time.Time
		prefix string
	}{
		{"PDT before midnight", time.Date(2026, time.July, 30, 6, 59, 0, 0, time.UTC), "feat/07-29-"},
		{"PDT at midnight", time.Date(2026, time.July, 30, 7, 0, 0, 0, time.UTC), "feat/07-30-"},
		{"PST before midnight", time.Date(2026, time.January, 15, 7, 59, 0, 0, time.UTC), "feat/01-14-"},
		{"PST at midnight", time.Date(2026, time.January, 15, 8, 0, 0, 0, time.UTC), "feat/01-15-"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if branch := generateBranchAt(nil, tt.now); !strings.HasPrefix(branch, tt.prefix) {
				t.Fatalf("GenerateBranch() = %q, want prefix %q", branch, tt.prefix)
			}
		})
	}
}

func TestGenerateBranchAvoidsSameDayCollisions(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	var existing []string

	for range 100 {
		branch := generateBranchAt(existing, now)
		if contains(existing, branch) {
			t.Fatalf("GenerateBranch() reused existing branch %q", branch)
		}
		existing = append(existing, branch)
	}
}

func TestGenerateBranchFallsBackAfterExhaustion(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	existing := make([]string, 0, len(warmAdjectives)*len(cuteAnimals))
	for _, adjective := range warmAdjectives {
		for _, animal := range cuteAnimals {
			existing = append(existing, "feat/07-30-"+adjective+"-"+animal)
		}
	}

	got := generateBranchAt(existing, now)
	want := "feat/07-30-" + warmAdjectives[0] + "-" + cuteAnimals[0] + "2"
	if got != want {
		t.Fatalf("GenerateBranch() = %q, want %q after exhausting candidates", got, want)
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
