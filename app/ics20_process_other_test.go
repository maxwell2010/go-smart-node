//go:build experimental_ibc && !windows

package app

import "os/exec"

func hideTestWindow(cmd *exec.Cmd) {}
