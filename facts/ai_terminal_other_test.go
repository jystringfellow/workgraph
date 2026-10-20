//go:build !unix

package facts

import "os/exec"

func detachFromControllingTerminal(*exec.Cmd) {}
