//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

func applyHiddenWindow(cmd *exec.Cmd) {}

func maintainTerminalTitle(_ string) func() {
	return func() {}
}

type interactiveCommandOptions struct {
	Args     []string
	Dir      string
	OnOutput func([]byte)
}

type interactiveCommand struct {
	Input io.WriteCloser
	cmd   *exec.Cmd
}

func (c *interactiveCommand) Wait() (int, error) {
	err := c.cmd.Wait()
	if err == nil {
		return 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), err
	}
	return 1, err
}

func startInteractiveCommand(options interactiveCommandOptions) (*interactiveCommand, error) {
	if len(options.Args) == 0 {
		return nil, fmt.Errorf("missing command")
	}
	cmd := exec.Command(options.Args[0], options.Args[1:]...)
	cmd.Dir = options.Dir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		_, _ = io.Copy(stdin, os.Stdin)
	}()
	go func() {
		buffer := make([]byte, 8192)
		for {
			n, err := stdout.Read(buffer)
			if n > 0 {
				chunk := append([]byte(nil), buffer[:n]...)
				_, _ = os.Stdout.Write(chunk)
				if options.OnOutput != nil {
					options.OnOutput(chunk)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	return &interactiveCommand{Input: stdin, cmd: cmd}, nil
}
