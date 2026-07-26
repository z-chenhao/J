//go:build darwin || linux

package bash

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

func configureCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		if err != nil {
			return command.Process.Kill()
		}
		return nil
	}
	command.WaitDelay = time.Second
}
