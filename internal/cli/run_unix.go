//go:build unix

package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

// runCommand executes argv with inherited stdio. While the child runs the
// parent ignores SIGINT/SIGTERM so that Ctrl-C reaches the child (same
// process group) but afl itself survives to re-lock afterwards.
func runCommand(argv []string, drop *Credential) (int, error) {
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return 127, err
	}
	cmd := exec.Command(path, argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = os.Environ()
	if drop != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{
			Uid: uint32(drop.UID), Gid: uint32(drop.GID), NoSetGroups: true,
		}}
		if home := os.Getenv("SUDO_USER"); home != "" {
			cmd.Env = append(cmd.Env, "USER="+home, "LOGNAME="+home)
		}
	}
	signal.Ignore(syscall.SIGINT, syscall.SIGTERM)
	defer signal.Reset(syscall.SIGINT, syscall.SIGTERM)
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				return 128 + int(ws.Signal()), fmt.Errorf("command killed by %v", ws.Signal())
			}
			return ee.ExitCode(), nil
		}
		return 126, err
	}
	return 0, nil
}
