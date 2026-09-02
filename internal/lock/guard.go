package lock

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Mineru98/agent-file-lock/internal/platform"
)

// A lock on a file is not enough on its own. `mv docs docs.locked && mkdir
// docs` leaves the locked inode untouched but makes the path resolve to a
// fresh, writable file — the agent never had to defeat the flag, it just
// stepped around it. Closing that hole means the directories on the way to a
// locked file must not be renamable either.
//
// The tool for that is the append-only flag (BSD sappnd, Linux FS_APPEND_FL)
// rather than a second immutable flag: on a directory it still allows new
// entries, but the kernel refuses to unlink or rename anything already in it
// (may_delete()/ufs_rename() check the flag) and refuses to rename the
// directory itself, because the victim inode is append-only. So `touch
// docs/new.md` keeps working while `mv docs docs.locked` does not.

// ErrGuardRoot is returned when the guard boundary is not a usable directory.
var ErrGuardRoot = errors.New("invalid guard root")

// GuardRoot resolves how far up the guard walks. Precedence: an explicit
// --guard-root, then the directory holding the config file, then the git
// worktree containing the target, then the target's own parent.
func GuardRoot(explicit, configPath, target string) (string, error) {
	if explicit != "" {
		abs, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		fi, err := os.Stat(abs)
		if err != nil {
			return "", err
		}
		if !fi.IsDir() {
			return "", fmt.Errorf("%w: %s is not a directory", ErrGuardRoot, explicit)
		}
		return filepath.Clean(abs), nil
	}
	if configPath != "" {
		abs, err := filepath.Abs(filepath.Dir(configPath))
		if err != nil {
			return "", err
		}
		return filepath.Clean(abs), nil
	}
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	start := filepath.Dir(abs)
	if fi, err := os.Stat(abs); err == nil && fi.IsDir() {
		start = abs
	}
	if root, ok := gitRoot(start); ok {
		return root, nil
	}
	return start, nil
}

// gitRoot walks up looking for a .git entry (directory or worktree file).
func gitRoot(dir string) (string, bool) {
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// CheckGuardRoot refuses boundaries that would make a huge part of the
// filesystem append-only by accident. Those are only reachable with an
// explicit --guard-root, and even then they are almost always a mistake.
func CheckGuardRoot(root string) error {
	clean := filepath.Clean(root)
	if clean == string(filepath.Separator) {
		return fmt.Errorf("%w: refusing to guard the filesystem root", ErrGuardRoot)
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.Clean(home) == clean {
		return fmt.Errorf("%w: refusing to guard the home directory %s; point --guard-root at the project", ErrGuardRoot, clean)
	}
	if parent := filepath.Dir(clean); parent == string(filepath.Separator) {
		return fmt.Errorf("%w: %s is a top-level directory; point --guard-root at the project", ErrGuardRoot, clean)
	}
	return nil
}

// GuardPlan lists the directories that must be made append-only so that no
// ancestor of a locked target can be renamed out of the way: every directory
// from each target's parent up to and including root. Deepest first, so a
// failure part-way still leaves the innermost (most valuable) guard in place.
// Targets outside root are guarded up to their own parent only, and reported.
func GuardPlan(targets []Target, root string, lvl platform.Level) ([]Target, []Skipped) {
	root = filepath.Clean(root)
	seen := map[string]bool{}
	var dirs []Target
	var skipped []Skipped
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			dirs = append(dirs, Target{Path: p, IsDir: true, Level: lvl})
		}
	}
	for _, t := range targets {
		abs, err := filepath.Abs(t.Path)
		if err != nil {
			skipped = append(skipped, Skipped{t.Path, err.Error()})
			continue
		}
		dir := filepath.Dir(abs)
		if t.IsDir {
			// A locked directory inode still needs its own parent guarded.
			dir = filepath.Dir(filepath.Clean(abs))
		}
		if !within(dir, root) {
			skipped = append(skipped, Skipped{dir, "outside the guard root " + root + "; guarded its own parent only"})
			add(dir)
			continue
		}
		for {
			add(dir)
			if dir == root {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	sort.SliceStable(dirs, func(i, j int) bool {
		di, dj := len(splitClean(filepath.ToSlash(dirs[i].Path))), len(splitClean(filepath.ToSlash(dirs[j].Path)))
		if di != dj {
			return di > dj
		}
		return dirs[i].Path < dirs[j].Path
	})
	return dirs, skipped
}

// within reports whether p is root or lives beneath it.
func within(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || !strings.HasPrefix(rel, "..")
}

// ApplyGuard sets the append-only flag on each directory, verifying by
// re-reading. Already-guarded directories are skipped.
func ApplyGuard(dirs []Target, l platform.Locker, dryRun bool) ([]Result, Summary) {
	results := make([]Result, 0, len(dirs))
	for _, d := range dirs {
		r := Result{Path: d.Path, Action: "guard", Level: d.Level.String()}
		before, err := l.Status(d.Path)
		r.Before, r.After = before, before
		if err != nil {
			results = append(results, fail(r, err))
			continue
		}
		if before.Append {
			r.Outcome = OutcomeSkipped
			results = append(results, r)
			continue
		}
		if dryRun {
			r.Outcome = OutcomePlanned
			results = append(results, r)
			continue
		}
		if err := l.Guard(d.Path, d.Level); err != nil {
			results = append(results, fail(r, err))
			continue
		}
		after, err := l.Status(d.Path)
		if err != nil {
			results = append(results, fail(r, err))
			continue
		}
		r.After = after
		if !after.Append {
			results = append(results, fail(r, ErrVerify))
			continue
		}
		r.Outcome = OutcomeChanged
		results = append(results, r)
	}
	return results, Summarize(results)
}

// ReleaseGuard clears the append-only flag from each directory, but only once
// nothing beneath it is locked any more — one `afl unlock` of a single file
// must not disarm the guards protecting its siblings. Shallowest first, so a
// half-finished release never leaves an unreachable inner guard.
func ReleaseGuard(dirs []Target, l platform.Locker, dryRun bool) ([]Result, Summary) {
	ordered := append([]Target(nil), dirs...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return len(splitClean(filepath.ToSlash(ordered[i].Path))) < len(splitClean(filepath.ToSlash(ordered[j].Path)))
	})
	results := make([]Result, 0, len(ordered))
	for _, d := range ordered {
		r := Result{Path: d.Path, Action: "unguard"}
		before, err := l.Status(d.Path)
		r.Before, r.After = before, before
		if err != nil {
			results = append(results, fail(r, err))
			continue
		}
		if !before.Append {
			r.Outcome = OutcomeSkipped
			results = append(results, r)
			continue
		}
		if n := countLocked(d.Path, l); n > 0 {
			r.Outcome = OutcomeSkipped
			r.Error = fmt.Sprintf("%d locked file(s) remain beneath it", n)
			results = append(results, r)
			continue
		}
		if dryRun {
			r.Outcome = OutcomePlanned
			results = append(results, r)
			continue
		}
		if err := l.Unguard(d.Path); err != nil {
			results = append(results, fail(r, err))
			continue
		}
		after, err := l.Status(d.Path)
		if err != nil {
			results = append(results, fail(r, err))
			continue
		}
		r.After = after
		if after.Append {
			results = append(results, fail(r, ErrVerify))
			continue
		}
		r.Outcome = OutcomeChanged
		results = append(results, r)
	}
	return results, Summarize(results)
}

// countLocked reports how many entries beneath dir still carry a lock. It
// stops at the first few so a large tree is not walked in full for nothing.
func countLocked(dir string, l platform.Locker) int {
	const enough = 1
	n := 0
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == dir {
			return nil //nolint:nilerr // an unreadable subtree is not evidence of a lock
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		st, err := l.Status(p)
		if err != nil {
			return nil
		}
		if st.Level() != 0 {
			n++
			if n >= enough {
				return fs.SkipAll
			}
		}
		return nil
	})
	return n
}
