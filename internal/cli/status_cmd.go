package cli

import (
	"fmt"

	"github.com/Mineru98/agent-file-lock/internal/lock"
	"github.com/Mineru98/agent-file-lock/internal/platform"
)

func (e *env) cmdStatus(args []string) int {
	var c common
	fs := e.newFlagSet("status", &c)
	paths, err := parseInterleaved(fs, args)
	if err != nil {
		return lock.ExitUsage
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

func stateName(st platform.State) string {
	switch {
	case st.IsSymlink:
		return "symlink"
	case st.Immutable:
		return "strong"
	case st.UserImmutable || !st.Writable:
		return "user"
	case st.FlagsUnknown:
		return "unknown"
	}
	return "unlocked"
}
