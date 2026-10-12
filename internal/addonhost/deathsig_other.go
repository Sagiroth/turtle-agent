//go:build !linux

package addonhost

import "os/exec"

// setDeathSignal: elsewhere the sidecar ends on the end of its stdin when the agent dies.
func setDeathSignal(cmd *exec.Cmd) {}
