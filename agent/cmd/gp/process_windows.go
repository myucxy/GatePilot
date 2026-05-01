//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func applyHiddenWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
}

func maintainTerminalTitle(title string) func() {
	if strings.TrimSpace(title) == "" {
		return func() {}
	}
	previous, hadPrevious := getConsoleTitle()
	setConsoleTitle(title)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				setConsoleTitle(title)
			case <-done:
				return
			}
		}
	}()
	return func() {
		close(done)
		if hadPrevious {
			setConsoleTitle(previous)
		}
	}
}

type interactiveCommandOptions struct {
	Args     []string
	Dir      string
	OnOutput func([]byte)
}

type interactiveCommand struct {
	Input io.WriteCloser
	wait  func() (int, error)
}

func (c *interactiveCommand) Wait() (int, error) {
	return c.wait()
}

func startInteractiveCommand(options interactiveCommandOptions) (*interactiveCommand, error) {
	if len(options.Args) == 0 {
		return nil, fmt.Errorf("missing command")
	}
	command, err := resolveWindowsCommand(options.Args)
	if err != nil {
		return nil, err
	}
	inRead, inWrite, err := createWindowsPipe()
	if err != nil {
		return nil, err
	}
	outRead, outWrite, err := createWindowsPipe()
	if err != nil {
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		return nil, err
	}
	pseudoConsole, err := createPseudoConsole(consoleSize(), inRead, outWrite)
	if err != nil {
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		_ = windows.CloseHandle(outWrite)
		return nil, err
	}

	attrList, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		closePseudoConsole(pseudoConsole)
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		_ = windows.CloseHandle(outWrite)
		return nil, err
	}
	if err := attrList.Update(procThreadAttributePseudoConsole, unsafe.Pointer(uintptr(pseudoConsole)), unsafe.Sizeof(pseudoConsole)); err != nil {
		attrList.Delete()
		closePseudoConsole(pseudoConsole)
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		_ = windows.CloseHandle(outWrite)
		return nil, err
	}
	startupInfo := windows.StartupInfoEx{
		StartupInfo:             windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))},
		ProcThreadAttributeList: attrList.List(),
	}
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(command.Args))
	if err != nil {
		attrList.Delete()
		closePseudoConsole(pseudoConsole)
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		_ = windows.CloseHandle(outWrite)
		return nil, err
	}
	appName, err := windows.UTF16PtrFromString(command.ApplicationName)
	if err != nil {
		attrList.Delete()
		closePseudoConsole(pseudoConsole)
		_ = windows.CloseHandle(inRead)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		_ = windows.CloseHandle(outWrite)
		return nil, err
	}
	var currentDir *uint16
	if options.Dir != "" {
		currentDir, err = windows.UTF16PtrFromString(options.Dir)
		if err != nil {
			attrList.Delete()
			closePseudoConsole(pseudoConsole)
			_ = windows.CloseHandle(inRead)
			_ = windows.CloseHandle(inWrite)
			_ = windows.CloseHandle(outRead)
			_ = windows.CloseHandle(outWrite)
			return nil, err
		}
	}
	var procInfo windows.ProcessInformation
	err = windows.CreateProcess(appName, commandLine, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT, nil, currentDir, &startupInfo.StartupInfo, &procInfo)
	attrList.Delete()
	_ = windows.CloseHandle(inRead)
	_ = windows.CloseHandle(outWrite)
	if err != nil {
		closePseudoConsole(pseudoConsole)
		_ = windows.CloseHandle(inWrite)
		_ = windows.CloseHandle(outRead)
		return nil, err
	}
	_ = windows.CloseHandle(procInfo.Thread)

	inputFile := os.NewFile(uintptr(inWrite), "gatepilot-gp-pty-input")
	outputFile := os.NewFile(uintptr(outRead), "gatepilot-gp-pty-output")
	restoreConsole := enableRawConsoleMode()
	doneOutput := make(chan struct{})
	go func() {
		defer close(doneOutput)
		defer outputFile.Close()
		buffer := make([]byte, 8192)
		for {
			n, err := outputFile.Read(buffer)
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
	go func() {
		_, _ = io.Copy(inputFile, os.Stdin)
	}()

	return &interactiveCommand{
		Input: inputFile,
		wait: func() (int, error) {
			defer restoreConsole()
			_, waitErr := windows.WaitForSingleObject(procInfo.Process, windows.INFINITE)
			_ = inputFile.Close()
			closePseudoConsole(pseudoConsole)
			<-doneOutput
			var exitCode uint32
			if err := windows.GetExitCodeProcess(procInfo.Process, &exitCode); err != nil && waitErr == nil {
				waitErr = err
			}
			_ = windows.CloseHandle(procInfo.Process)
			if waitErr != nil {
				return int(exitCode), waitErr
			}
			if exitCode != 0 {
				return int(exitCode), fmt.Errorf("process exited with code %d", exitCode)
			}
			return int(exitCode), nil
		},
	}, nil
}

type windowsCommand struct {
	ApplicationName string
	Args            []string
}

func resolveWindowsCommand(args []string) (windowsCommand, error) {
	path, err := exec.LookPath(args[0])
	if err != nil {
		return windowsCommand{}, err
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == ".cmd" || ext == ".bat" {
		comspec := getenv("ComSpec", "cmd.exe")
		comspecPath, err := exec.LookPath(comspec)
		if err != nil {
			return windowsCommand{}, err
		}
		return windowsCommand{
			ApplicationName: comspecPath,
			Args:            []string{comspecPath, "/d", "/s", "/c", windows.ComposeCommandLine(append([]string{path}, args[1:]...))},
		}, nil
	}
	resolved := append([]string{}, args...)
	resolved[0] = path
	return windowsCommand{ApplicationName: path, Args: resolved}, nil
}

func createWindowsPipe() (windows.Handle, windows.Handle, error) {
	var read windows.Handle
	var write windows.Handle
	if err := windows.CreatePipe(&read, &write, nil, 0); err != nil {
		return 0, 0, err
	}
	return read, write, nil
}

func consoleSize() windows.Coord {
	handle, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		return windows.Coord{X: 120, Y: 30}
	}
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(handle, &info); err != nil {
		return windows.Coord{X: 120, Y: 30}
	}
	width := info.Window.Right - info.Window.Left + 1
	height := info.Window.Bottom - info.Window.Top + 1
	if width <= 0 {
		width = 120
	}
	if height <= 0 {
		height = 30
	}
	return windows.Coord{X: width, Y: height}
}

func enableRawConsoleMode() func() {
	input, inputErr := windows.GetStdHandle(windows.STD_INPUT_HANDLE)
	output, outputErr := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	var oldInput uint32
	var oldOutput uint32
	if inputErr == nil && windows.GetConsoleMode(input, &oldInput) == nil {
		mode := oldInput
		mode &^= windows.ENABLE_ECHO_INPUT | windows.ENABLE_LINE_INPUT
		mode |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
		_ = windows.SetConsoleMode(input, mode)
	}
	if outputErr == nil && windows.GetConsoleMode(output, &oldOutput) == nil {
		outputMode := oldOutput | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING | windows.DISABLE_NEWLINE_AUTO_RETURN
		if err := windows.SetConsoleMode(output, outputMode); err != nil {
			_ = windows.SetConsoleMode(output, oldOutput|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
		}
	}
	return func() {
		if inputErr == nil && oldInput != 0 {
			_ = windows.SetConsoleMode(input, oldInput)
		}
		if outputErr == nil && oldOutput != 0 {
			_ = windows.SetConsoleMode(output, oldOutput)
		}
	}
}

const procThreadAttributePseudoConsole = 0x00020016

var (
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procCreatePseudoConsole = kernel32.NewProc("CreatePseudoConsole")
	procClosePseudoConsole  = kernel32.NewProc("ClosePseudoConsole")
	procSetConsoleTitle     = kernel32.NewProc("SetConsoleTitleW")
	procGetConsoleTitle     = kernel32.NewProc("GetConsoleTitleW")
)

func setConsoleTitle(title string) {
	ptr, err := windows.UTF16PtrFromString(title)
	if err != nil {
		return
	}
	procSetConsoleTitle.Call(uintptr(unsafe.Pointer(ptr)))
}

func getConsoleTitle() (string, bool) {
	buffer := make([]uint16, 1024)
	r1, _, _ := procGetConsoleTitle.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if r1 == 0 || int(r1) > len(buffer) {
		return "", false
	}
	return windows.UTF16ToString(buffer[:r1]), true
}

func createPseudoConsole(size windows.Coord, input windows.Handle, output windows.Handle) (windows.Handle, error) {
	var pseudoConsole windows.Handle
	packedSize := uintptr(uint32(uint16(size.X)) | uint32(uint16(size.Y))<<16)
	r1, _, err := procCreatePseudoConsole.Call(packedSize, uintptr(input), uintptr(output), 0, uintptr(unsafe.Pointer(&pseudoConsole)))
	if r1 != 0 {
		if err != windows.ERROR_SUCCESS {
			return 0, err
		}
		return 0, syscall.Errno(r1)
	}
	return pseudoConsole, nil
}

func closePseudoConsole(pseudoConsole windows.Handle) {
	if pseudoConsole != 0 {
		procClosePseudoConsole.Call(uintptr(pseudoConsole))
	}
}
