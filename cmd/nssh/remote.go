package main

import (
	"os"
	"os/exec"
	"strings"
)

// runRemoteScript pipes a shell script to `bash -l -s` on the remote and
// returns its stdout. Stderr passes through to the user's terminal so SSH
// errors (auth, connection refused, host key) are visible. BatchMode=yes
// — every caller wants a deterministic non-interactive run; key auth that
// works for the initial connection works for every subsequent call.
func runRemoteScript(sshTarget, script string) ([]byte, error) {
	cmd := exec.Command("ssh", "-o", "BatchMode=yes", sshTarget, "bash", "-l", "-s")
	cmd.Stdin = strings.NewReader(script)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}
