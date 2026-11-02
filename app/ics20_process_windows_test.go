//go:build experimental_ibc && windows

package app

import (
	"os/exec"
	"syscall"
)

func hideTestWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
