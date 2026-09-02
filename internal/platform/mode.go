package platform

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

const writeBits = 0o222

// openNoFollow opens path for inspection/mutation without following a final
// symlink, so every later operation acts on the inode we inspected (no
// lstat→chmod race on the last component).
func openNoFollow(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		var errno syscall.Errno
		if errors.As(err, &errno) && (errno == syscall.ELOOP || errno == syscall.EMLINK) {
			return nil, ErrSymlink
		}
		return nil, mapErr(err)
	}
	return f, nil
}

// fchmodRemoveWrite performs chmod a-w on an open file.
func fchmodRemoveWrite(f *os.File, mode fs.FileMode) error {
	if mode.Perm()&writeBits == 0 {
		return nil
	}
	return mapErr(syscall.Fchmod(int(f.Fd()), uint32(mode.Perm()&^writeBits)))
}

// fchmodRestoreOwnerWrite performs chmod u+w on an open file. Group/other
// write bits removed by the lock are not restored (we keep no record).
func fchmodRestoreOwnerWrite(f *os.File, mode fs.FileMode) error {
	if mode.Perm()&0o200 != 0 {
		return nil
	}
	return mapErr(syscall.Fchmod(int(f.Fd()), uint32(mode.Perm()|0o200)))
}

// mapErr converts raw errno values into the package sentinels, keeping the
// original error in the chain.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return err
	}
	switch {
	case isUnsupported(errno):
		return joinErr(ErrUnsupportedFS, err)
	case errno == syscall.EPERM || errno == syscall.EACCES:
		return joinErr(ErrPermission, err)
	}
	return err
}

func joinErr(sentinel, cause error) error { return &wrapped{sentinel: sentinel, cause: cause} }

type wrapped struct {
	sentinel error
	cause    error
}

func (w *wrapped) Error() string { return w.sentinel.Error() + ": " + w.cause.Error() }
func (w *wrapped) Is(target error) bool {
	return target == w.sentinel || errors.Is(w.cause, target)
}
func (w *wrapped) Unwrap() error { return w.cause }
