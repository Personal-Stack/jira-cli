//go:build windows

package cli

import (
	"os"
	"os/exec"
	"os/signal"
)

// execSbx runs `sbx args...` in dir with the terminal attached. Windows has
// no exec(2); Ctrl+C is left to sbx while it runs.
func execSbx(dir string, args ...string) error {
	signal.Ignore(os.Interrupt)
	defer signal.Reset(os.Interrupt)
	cmd := exec.Command("sbx", args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
