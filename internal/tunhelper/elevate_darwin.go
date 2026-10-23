//go:build darwin && !ios

package tunhelper

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// runElevated starts the helper as root through the standard macOS
// administrator password dialog. The helper runs detached, so there is no
// process handle; exited reports a cancelled dialog, or a helper that never
// connected back.
func runElevated(exe string, args []string) (*os.Process, <-chan error, error) {
	parts := []string{shellQuote(exe)}
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	cmdline := strings.Join(parts, " ") + " >/dev/null 2>&1 &"
	esc := strings.NewReplacer(`\`, `\`, `"`, `\"`).Replace
	script := `do shell script "` + esc(cmdline) + `" with prompt "Zeus VPN needs administrator permission to start Full VPN mode." with administrator privileges`
	exited := make(chan error, 1)
	go func() {
		out, err := exec.Command("/usr/bin/osascript", "-e", script).CombinedOutput()
		if err != nil {
			if strings.Contains(string(out), "-128") {
				exited <- errors.New("administrator permission was not granted")
			} else {
				exited <- errors.New("could not start the Full VPN helper: " + strings.TrimSpace(string(out)))
			}
			return
		}
		time.Sleep(20 * time.Second)
		exited <- errors.New("the Full VPN helper did not start")
	}()
	return nil, exited, nil
}
