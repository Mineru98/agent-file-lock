// Package lock holds the platform-independent core: building the list of
// targets from paths and options, applying an action through a
// platform.Locker with write-then-verify, and checking configuration drift.
package lock

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/Mineru98/agent-file-lock/internal/platform"
)

// ErrDirNeedsRecursive is returned when a directory is given without -R.
var ErrDirNeedsRecursive = errors.New("is a directory; use -R to lock its files or --dir-only for the directory inode itself")

// Target is one filesystem entry the tool will act on.
type Target struct {
	Path  string         `json:"path"`
	IsDir bool           `json:"is_dir"`
	Level platform.Level `json:"-"`
}

// PlanOptions controls how a root path expands into targets.
type PlanOptions struct {
	Recursive      bool
	IncludeDirs    bool // with Recursive: also lock directory inodes
	DirOnly        bool // lock the directory inode only, not its contents
	FollowSymlinks bool
	Exclude        []string
	Level          platform.Level
}

// Skipped records an entry that was deliberately left out of the plan.
type Skipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Plan expands root into an ordered list of targets. Files are listed
// depth-first so that a lock pass (post-order) processes children before their
// parent directory; callers reverse the slice for unlock (pre-order).
func Plan(root string, opts PlanOptions) ([]Target, []Skipped, error) {
	for _, g := range opts.Exclude {
		if err := ValidateGlob(g); err != nil {
			return nil, nil, fmt.Errorf("bad exclude pattern %q: %w", g, err)
		}
	}
	root = filepath.Clean(root)
	fi, err := os.Lstat(root)
	if err != nil {
		return nil, nil, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		if !opts.FollowSymlinks {
			return nil, []Skipped{{root, "symlink"}}, nil
		}
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, nil, err
		}
		root = resolved
		if fi, err = os.Lstat(root); err != nil {
			return nil, nil, err
		}
	}
	if !fi.IsDir() {
		return []Target{{Path: root, Level: opts.Level}}, nil, nil
	}
	if opts.DirOnly {
		return []Target{{Path: root, IsDir: true, Level: opts.Level}}, nil, nil
	}
	if !opts.Recursive {
		return nil, nil, fmt.Errorf("%s: %w", root, ErrDirNeedsRecursive)
	}
	var targets []Target
	var skipped []Skipped
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			skipped = append(skipped, Skipped{p, err.Error()})
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if p != root && excluded(rel, opts.Exclude) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			if !opts.FollowSymlinks {
				skipped = append(skipped, Skipped{p, "symlink"})
				return nil
			}
			resolved, err := filepath.EvalSymlinks(p)
			if err != nil {
				skipped = append(skipped, Skipped{p, err.Error()})
				return nil
			}
			rfi, err := os.Stat(resolved)
			if err != nil {
				skipped = append(skipped, Skipped{p, err.Error()})
				return nil
			}
			if rfi.IsDir() {
				skipped = append(skipped, Skipped{p, "symlink to directory (not followed)"})
				return nil
			}
			targets = append(targets, Target{Path: resolved, Level: opts.Level})
		case d.IsDir():
			if opts.IncludeDirs {
				targets = append(targets, Target{Path: p, IsDir: true, Level: opts.Level})
			}
		case d.Type().IsRegular():
			targets = append(targets, Target{Path: p, Level: opts.Level})
		default:
			skipped = append(skipped, Skipped{p, "special file"})
		}
		return nil
	})
	if walkErr != nil {
		return nil, nil, walkErr
	}
	sortPostOrder(targets)
	return targets, skipped, nil
}

func excluded(rel string, patterns []string) bool {
	for _, g := range patterns {
		if MatchGlob(g, rel) {
			return true
		}
	}
	return false
}

// sortPostOrder orders targets so every directory comes after everything
// beneath it. WalkDir yields pre-order; a stable sort by descending depth with
// directories after files at equal depth gives the required post-order.
func sortPostOrder(ts []Target) {
	depth := func(p string) int { return len(splitClean(filepath.ToSlash(p))) }
	sort.SliceStable(ts, func(i, j int) bool {
		di, dj := depth(ts[i].Path), depth(ts[j].Path)
		if di != dj {
			return di > dj
		}
		if ts[i].IsDir != ts[j].IsDir {
			return !ts[i].IsDir
		}
		return ts[i].Path < ts[j].Path
	})
}

// Reverse flips the slice in place (post-order -> pre-order for unlock).
func Reverse(ts []Target) {
	for i, j := 0, len(ts)-1; i < j; i, j = i+1, j-1 {
		ts[i], ts[j] = ts[j], ts[i]
	}
}
