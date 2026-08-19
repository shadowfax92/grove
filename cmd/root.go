package cmd

import (
	"errors"
	"fmt"
	"os"

	"grove/internal/picker"

	"github.com/spf13/cobra"
)

var Version = "dev"

type commandDependencies struct {
	getwd       func() (string, error)
	interactive func() bool
	pick        func(string, []picker.Item) (string, error)
	pickMany    func(string, []picker.Item) ([]string, error)
}

type application struct {
	dependencies commandDependencies
	directory    string
	noInput      bool
	jsonOutput   bool
	nullOutput   bool
	colorMode    string
}

func newRootCommand(dependencies commandDependencies) *cobra.Command {
	if dependencies.getwd == nil {
		dependencies.getwd = os.Getwd
	}
	if dependencies.interactive == nil {
		dependencies.interactive = picker.Interactive
	}
	if dependencies.pick == nil {
		dependencies.pick = picker.Select
	}
	if dependencies.pickMany == nil {
		dependencies.pickMany = picker.SelectMany
	}
	app := &application{dependencies: dependencies}
	root := &cobra.Command{
		Use:           "grove [selector]",
		Short:         "Git worktrees rooted with their repositories",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.MaximumNArgs(1),
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if app.jsonOutput && app.nullOutput {
				return fmt.Errorf("--json and --null cannot be used together")
			}
			switch app.colorMode {
			case "auto", "always", "never":
			default:
				return fmt.Errorf("--color must be auto, always, or never")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runNavigate(cmd, args)
		},
	}
	root.SetHelpFunc(app.writeHelp)
	root.PersistentFlags().StringVarP(&app.directory, "directory", "C", "", "Run as if started in this directory")
	root.PersistentFlags().BoolVar(&app.noInput, "no-input", false, "Never open an interactive picker")
	root.PersistentFlags().BoolVar(&app.jsonOutput, "json", false, "Print versioned JSON")
	root.PersistentFlags().BoolVarP(&app.nullOutput, "null", "0", false, "Terminate path output with NUL")
	root.PersistentFlags().StringVar(&app.colorMode, "color", "auto", "Color output: auto, always, or never")
	root.AddCommand(
		app.cdCommand(),
		app.configCommand(),
		app.listCommand(),
		app.newCommand(),
		app.removeCommand(),
	)
	return root
}

func Execute() {
	root := newRootCommand(commandDependencies{})
	if err := root.Execute(); err != nil {
		if errors.Is(err, picker.ErrCancelled) {
			return
		}
		fmt.Fprintln(root.ErrOrStderr(), err)
		os.Exit(1)
	}
}
