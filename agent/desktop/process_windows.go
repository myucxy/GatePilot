package main

import (
	"os/exec"
	"syscall"
)

func hiddenWindowSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: 0x08000000}
}

func applyHiddenWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = hiddenWindowSysProcAttr()
}
