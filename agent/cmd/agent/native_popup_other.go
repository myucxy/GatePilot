//go:build !windows

package main

import "fmt"

func nativeApprovalMessageBox(_ string, _ string) (bool, error) {
	return false, fmt.Errorf("native popup is only available on Windows")
}

func nativeInfoMessageBox(_ string, _ string) error {
	return nil
}

func nativeOpenClientMessageBox(_ string, _ string) (bool, error) {
	return false, nil
}
