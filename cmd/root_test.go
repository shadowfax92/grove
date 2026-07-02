package cmd

import (
	"fmt"
	"reflect"
	"testing"
)

func TestRootCommandDefaultsToWorkspacePicker(t *testing.T) {
	original := runRootDefault
	defer func() {
		runRootDefault = original
	}()

	called := false
	runRootDefault = func(args []string) error {
		called = true
		if got, want := args, []string{"mono/feat-auth"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("runRootDefault args = %#v, want %#v", got, want)
		}
		return nil
	}

	if err := rootCmd.RunE(rootCmd, []string{"mono/feat-auth"}); err != nil {
		t.Fatalf("rootCmd.RunE() error = %v", err)
	}
	if !called {
		t.Fatal("rootCmd.RunE() did not call runRootDefault")
	}
}

func TestExitCodeForErrorMapsRemovalSentinels(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "not found", err: ErrRemovePathNotFound, want: exitRemovePathNotFound},
		{name: "removal failed", err: ErrRemoveFailed, want: exitRemoveFailed},
		{name: "wrapped not found", err: fmt.Errorf("wrapped: %w", ErrRemovePathNotFound), want: exitRemovePathNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := exitCodeForError(tt.err)
			if !ok {
				t.Fatalf("exitCodeForError(%v) ok = false, want true", tt.err)
			}
			if got != tt.want {
				t.Fatalf("exitCodeForError(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
