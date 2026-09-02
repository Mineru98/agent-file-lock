package cli

import (
	"fmt"
	"path/filepath"

	"github.com/Mineru98/agent-file-lock/internal/lock"
	"github.com/Mineru98/agent-file-lock/internal/notice"
	"github.com/Mineru98/agent-file-lock/internal/platform"
)

func (e *env) cmdStatus(args []string) int {
	var c common
	fs := e.newFlagSet("status", &c)
	paths, err := parseInterleaved(fs, args)
	if err != nil {
		return lock.ExitUsage
	}
	// With nothing to point at, status answers the question actually being
	// asked — "what is locked around here?" — by scanning the tree instead of
	// refusing for want of an argument.
	if len(paths) == 0 && c.config == "" {
		return e.cmdScan(&c)
	}
	entries, code := e.collectEntries(&c, paths, platform.LevelStrong)
	if code >= 0 {
		return code
	}
	// status on a bare directory shows the directory inode instead of failing
	for i := range entries {
		if !entries[i].Opts.Recursive && !entries[i].Opts.DirOnly {
			if st, err := e.deps.Locker.Status(entries[i].Path); err == nil && st.IsDir {
				entries[i].Opts.DirOnly = true
			}
		}
	}
	targets, skipped, code := e.planAll(entries)
	if code >= 0 {
		return code
	}
	results, sum := lock.Apply(targets, e.deps.Locker, lock.ActionStatus, lock.ApplyOptions{})
	if c.json {
		writeJSON(e.stdout, jsonReport{Action: "status", Results: results, Skipped: skipped, Summary: sum})
		return sum.ExitCode
	}
	for _, r := range results {
		if r.Outcome == lock.OutcomeFailed {
			fmt.Fprintf(e.stderr, "%-9s %s: %s\n", "error", r.Path, r.Error)
			continue
		}
		fmt.Fprintf(e.stdout, "%-9s %s\n", stateName(r.Before), r.Path)
	}
	for _, s := range skipped {
		fmt.Fprintf(e.stdout, "%-9s %s (%s)\n", "skipped", s.Path, s.Reason)
	}
	return sum.ExitCode
}

// cmdScan is `afl status` with no arguments: walk the tree and report what is
// locked. It reads only, so it works without sudo, and it skips the
// directories that make a repository big but never hold a lock.
func (e *env) cmdScan(c *common) int {
	root := "."
	res, err := lock.Scan(lock.ScanOptions{
		Root:     root,
		All:      c.all,
		MaxDepth: c.depth,
		Exclude:  c.exclude,
	}, e.deps.Locker)
	if err != nil {
		fmt.Fprintf(e.stderr, "afl: %v\n", err)
		return lock.ExitPartial
	}
	if c.json {
		writeJSON(e.stdout, res)
		return lock.ExitOK
	}
	for _, f := range res.Locked {
		fmt.Fprintf(e.stdout, "%-9s %s\n", f.Level, rel(f))
	}
	for _, g := range res.Guards {
		fmt.Fprintf(e.stdout, "%-9s %s\n", "guard", rel(g))
	}
	if c.quiet {
		return lock.ExitOK // the listing above is the whole answer
	}
	if len(res.Locked) == 0 && len(res.Guards) == 0 {
		fmt.Fprintf(e.stdout, "nothing locked under %s (%d files, %d directories scanned)\n", res.Root, res.Files, res.Dirs)
		if !c.all {
			fmt.Fprintf(e.stdout, "hint: %s and other build directories were skipped; use -a to include them\n", lock.SkipDirs[0])
		}
		return lock.ExitOK
	}
	fmt.Fprintf(e.stdout, "\n%d locked, %d guarded parent%s (%d files, %d directories scanned under %s)\n",
		len(res.Locked), len(res.Guards), plural(len(res.Guards)), res.Files, res.Dirs, res.Root)
	if len(res.Locked) > 0 {
		fmt.Fprintf(e.stdout, "an agent is refused with: %q\n", notice.Headline)
		fmt.Fprintf(e.stdout, "details: afl status <path>   ·   the full refusal: afl hook check <path>\n")
	}
	for _, s := range res.Skipped {
		fmt.Fprintf(e.stderr, "%-9s %s (%s)\n", "skipped", s.Path, s.Reason)
	}
	return lock.ExitOK
}

// rel prints a scan hit relative to where the scan started, with a trailing
// slash on directories so a guarded parent is not mistaken for a locked file.
func rel(f lock.Found) string {
	p := f.Rel
	if p == "" {
		p = filepath.Base(f.Path)
	}
	if f.IsDir && p != "." {
		return p + "/"
	}
	return p
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func stateName(st platform.State) string {
	switch {
	case st.IsSymlink:
		return "symlink"
	case st.Immutable:
		return "strong"
	case st.UserImmutable || !st.Writable:
		return "user"
	case st.Append:
		return "guard"
	case st.FlagsUnknown:
		return "unknown"
	}
	return "unlocked"
}
