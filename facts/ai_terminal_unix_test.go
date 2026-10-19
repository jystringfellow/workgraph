//go:build unix

package facts

import (
	"os/exec"
	"syscall"
)

func detachFromControllingTerminal(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
