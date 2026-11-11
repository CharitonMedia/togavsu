package editor

import (
	"os"
	"os/exec"
	"syscall"
)

// netNSFlags give the child its own user and network namespaces: the network namespace
// has only a loopback interface that is down, so nothing it opens leaves the process.
const netNSFlags = syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET

func nsAttr() *syscall.SysProcAttr {
	uid, gid := os.Getuid(), os.Getgid()
	return &syscall.SysProcAttr{
		Setpgid:                    true,
		Cloneflags:                 netNSFlags,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: uid, HostID: uid, Size: int(zero) + 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: gid, HostID: gid, Size: int(zero) + 1}},
		GidMappingsEnableSetgroups: false,
	}
}

// probeNetNS reports whether this host lets the core make a network namespace (Docker's
// default seccomp profile and some AppArmor policies do not).
func probeNetNS() bool {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	cmd.SysProcAttr = nsAttr()
	return cmd.Run() == nil
}

// sandbox puts the child in its own process group, so a timeout kills everything it
// started, and in a network namespace when the host allows one.
func sandbox(cmd *exec.Cmd, isolated bool) {
	if isolated {
		cmd.SysProcAttr = nsAttr()
	} else {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
