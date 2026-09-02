//go:build unix

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

func execSudo(exe string, args []string) error {
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return errNoSudo
	}
	argv := append([]string{"sudo", "--", exe}, args...)
	return syscall.Exec(sudo, argv, os.Environ())
}
