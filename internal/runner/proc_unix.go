//go:build !windows

package runner

// Unix children are put in their own process group, so one signal reaches the whole tree: a hermes
// run starts its own children, and a bare SIGTERM to the leader leaves them writing into the run
// root after the harness has moved on.

import (
	"os/exec"
	"syscall"
	"time"
)

// configureProcess puts the child in a new process group.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate ends the child's whole process group: SIGTERM, ten seconds, then SIGKILL.
func terminate(p *process) {
	select {
	case <-p.done:
		return
	default:
	}
	pid := p.cmd.Process.Pid
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil {
		_ = p.cmd.Process.Kill()
		<-p.done
		return
	}
	select {
	case <-p.done:
		return
	case <-time.After(10 * time.Second):
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	<-p.done
}
