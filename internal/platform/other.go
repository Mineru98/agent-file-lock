//go:build !darwin && !freebsd && !(linux && (amd64 || arm64 || riscv64 || s390x || loong64 || 386 || arm))

package platform

import (
	"os"
	"syscall"
)

// userLocker is the fallback for platforms without immutable flags.
type userLocker struct{}

func newLocker() Locker { return userLocker{} }

func isUnsupported(errno syscall.Errno) bool { return false }

func (userLocker) Status(path string) (State, error) {
	st := State{Path: path}
	fi, err := os.Lstat(path)
	if err != nil {
		return st, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		st.IsSymlink = true
		return st, nil
	}
	st.IsDir = fi.IsDir()
	st.Writable = fi.Mode().Perm()&writeBits != 0
	return st, nil
}

func (userLocker) Lock(path string, lvl Level) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return ErrSymlink
	}
	if lvl == LevelStrong {
		return ErrUnsupportedFS
	}
	return removeWriteBits(path, fi.Mode())
}

func (userLocker) Unlock(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return ErrSymlink
	}
	return restoreOwnerWrite(path, fi.Mode())
}

func (userLocker) Supports(path string, lvl Level) (bool, string) {
	if lvl == LevelStrong {
		return false, "this platform has no immutable flag; only --level user is available"
	}
	return true, ""
}

func strongPrivilege() (bool, string) {
	return false, "this platform has no immutable flag; only --level user is available"
}

func IsWSL() bool { return false }
