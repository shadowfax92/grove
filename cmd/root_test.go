package cmd

import (
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
