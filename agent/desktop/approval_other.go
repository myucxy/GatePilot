//go:build !windows

package main

func platformApprovalPrompt(_ string, _ string, _ string) (string, string, error) {
	return "reject", "", nil
}
