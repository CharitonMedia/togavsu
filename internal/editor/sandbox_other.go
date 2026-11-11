//go:build !linux

package editor

import "os/exec"

func probeNetNS() bool { return false }

func sandbox(cmd *exec.Cmd, _ bool) {}
