//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

const (
	messageBoxYesNo         = 0x00000004
	messageBoxIconWarning   = 0x00000030
	messageBoxTopmost       = 0x00040000
	messageBoxSetForeground = 0x00010000
	messageBoxResultYes     = 6
)

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
)

func platformApprovalPrompt(title string, message string, _ string) (string, string, error) {
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return "reject", "", err
	}
	messagePtr, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		return "reject", "", err
	}
	result, _, callErr := procMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(messagePtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(messageBoxYesNo|messageBoxIconWarning|messageBoxTopmost|messageBoxSetForeground),
	)
	if result == messageBoxResultYes {
		return "approve", "", nil
	}
	if callErr != syscall.Errno(0) {
		return "reject", "", callErr
	}
	return "reject", "", nil
}
