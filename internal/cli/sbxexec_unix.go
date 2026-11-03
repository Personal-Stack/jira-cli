//go:build !windows

package cli

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// execSbx replaces this process with `sbx args...` in dir, so an interactive
// session (`sbx env run`) owns the terminal and its Ctrl+C: jira's
// "Ctrl+C twice" handling must not apply inside the agent.
func execSbx(dir string, args ...string) error {
	path, err := exec.LookPath("sbx")
	if err != nil {
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	signal.Reset()
	return syscall.Exec(path, append([]string{"sbx"}, args...), os.Environ())
}
