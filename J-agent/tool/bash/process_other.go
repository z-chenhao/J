//go:build !darwin && !linux

package bash

import (
	"os/exec"
	"time"
)

func configureCommand(command *exec.Cmd) {
	command.WaitDelay = time.Second
}
