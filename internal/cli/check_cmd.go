package cli

import (
	"fmt"

	"github.com/Mineru98/agent-file-lock/internal/lock"
	"github.com/Mineru98/agent-file-lock/internal/platform"
)

type checkReport struct {
	Config     string          `json:"config"`
	Checked    int             `json:"checked"`
	Mismatches []lock.Mismatch `json:"mismatches"`
	OK         bool            `json:"ok"`
}

func (e *env) cmdCheck(args []string) int {
	var c common
	fs := e.newFlagSet("check", &c)
	paths, err := parseInterleaved(fs, args)
	if err != nil {
		return lock.ExitUsage
	}
	if c.config == "" {
		return e.usageErr("check requires -f <config>")
	}
	entries, code := e.collectEntries(&c, paths, platform.LevelStrong)
	if code >= 0 {
		return code
	}
	targets, _, code := e.planAll(entries)
	if code >= 0 {
		return code
	}
	mm := lock.Check(targets, e.deps.Locker)
	rep := checkReport{Config: c.config, Checked: len(targets), Mismatches: mm, OK: len(mm) == 0}
	if rep.Mismatches == nil {
		rep.Mismatches = []lock.Mismatch{}
	}
	if c.json {
		writeJSON(e.stdout, rep)
	} else {
		for _, m := range mm {
			if m.Err != "" {
				fmt.Fprintf(e.stderr, "[DRIFT]   %s: expected %s, %s\n", m.Path, m.Expected, m.Err)
			} else {
				fmt.Fprintf(e.stderr, "[DRIFT]   %s: expected %s, actual %s\n", m.Path, m.Expected, m.Actual)
			}
		}
		if !c.quiet {
			if rep.OK {
				fmt.Fprintf(e.stdout, "check: %d paths ok\n", rep.Checked)
			} else {
				fmt.Fprintf(e.stdout, "check: %d of %d paths drifted (run: sudo afl lock -f %s)\n", len(mm), rep.Checked, c.config)
			}
		}
	}
	if rep.OK {
		return lock.ExitOK
	}
	return lock.ExitPartial
}
