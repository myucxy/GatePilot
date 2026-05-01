//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

const (
	nativeMessageBoxOK              = 0x00000000
	nativeMessageBoxOKCancel        = 0x00000001
	nativeMessageBoxYesNo           = 0x00000004
	nativeMessageBoxIconInformation = 0x00000040
	nativeMessageBoxIconWarning     = 0x00000030
	nativeMessageBoxDefaultButton2  = 0x00000100
	nativeMessageBoxSetForeground   = 0x00010000
	nativeMessageBoxTopmost         = 0x00040000
	nativeMessageBoxResultOK        = 1
	nativeMessageBoxResultYes       = 6
)

var (
	nativeUser32          = syscall.NewLazyDLL("user32.dll")
	nativeProcMessageBoxW = nativeUser32.NewProc("MessageBoxW")
)

func nativeApprovalMessageBox(title string, message string) (bool, error) {
	result, err := nativeMessageBox(title, message, nativeMessageBoxYesNo|nativeMessageBoxIconWarning|nativeMessageBoxDefaultButton2)
	if err != nil {
		return false, err
	}
	return result == nativeMessageBoxResultYes, nil
}

func nativeInfoMessageBox(title string, message string) error {
	_, err := nativeMessageBox(title, message, nativeMessageBoxOK|nativeMessageBoxIconInformation)
	return err
}

func nativeOpenClientMessageBox(title string, message string) (bool, error) {
	result, err := nativeMessageBox(title, message+"\n\n点击 OK 打开客户端，点击 Cancel 忽略。", nativeMessageBoxOKCancel|nativeMessageBoxIconInformation)
	if err != nil {
		return false, err
	}
	return result == nativeMessageBoxResultOK, nil
}

func nativeMessageBox(title string, message string, flags uintptr) (uintptr, error) {
	titlePtr, err := syscall.UTF16PtrFromString(title)
	if err != nil {
		return 0, err
	}
	messagePtr, err := syscall.UTF16PtrFromString(message)
	if err != nil {
		return 0, err
	}
	result, _, callErr := nativeProcMessageBoxW.Call(
		0,
		uintptr(unsafe.Pointer(messagePtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		flags|nativeMessageBoxTopmost|nativeMessageBoxSetForeground,
	)
	if result == 0 && callErr != syscall.Errno(0) {
		return result, callErr
	}
	return result, nil
}
