//go:build !linux && !darwin

package analyzer

import "os/exec"

// Only Linux and macOS builds promise process-group termination.
func cancelCLIGroup(_ *exec.Cmd) {}
