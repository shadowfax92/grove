package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"grove/internal/config"

	"github.com/spf13/cobra"
)

func init() {
	configCmd.Flags().Bool("path", false, "Print config file path and exit")
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Open grove config in $EDITOR",
	RunE: func(cmd *cobra.Command, args []string) error {
		path, err := config.DefaultConfigPath()
		if err != nil {
			return err
		}

		showPath, _ := cmd.Flags().GetBool("path")
		if showPath {
			fmt.Println(path)
			return nil
		}

		// Ensure config exists
		if _, err := config.Load(); err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vi"
		}

		c := exec.Command(editor, path)
		c.Stdin = os.Stdin
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
		return c.Run()
	},
}
