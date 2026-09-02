package cli

import (
	"fmt"

	"github.com/Mineru98/agent-file-lock/internal/config"
	"github.com/Mineru98/agent-file-lock/internal/lock"
	"github.com/Mineru98/agent-file-lock/internal/platform"
)

// collectEntries turns CLI arguments (either -f config or positional paths)
// into plan entries. When a config is used, per-entry options come from the
// file; CLI --level overrides every entry, --exclude and --follow-symlinks
// are added on top.
func (e *env) collectEntries(c *common, paths []string, defaultLevel platform.Level) ([]config.Entry, int) {
	var level platform.Level
	if c.level != "" {
		l, err := platform.ParseLevel(c.level)
		if err != nil {
			return nil, e.usageErr("%v", err)
		}
		level = l
	}
	if c.dirOnly && c.recursive {
		return nil, e.usageErr("--dir-only and -R are mutually exclusive")
	}
	if c.config != "" {
		if len(paths) > 0 {
			return nil, e.usageErr("give either -f <config> or paths, not both")
		}
		cfg, err := config.Load(c.config)
		if err != nil {
			return nil, e.usageErr("%v", err)
		}
		entries := cfg.Entries(level)
		for i := range entries {
			entries[i].Opts.Exclude = append(entries[i].Opts.Exclude, c.exclude...)
			entries[i].Opts.FollowSymlinks = entries[i].Opts.FollowSymlinks || c.followSymlinks
		}
		return entries, -1
	}
	if len(paths) == 0 {
		return nil, e.usageErr("no paths given (or use -f <config>)")
	}
	if level == 0 {
		level = defaultLevel
	}
	entries := make([]config.Entry, 0, len(paths))
	for _, p := range paths {
		entries = append(entries, config.Entry{Path: p, Opts: lock.PlanOptions{
			Recursive:      c.recursive,
			IncludeDirs:    c.includeDirs,
			DirOnly:        c.dirOnly,
			FollowSymlinks: c.followSymlinks,
			Exclude:        c.exclude,
			Level:          level,
		}})
	}
	return entries, -1
}

// planAll expands entries into targets, aggregating skips. The first plan
// error aborts with a usage/partial exit code.
func (e *env) planAll(entries []config.Entry) ([]lock.Target, []lock.Skipped, int) {
	var targets []lock.Target
	var skipped []lock.Skipped
	for _, en := range entries {
		ts, sk, err := lock.Plan(en.Path, en.Opts)
		if err != nil {
			fmt.Fprintf(e.stderr, "afl: %v\n", err)
			return nil, nil, mapPlanErr(err)
		}
		targets = append(targets, ts...)
		skipped = append(skipped, sk...)
	}
	return targets, skipped, -1
}
