//go:build !darwin && !linux

package merged

import (
	"os/exec"
	"time"
)

func configureForgeProcess(cmd *exec.Cmd) { cmd.WaitDelay = 100 * time.Millisecond }
