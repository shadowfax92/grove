//go:build darwin || linux

package merged

import (
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureForgeProcess(cmd *exec.Cmd) {
	// A private process group makes cancellation own gh's descendants as well.
	// Killing only the shell can leave credential helpers or inherited pipes alive.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 100 * time.Millisecond
}
