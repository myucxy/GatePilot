//go:build !windows

package main

import "os/exec"

func applyHiddenWindow(cmd *exec.Cmd) {}
