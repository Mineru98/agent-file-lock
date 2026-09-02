//go:build !darwin && !freebsd && !(linux && (amd64 || arm64 || riscv64 || s390x || loong64 || 386 || arm))

package platform

import (
	"errors"
	"os"
	"runtime"
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
	f, err := openNoFollow(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if lvl == LevelStrong {
		return joinErr(ErrUnsupportedFS, errors.New(noStrongReason))
	}
	return fchmodRemoveWrite(f, fi.Mode())
}

func (userLocker) Unlock(path string) error {
	f, err := openNoFollow(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	return fchmodRestoreOwnerWrite(f, fi.Mode())
}

// Guard is unavailable without inode flags: there is no way to make a
// directory append-only, so the parent-rename bypass cannot be closed here.
func (userLocker) Guard(path string, lvl Level) error {
	return joinErr(ErrUnsupportedFS, errors.New(noGuardReason))
}

// Unguard is a no-op: Guard never succeeds on this platform.
func (userLocker) Unguard(path string) error { return nil }

var noGuardReason = "this platform has no append-only flag; parent directories cannot be guarded"

var noStrongReason = func() string {
	if runtime.GOOS == "linux" {
		return "afl has no FS_IOC_SETFLAGS encoding for linux/" + runtime.GOARCH + " yet; only --level user is available"
	}
	return "this platform has no immutable flag; only --level user is available"
}()

func (userLocker) Supports(path string, lvl Level) (bool, string) {
	if lvl == LevelStrong {
		return false, noStrongReason
	}
	return true, ""
}

func strongPrivilege() (bool, string) { return false, noStrongReason }

func IsWSL() bool { return false }
