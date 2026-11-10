package cmd

import (
	"fmt"
	"os"
	"time"

	"grove/internal/cleanup"
	"grove/internal/config"

	"github.com/spf13/cobra"
)

type automaticSettings struct{ grace, interval time.Duration }

func cleanupSettings(c config.CleanupConfig) (automaticSettings, error) {
	interval := c.Interval
	if interval == "" {
		interval = "1h"
	}
	g, err := cleanupGrace(c)
	if err != nil {
		return automaticSettings{}, err
	}
	i, err := cleanup.Duration(interval)
	if err != nil {
		return automaticSettings{}, fmt.Errorf("cleanup.interval: %w", err)
	}
	return automaticSettings{g, i}, nil
}

// The grace period also protects manual merged cleanup and picker defaults;
// an unrelated invalid scheduling interval must not disable those safeguards.
func cleanupGrace(c config.CleanupConfig) (time.Duration, error) {
	value := c.GracePeriod
	if value == "" {
		value = "1d"
	}
	grace, err := cleanup.Duration(value)
	if err != nil {
		return 0, fmt.Errorf("cleanup.grace_period: %w", err)
	}
	return grace, nil
}

// Called after ordinary commands. A background child inherits config/state but
// has null stdio and its own session; the origin path crosses exec explicitly so
// a sweep cannot remove the worktree which launched it, even after the CLI exits.
func (a *application) maybeAutomaticCleanup(cmd *cobra.Command) {
	if cmd.Name() == "completion" {
		return
	}
	if dry, _ := cmd.Flags().GetBool("dry-run"); dry {
		return
	}
	if refresh, _ := cmd.Flags().GetBool("background-refresh"); refresh {
		return
	}
	if automatic, _ := cmd.Flags().GetBool("automatic"); automatic {
		return
	}
	cfg, err := config.Load()
	if err != nil {
		return
	}
	schedule, err := cleanup.DefaultSchedule()
	if err != nil {
		return
	}
	if a.dependencies.interactive() {
		if report, ok := schedule.Notice(); ok {
			var bytes int64
			for _, r := range report.Removed {
				if r.Bytes != nil {
					bytes += *r.Bytes
				}
			}
			status := ""
			if report.Error != "" {
				status = "; " + report.Error
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "Last automatic cleanup: %d worktrees, ~%s cached, %s%s (log: %s/sweeps.jsonl).\n", len(report.Removed), diskSize(bytes), ago(time.Since(report.Finished)), status, schedule.Directory)
		}
	}
	if cmd.Name() == "cleanup" {
		return
	}
	if !cfg.Cleanup.Enabled {
		return
	}
	settings, err := cleanupSettings(cfg.Cleanup)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: automatic cleanup disabled: %v\n", err)
		return
	}
	claimed, err := schedule.Claim(time.Now(), settings.interval)
	if err != nil || !claimed {
		return
	}
	directory, err := a.workingDirectory()
	if err != nil {
		return
	}
	caller, err := os.Getwd()
	if err != nil {
		return
	}
	args := []string{"-C", directory, "--no-input", "cleanup", "--sweep", "--automatic", "--caller-directory", caller}
	launch := a.dependencies.startAutomaticCleanup
	if launch == nil {
		launch = startFootprintRefresh
	}
	if err := launch(args); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: starting automatic cleanup: %v\n", err)
	}
}
