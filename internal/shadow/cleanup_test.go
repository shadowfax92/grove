package shadow

import (
	"errors"
	"testing"
	"time"

	"grove/internal/tmux"
)

func TestParseInactiveThresholdAcceptsDayShorthand(t *testing.T) {
	got, err := ParseInactiveThreshold("1d")
	if err != nil {
		t.Fatalf("ParseInactiveThreshold() error = %v", err)
	}
	if got != 24*time.Hour {
		t.Fatalf("ParseInactiveThreshold() = %v, want %v", got, 24*time.Hour)
	}
}

func TestParseInactiveThresholdRejectsInvalidValue(t *testing.T) {
	if _, err := ParseInactiveThreshold("nope"); err == nil {
		t.Fatal("ParseInactiveThreshold() error = nil, want parse error")
	}
}

func TestSelectCleanupCandidatesDefaultsToOrphans(t *testing.T) {
	restore := stubShadowSessionInventory(t, []shadowSessionFixture{
		{
			name:         "gs/sh/1",
			parentPane:   "%1",
			parentExists: false,
			lastActiveAt: fixtureNow().Add(-10 * time.Minute),
		},
		{
			name:         "gs/vim/2",
			parentPane:   "%2",
			parentExists: true,
			lastActiveAt: fixtureNow().Add(-48 * time.Hour),
		},
	})
	defer restore()

	got, err := SelectCleanupCandidates(CleanupOptions{})
	if err != nil {
		t.Fatalf("SelectCleanupCandidates() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(SelectCleanupCandidates()) = %d, want 1", len(got))
	}
	if got[0].SessionName != "gs/sh/1" || got[0].Reason != CleanupReasonOrphan {
		t.Fatalf("unexpected candidate: %#v", got[0])
	}
}

func TestSelectCleanupCandidatesIncludesInactiveSessions(t *testing.T) {
	restore := stubShadowSessionInventory(t, []shadowSessionFixture{
		{
			name:         "gs/sh/1",
			parentPane:   "%1",
			parentExists: true,
			lastActiveAt: fixtureNow().Add(-2 * time.Hour),
		},
		{
			name:         "gs/vim/2",
			parentPane:   "%2",
			parentExists: true,
			lastActiveAt: fixtureNow().Add(-10 * time.Minute),
		},
	})
	defer restore()

	got, err := SelectCleanupCandidates(CleanupOptions{InactiveOlderThan: time.Hour})
	if err != nil {
		t.Fatalf("SelectCleanupCandidates() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(SelectCleanupCandidates()) = %d, want 1", len(got))
	}
	if got[0].SessionName != "gs/sh/1" || got[0].Reason != CleanupReasonInactive {
		t.Fatalf("unexpected candidate: %#v", got[0])
	}
}

func TestSelectCleanupCandidatesTreatsMissingMetadataAsOrphan(t *testing.T) {
	restore := stubShadowSessionInventory(t, []shadowSessionFixture{
		{
			name:         "gs/sh/1",
			parentErr:    errors.New("missing metadata"),
			lastActiveAt: fixtureNow().Add(-10 * time.Minute),
		},
	})
	defer restore()

	got, err := SelectCleanupCandidates(CleanupOptions{})
	if err != nil {
		t.Fatalf("SelectCleanupCandidates() error = %v", err)
	}
	if len(got) != 1 || got[0].Reason != CleanupReasonOrphan {
		t.Fatalf("unexpected candidates: %#v", got)
	}
}

func TestSelectCleanupCandidatesAllModeIncludesEverything(t *testing.T) {
	restore := stubShadowSessionInventory(t, []shadowSessionFixture{
		{
			name:         "gs/sh/1",
			parentPane:   "%1",
			parentExists: false,
			lastActiveAt: fixtureNow().Add(-10 * time.Minute),
		},
		{
			name:         "gs/vim/2",
			parentPane:   "%2",
			parentExists: true,
			lastActiveAt: fixtureNow().Add(-10 * time.Minute),
		},
	})
	defer restore()

	got, err := SelectCleanupCandidates(CleanupOptions{RemoveAll: true})
	if err != nil {
		t.Fatalf("SelectCleanupCandidates() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(SelectCleanupCandidates()) = %d, want 2", len(got))
	}
}

func TestCleanupKillsMatchedSessions(t *testing.T) {
	restore := stubShadowSessionInventory(t, []shadowSessionFixture{
		{
			name:         "gs/sh/1",
			parentPane:   "%1",
			parentExists: false,
			lastActiveAt: fixtureNow().Add(-10 * time.Minute),
		},
	})
	defer restore()

	var killed []string
	killSession = func(name string) error {
		killed = append(killed, name)
		return nil
	}
	defer func() {
		killSession = defaultKillSession
	}()

	report, err := Cleanup(CleanupOptions{})
	if err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if len(report.Removed) != 1 || len(killed) != 1 || killed[0] != "gs/sh/1" {
		t.Fatalf("unexpected cleanup report: %#v killed=%#v", report, killed)
	}
}

func TestCleanupReturnsPartialFailure(t *testing.T) {
	restore := stubShadowSessionInventory(t, []shadowSessionFixture{
		{
			name:         "gs/sh/1",
			parentPane:   "%1",
			parentExists: false,
			lastActiveAt: fixtureNow().Add(-10 * time.Minute),
		},
		{
			name:         "gs/sh/2",
			parentPane:   "%2",
			parentExists: false,
			lastActiveAt: fixtureNow().Add(-10 * time.Minute),
		},
	})
	defer restore()

	killSession = func(name string) error {
		if name == "gs/sh/2" {
			return errors.New("boom")
		}
		return nil
	}
	defer func() {
		killSession = defaultKillSession
	}()

	report, err := Cleanup(CleanupOptions{})
	if err == nil {
		t.Fatal("Cleanup() error = nil, want partial failure")
	}
	if len(report.Removed) != 1 || len(report.Failed) != 1 {
		t.Fatalf("unexpected cleanup report: %#v", report)
	}
}

type shadowSessionFixture struct {
	name         string
	parentPane   string
	parentErr    error
	parentExists bool
	lastActiveAt time.Time
	createdAt    time.Time
}

func fixtureNow() time.Time {
	return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
}

func stubShadowSessionInventory(t *testing.T, fixtures []shadowSessionFixture) func() {
	t.Helper()

	origList := listSessionSnapshotsByPrefix
	origGet := getSessionVar
	origPaneExists := paneExists
	origNow := now

	snapshots := make([]tmux.SessionSnapshot, 0, len(fixtures))
	parents := make(map[string]string, len(fixtures))
	parentErrs := make(map[string]error, len(fixtures))
	parentExists := make(map[string]bool, len(fixtures))

	for _, fixture := range fixtures {
		createdAt := fixture.createdAt
		if createdAt.IsZero() {
			createdAt = fixtureNow().Add(-24 * time.Hour)
		}
		lastActiveAt := fixture.lastActiveAt
		if lastActiveAt.IsZero() {
			lastActiveAt = fixtureNow().Add(-time.Hour)
		}
		snapshots = append(snapshots, tmux.SessionSnapshot{
			Name:     fixture.name,
			Created:  createdAt,
			Activity: lastActiveAt,
		})
		parents[fixture.name] = fixture.parentPane
		parentErrs[fixture.name] = fixture.parentErr
		parentExists[fixture.parentPane] = fixture.parentExists
	}

	listSessionSnapshotsByPrefix = func(prefix string) ([]tmux.SessionSnapshot, error) {
		return snapshots, nil
	}
	getSessionVar = func(session, key string) (string, error) {
		if key != "shadow_parent_pane" {
			return "", nil
		}
		if err := parentErrs[session]; err != nil {
			return "", err
		}
		return parents[session], nil
	}
	paneExists = func(paneID string) bool {
		return parentExists[paneID]
	}
	now = func() time.Time {
		return fixtureNow()
	}

	return func() {
		listSessionSnapshotsByPrefix = origList
		getSessionVar = origGet
		paneExists = origPaneExists
		now = origNow
	}
}
