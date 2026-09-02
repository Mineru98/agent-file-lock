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

func flagsOf(fi os.FileInfo) uint32 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint32(st.Flags)
	}
	return 0
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
	fi, err := os.Lstat(path)
	if err != nil {
		return st, err
	}
	st.FSType = fsTypeOf(path)
	if fi.Mode()&os.ModeSymlink != 0 {
		st.IsSymlink = true
		return st, nil
	}
	flags := flagsOf(fi)
	st.IsDir = fi.IsDir()
	st.Writable = fi.Mode().Perm()&writeBits != 0
	st.Immutable = flags&sfImmutable != 0
	st.UserImmutable = flags&ufImmutable != 0
	st.Append = flags&(sfAppend|ufAppend) != 0
	return st, nil
}

// StateFromInfo implements FastStatuser: on BSD every flag we care about is
// already in the stat struct, so a scan needs no extra syscall.
func (bsdLocker) StateFromInfo(path string, fi os.FileInfo) (State, bool) {
	st := State{Path: path}
	if fi.Mode()&os.ModeSymlink != 0 {
		st.IsSymlink = true
		return st, true
	}
	flags := flagsOf(fi)
	st.IsDir = fi.IsDir()
	st.Writable = fi.Mode().Perm()&writeBits != 0
	st.Immutable = flags&sfImmutable != 0
	st.UserImmutable = flags&ufImmutable != 0
	st.Append = flags&(sfAppend|ufAppend) != 0
	return st, true
}

func (bsdLocker) Lock(path string, lvl Level) error {
	f, err := openNoFollow(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	flags := flagsOf(fi)
	fd := int(f.Fd())
	switch lvl {
	case LevelUser:
		if flags&(ufImmutable|sfImmutable) != 0 {
			// chmod would fail with EPERM while an immutable flag is set;
			// the file is already at least user-locked.
			return nil
		}
		if err := fchmodRemoveWrite(f, fi.Mode()); err != nil {
			return err
		}
		return mapErr(syscall.Fchflags(fd, int(flags|ufImmutable)))
	case LevelStrong:
		if flags&sfImmutable != 0 {
			return nil
		}
		return mapErr(syscall.Fchflags(fd, int(flags|sfImmutable)))
	default:
		return fmt.Errorf("unknown level %v", lvl)
	}
}

func (bsdLocker) Unlock(path string) error {
	f, err := openNoFollow(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	flags := flagsOf(fi)
	const both = sfImmutable | ufImmutable
	if flags&both != 0 {
		if err := mapErr(syscall.Fchflags(int(f.Fd()), int(flags&^both))); err != nil {
			return err
		}
	}
	return fchmodRestoreOwnerWrite(f, fi.Mode())
}

// Guard sets the append-only flag on a directory: entries can still be
// created inside it, but none can be deleted or renamed, and the directory
// itself cannot be renamed (BSD rename() rejects an append-flagged source).
func (bsdLocker) Guard(path string, lvl Level) error {
	f, err := openNoFollow(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	flags := flagsOf(fi)
	bit := sfAppend
	if lvl == LevelUser {
		bit = ufAppend
	}
	if flags&bit != 0 {
		return nil
	}
	return mapErr(syscall.Fchflags(int(f.Fd()), int(flags|bit)))
}

// Unguard clears both append flags and leaves the immutable ones untouched.
func (bsdLocker) Unguard(path string) error {
	f, err := openNoFollow(path)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	flags := flagsOf(fi)
	const both = sfAppend | ufAppend
	if flags&both == 0 {
		return nil
	}
	return mapErr(syscall.Fchflags(int(f.Fd()), int(flags&^both)))
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
	ufAppend    uint32 = 0x00000004 // UF_APPEND    (uappnd): owner may clear
	sfImmutable uint32 = 0x00020000 // SF_IMMUTABLE (schg): root only
	sfAppend    uint32 = 0x00040000 // SF_APPEND    (sappnd): root only
)
