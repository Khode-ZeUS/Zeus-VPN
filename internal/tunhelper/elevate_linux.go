//go:build linux && !android

package tunhelper

import (
	"errors"
	"os"
	"os/exec"
)

// runElevated starts the helper as root with pkexec, which shows the
// desktop's polkit password dialog.
func runElevated(exe string, args []string) (*os.Process, <-chan error, error) {
	pkexec, err := exec.LookPath("pkexec")
	if err != nil {
		return nil, nil, errors.New("Full VPN mode needs pkexec (polkit) to ask for the administrator password; install polkit, or run Zeus VPN with sudo")
	}
	cmd := exec.Command(pkexec, append([]string{exe}, args...)...)
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	exited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		var ee *exec.ExitError
		if errors.As(err, &ee) && (ee.ExitCode() == 126 || ee.ExitCode() == 127) {
			exited <- errors.New("administrator permission was not granted")
			return
		}
		exited <- errors.New("the Full VPN helper stopped")
	}()
	return cmd.Process, exited, nil
}
