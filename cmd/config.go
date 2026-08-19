package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"grove/internal/config"

	"github.com/spf13/cobra"
)

func (a *application) configCommand() *cobra.Command {
	var showPath bool
	command := &cobra.Command{
		Use:   "config",
		Short: "Edit configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := config.DefaultConfigPath()
			if err != nil {
				return err
			}
			if showPath {
				fmt.Fprintln(cmd.OutOrStdout(), path)
				return nil
			}
			if _, err := config.Load(); err != nil {
				return err
			}
			editor := os.Getenv("VISUAL")
			if editor == "" {
				editor = os.Getenv("EDITOR")
			}
			if editor == "" {
				editor = "vi"
			}
			process := exec.Command(editor, path)
			process.Stdin = os.Stdin
			process.Stdout = cmd.OutOrStdout()
			process.Stderr = cmd.ErrOrStderr()
			return process.Run()
		},
	}
	command.Flags().BoolVar(&showPath, "path", false, "Print the config path")
	return command
}
