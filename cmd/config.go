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
	configCmd.AddCommand(configProfileCmd)
	rootCmd.AddCommand(configCmd)
}

var configCmd = &cobra.Command{
	Use:     "config",
	Aliases:     []string{"cfg"},
	Annotations: map[string]string{"group": "Setup:"},
	Short:       "Open grove config in $EDITOR",
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

var configProfileCmd = &cobra.Command{
	Use:   "profile",
	Short: "Show which shadow popup profile is active and its resolved sizes",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadFast()
		if err != nil {
			return err
		}

		width := config.TmuxClientWidth()
		override := os.Getenv("GROVE_PROFILE")

		if width > 0 {
			fmt.Printf("tmux client_width: %d\n", width)
		} else {
			fmt.Println("tmux client_width: (unavailable — not inside tmux)")
		}
		if override != "" {
			fmt.Printf("GROVE_PROFILE: %s\n", override)
		} else {
			fmt.Println("GROVE_PROFILE: (unset)")
		}

		for _, typ := range []string{"vim", "shell"} {
			size, name := cfg.Shadow.Popup.ResolvePopup(typ)
			profile := name
			if profile == "" {
				profile = "(top-level)"
			}
			fmt.Printf("%-6s → profile=%-12s width=%-5s height=%s\n", typ, profile, size.Width, size.Height)
		}

		if len(cfg.Shadow.Popup.Profiles) > 0 {
			fmt.Println("\nAvailable profiles:")
			for _, p := range cfg.Shadow.Popup.Profiles {
				bounds := ""
				if p.Match.MinClientWidth > 0 || p.Match.MaxClientWidth > 0 {
					bounds = fmt.Sprintf(" [min=%d max=%d]", p.Match.MinClientWidth, p.Match.MaxClientWidth)
				}
				fmt.Printf("  %s%s\n", p.Name, bounds)
			}
		}
		return nil
	},
}
