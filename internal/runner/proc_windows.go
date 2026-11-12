//go:build windows

package runner

// Windows has no process group to signal, so the tree is ended with taskkill /T: killing only the
// leader leaves the agent's own children running, which is the failure the Unix path avoids.

import (
	"os/exec"
	"strconv"
	"time"
)

// configureProcess is a no-op: a new process group is a Unix idea.
func configureProcess(cmd *exec.Cmd) {}

// terminate ends the child and everything it started.
func terminate(p *process) {
	select {
	case <-p.done:
		return
	default:
	}
	pid := strconv.Itoa(p.cmd.Process.Pid)
	if err := exec.Command("taskkill", "/T", "/F", "/PID", pid).Run(); err != nil {
		_ = p.cmd.Process.Kill()
	}
	select {
	case <-p.done:
		return
	case <-time.After(10 * time.Second):
	}
	_ = p.cmd.Process.Kill()
	<-p.done
}
