//go:build darwin || freebsd

package platform

import (
	"fmt"
	"os"
	"syscall"
)

type bsdLocker struct{}

func newLocker() Locker { return bsdLocker{} }

func isUnsupported(errno syscall.Errno) bool {
	return errno == syscall.ENOTSUP || errno == syscall.EOPNOTSUPP || errno == syscall.ENOTTY
}

func lstatFlags(path string) (os.FileInfo, uint32, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fi, 0, fmt.Errorf("unexpected stat type for %s", path)
	}
	return fi, uint32(st.Flags), nil
}

func fsTypeOf(path string) string {
	var sfs syscall.Statfs_t
	if err := syscall.Statfs(path, &sfs); err != nil {
		return ""
	}
	b := make([]byte, 0, len(sfs.Fstypename))
	for _, c := range sfs.Fstypename {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}

func (bsdLocker) Status(path string) (State, error) {
	st := State{Path: path}
	fi, flags, err := lstatFlags(path)
	if err != nil {
		return st, err
	}
	st.FSType = fsTypeOf(path)
	if fi.Mode()&os.ModeSymlink != 0 {
		st.IsSymlink = true
		return st, nil
	}
	st.IsDir = fi.IsDir()
	st.Writable = fi.Mode().Perm()&writeBits != 0
	st.Immutable = flags&sfImmutable != 0
	st.UserImmutable = flags&ufImmutable != 0
	return st, nil
}

func (bsdLocker) Lock(path string, lvl Level) error {
	fi, flags, err := lstatFlags(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return ErrSymlink
	}
	switch lvl {
	case LevelUser:
		if flags&(ufImmutable|sfImmutable) != 0 {
			// chmod would fail with EPERM while an immutable flag is set;
			// the file is already at least user-locked.
			return nil
		}
		if err := removeWriteBits(path, fi.Mode()); err != nil {
			return err
		}
		return mapErr(syscall.Chflags(path, int(flags|ufImmutable)))
	case LevelStrong:
		if flags&sfImmutable != 0 {
			return nil
		}
		return mapErr(syscall.Chflags(path, int(flags|sfImmutable)))
	default:
		return fmt.Errorf("unknown level %v", lvl)
	}
}

func (bsdLocker) Unlock(path string) error {
	fi, flags, err := lstatFlags(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return ErrSymlink
	}
	const both = sfImmutable | ufImmutable
	if flags&both != 0 {
		if err := mapErr(syscall.Chflags(path, int(flags&^both))); err != nil {
			return err
		}
	}
	return restoreOwnerWrite(path, fi.Mode())
}

func (bsdLocker) Supports(path string, lvl Level) (bool, string) {
	if lvl == LevelUser {
		return true, ""
	}
	switch fst := fsTypeOf(path); fst {
	case "nfs", "smbfs", "webdav", "afpfs", "msdos", "exfat":
		return false, fst + " does not support the schg flag; use --level user"
	}
	return true, ""
}

func strongPrivilege() (bool, string) {
	if !IsRoot() {
		return false, "strong locks (schg) need root; re-run with sudo or --elevate"
	}
	return true, ""
}

// IsWSL is always false outside Linux.
func IsWSL() bool { return false }

// Flag bits from <sys/stat.h>; identical on Darwin and FreeBSD.
const (
	ufImmutable uint32 = 0x00000002 // UF_IMMUTABLE (uchg): owner may clear
	sfImmutable uint32 = 0x00020000 // SF_IMMUTABLE (schg): root only
)
