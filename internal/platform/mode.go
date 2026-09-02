package platform

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

const writeBits = 0o222

// removeWriteBits performs chmod a-w.
func removeWriteBits(path string, mode fs.FileMode) error {
	if mode.Perm()&writeBits == 0 {
		return nil
	}
	return mapErr(os.Chmod(path, mode.Perm()&^writeBits))
}

// restoreOwnerWrite performs chmod u+w.
func restoreOwnerWrite(path string, mode fs.FileMode) error {
	if mode.Perm()&0o200 != 0 {
		return nil
	}
	return mapErr(os.Chmod(path, mode.Perm()|0o200))
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
