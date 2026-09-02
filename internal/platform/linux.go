//go:build linux && (amd64 || arm64 || riscv64 || s390x || loong64 || 386 || arm)

package platform

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// Inode flag bits (linux/fs.h). The kernel reads/writes an int, not a long.
const (
	fsImmutableFl int32 = 0x00000010
	fsAppendFl    int32 = 0x00000020
)

type linuxLocker struct {
	mounts *mountTable
}

func newLocker() Locker { return &linuxLocker{mounts: loadMountTable()} }

func isUnsupported(errno syscall.Errno) bool {
	return errno == syscall.ENOTTY || errno == syscall.EOPNOTSUPP || errno == syscall.ENOSYS
}

func getFlags(fd uintptr) (int32, error) {
	var attr int32
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, fsIocGetFlags, uintptr(unsafe.Pointer(&attr)))
	if e != 0 {
		return 0, mapErr(e)
	}
	return attr, nil
}

func setFlags(fd uintptr, attr int32) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, fsIocSetFlags, uintptr(unsafe.Pointer(&attr)))
	if e != 0 {
		return mapErr(e)
	}
	return nil
}

func openNoFollow(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, mapErr(err)
	}
	return f, nil
}

func (l *linuxLocker) Status(path string) (State, error) {
	st := State{Path: path, FSType: l.mounts.fsTypeFor(path)}
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
	f, err := openNoFollow(path)
	if err != nil {
		// Unreadable files (e.g. 0000 owned by someone else) still have a
		// mode-based state; report that without the flag bits.
		return st, nil
	}
	defer f.Close()
	attr, err := getFlags(f.Fd())
	if err != nil {
		if isUnsupportedErr(err) {
			return st, nil
		}
		return st, err
	}
	st.Immutable = attr&fsImmutableFl != 0
	return st, nil
}

func (l *linuxLocker) Lock(path string, lvl Level) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return ErrSymlink
	}
	switch lvl {
	case LevelUser:
		return removeWriteBits(path, fi.Mode())
	case LevelStrong:
		f, err := openNoFollow(path)
		if err != nil {
			return err
		}
		defer f.Close()
		attr, err := getFlags(f.Fd())
		if err != nil {
			return err
		}
		if attr&fsImmutableFl != 0 {
			return nil
		}
		return setFlags(f.Fd(), attr|fsImmutableFl)
	default:
		return fmt.Errorf("unknown level %v", lvl)
	}
}

func (l *linuxLocker) Unlock(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return ErrSymlink
	}
	f, err := openNoFollow(path)
	if err != nil {
		return err
	}
	attr, err := getFlags(f.Fd())
	switch {
	case err == nil && attr&fsImmutableFl != 0:
		if err := setFlags(f.Fd(), attr&^fsImmutableFl); err != nil {
			f.Close()
			return err
		}
	case err != nil && !isUnsupportedErr(err):
		f.Close()
		return err
	}
	f.Close()
	return restoreOwnerWrite(path, fi.Mode())
}

// Filesystems known to implement FS_IOC_SETFLAGS with FS_IMMUTABLE_FL.
var strongFS = map[string]bool{
	"ext2": true, "ext3": true, "ext4": true, "xfs": true, "btrfs": true,
	"f2fs": true, "jfs": true, "reiserfs": true, "nilfs2": true, "ocfs2": true,
	"gfs2": true, "ubifs": true, "bcachefs": true,
}

// Filesystems known to lack immutable support, with user-facing hints.
var noStrongFS = map[string]string{
	"9p":       "WSL DrvFs (/mnt/<drive>) is a 9p mount; move the files into the Linux filesystem (e.g. under ~) or use --level user",
	"drvfs":    "WSL1 DrvFs does not support immutable flags; move the files into the Linux filesystem or use --level user",
	"lxfs":     "WSL1 lxfs does not support immutable flags",
	"wslfs":    "WSL1 wslfs does not support immutable flags",
	"nfs":      "NFS does not honour immutable flags; lock the files on the server instead",
	"nfs4":     "NFS does not honour immutable flags; lock the files on the server instead",
	"cifs":     "SMB/CIFS does not support immutable flags",
	"smb3":     "SMB/CIFS does not support immutable flags",
	"vfat":     "FAT filesystems have no inode flags; use --level user",
	"exfat":    "exFAT has no inode flags; use --level user",
	"msdos":    "FAT filesystems have no inode flags; use --level user",
	"ntfs":     "NTFS-3G does not support immutable flags; use --level user",
	"ntfs3":    "ntfs3 does not support immutable flags; use --level user",
	"overlay":  "overlayfs forwards flags inconsistently; lock the files on the lower/upper filesystem directly",
	"squashfs": "squashfs is already read-only",
	"iso9660":  "iso9660 is already read-only",
}

func (l *linuxLocker) Supports(path string, lvl Level) (bool, string) {
	if lvl == LevelUser {
		return true, ""
	}
	fst := l.mounts.fsTypeFor(path)
	if fst == "" {
		return true, ""
	}
	if strings.HasPrefix(fst, "fuse") {
		return false, "FUSE filesystems (" + fst + ") do not support immutable flags; use --level user"
	}
	if why, bad := noStrongFS[fst]; bad {
		return false, why
	}
	return true, ""
}

func strongPrivilege() (bool, string) {
	if !IsRoot() {
		return false, "strong locks need root (CAP_LINUX_IMMUTABLE); re-run with sudo or --elevate"
	}
	if !HasCapImmutable() {
		return false, "running as root but CAP_LINUX_IMMUTABLE is missing (containers: docker run --cap-add LINUX_IMMUTABLE)"
	}
	return true, ""
}

func isUnsupportedErr(err error) bool {
	var w *wrapped
	if ok := asWrapped(err, &w); ok {
		return w.sentinel == ErrUnsupportedFS
	}
	return false
}

// IsWSL reports whether we are running inside Windows Subsystem for Linux.
func IsWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	if _, err := os.Stat("/proc/sys/fs/binfmt_misc/WSLInterop"); err == nil {
		return true
	}
	b, err := os.ReadFile("/proc/version")
	return err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft")
}
