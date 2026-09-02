// Package platform abstracts the OS-specific primitives used to make files
// immutable. Two protection levels exist:
//
//   - LevelUser: chmod a-w (plus UF_IMMUTABLE on BSD/macOS). The file owner
//     can undo it, so it is only a speed bump against careless writes.
//   - LevelStrong: FS_IMMUTABLE_FL on Linux, SF_IMMUTABLE on BSD/macOS.
//     Requires root (CAP_LINUX_IMMUTABLE on Linux) to set and to clear.
package platform

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// Level is the strength of a lock.
type Level int

const (
	LevelUser   Level = iota + 1 // chmod a-w (+ UF_IMMUTABLE on BSD/macOS)
	LevelStrong                  // FS_IMMUTABLE_FL / SF_IMMUTABLE
)

func (l Level) String() string {
	switch l {
	case LevelUser:
		return "user"
	case LevelStrong:
		return "strong"
	default:
		return fmt.Sprintf("level(%d)", int(l))
	}
}

// ParseLevel converts "strong" or "user" (case-insensitive) into a Level.
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "strong":
		return LevelStrong, nil
	case "user":
		return LevelUser, nil
	default:
		return 0, fmt.Errorf("unknown level %q (expected strong or user)", s)
	}
}

// State is the observed lock state of a single path.
type State struct {
	Path          string `json:"path"`
	Immutable     bool   `json:"immutable"`      // strong flag present (chattr +i / schg)
	UserImmutable bool   `json:"user_immutable"` // BSD/macOS uchg; always false on Linux
	Writable      bool   `json:"writable"`       // any write bit set in the mode
	IsDir         bool   `json:"is_dir"`
	IsSymlink     bool   `json:"is_symlink"`
	FSType        string `json:"fstype,omitempty"`
}

// LockedAt reports whether the state satisfies the given level.
func (s State) LockedAt(lvl Level) bool {
	switch lvl {
	case LevelStrong:
		return s.Immutable
	case LevelUser:
		return s.Immutable || s.UserImmutable || !s.Writable
	}
	return false
}

// Level returns the strongest level the state currently satisfies, or 0.
func (s State) Level() Level {
	if s.Immutable {
		return LevelStrong
	}
	if s.UserImmutable || !s.Writable {
		return LevelUser
	}
	return 0
}

// Locker performs lock operations on one path at a time.
type Locker interface {
	// Status inspects a path without modifying it. Symlinks are not followed.
	Status(path string) (State, error)
	// Lock applies the given level. Symlinks are rejected.
	Lock(path string, lvl Level) error
	// Unlock removes every lock level (immutable flags and restores u+w).
	Unlock(path string) error
	// Supports reports whether the filesystem holding path can hold lvl.
	// It is a best-effort pre-check; the definitive answer comes from Lock.
	Supports(path string, lvl Level) (ok bool, reason string)
}

// Sentinel errors. Callers use errors.Is to map them to exit codes.
var (
	ErrUnsupportedFS = errors.New("filesystem does not support immutable flags")
	ErrPermission    = errors.New("insufficient privileges")
	ErrSymlink       = errors.New("symbolic links are not locked (use --follow-symlinks to target the link destination)")
)

// New returns the Locker for the current OS.
func New() Locker { return newLocker() }

// IsRoot reports whether the effective uid is 0.
func IsRoot() bool { return os.Geteuid() == 0 }

// StrongPrivilege reports whether this process can apply LevelStrong, and a
// human-readable reason when it cannot.
func StrongPrivilege() (ok bool, reason string) { return strongPrivilege() }

// Platform describes the host for `afl doctor`.
type Platform struct {
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	EUID      int    `json:"euid"`
	IsWSL     bool   `json:"is_wsl"`
	StrongOK  bool   `json:"strong_ok"`
	StrongWhy string `json:"strong_reason,omitempty"`
}

// PathWritableTest reports whether a write to path is currently refused by the
// kernel. It is used by tests and by `doctor` to prove a lock took effect.
func PathWritableTest(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return false
	}
	f.Close()
	return true
}
