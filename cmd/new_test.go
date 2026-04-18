package cmd

import "testing"

func TestResolveNewModeDefaultsToCD(t *testing.T) {
	got, err := resolveNewMode(false, false)
	if err != nil {
		t.Fatalf("resolveNewMode() error = %v", err)
	}
	if got != newModeCD {
		t.Fatalf("resolveNewMode() = %v, want %v", got, newModeCD)
	}
}

func TestResolveNewModeAllowsTmux(t *testing.T) {
	got, err := resolveNewMode(false, true)
	if err != nil {
		t.Fatalf("resolveNewMode() error = %v", err)
	}
	if got != newModeTmux {
		t.Fatalf("resolveNewMode() = %v, want %v", got, newModeTmux)
	}
}

func TestResolveNewModeRejectsConflictingFlags(t *testing.T) {
	if _, err := resolveNewMode(true, true); err == nil {
		t.Fatal("resolveNewMode() error = nil, want conflict error")
	}
}
