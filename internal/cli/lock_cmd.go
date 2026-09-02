package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Mineru98/agent-file-lock/internal/config"
	"github.com/Mineru98/agent-file-lock/internal/lock"
	"github.com/Mineru98/agent-file-lock/internal/notice"
	"github.com/Mineru98/agent-file-lock/internal/platform"
)

func (e *env) cmdLock(args []string, action lock.Action) int {
	var c common
	fs := e.newFlagSet(action.String(), &c)
	paths, err := parseInterleaved(fs, args)
	if err != nil {
		return lock.ExitUsage
	}
	entries, code := e.collectEntries(&c, paths, platform.LevelStrong)
	if code >= 0 {
		return code
	}

	targets, skipped, code := e.planAll(entries)
	if code >= 0 {
		return code
	}
	if e.nothingToDo(entries, targets, skipped) {
		return lock.ExitPartial
	}

	// Pre-flight 1: filesystem support, checked on every target (cheap
	// mount-table lookup) so a tree spanning mounts is caught up front.
	if action == lock.ActionLock {
		var unsupported []string
		seen := map[string]bool{}
		for _, t := range targets {
			if t.Level != platform.LevelStrong {
				continue
			}
			if ok, why := e.deps.Locker.Supports(t.Path, platform.LevelStrong); !ok && !seen[why] {
				seen[why] = true
				unsupported = append(unsupported, fmt.Sprintf("  %s: %s", t.Path, why))
			}
		}
		if len(unsupported) > 0 {
			fmt.Fprintf(e.stderr, "afl: strong locks are not possible on these paths:\n%s\n", strings.Join(unsupported, "\n"))
			return lock.ExitUnsupported
		}
	}
	// A lock on the file alone is not a lock on the path: the parent
	// directory can be renamed aside and the path recreated, which is exactly
	// the bypass this guard closes. Ancestors get the append-only flag, which
	// still allows new files but refuses deletes and renames.
	var guardDirs []lock.Target
	if c.guarding() {
		root, err := e.guardRootFor(&c, entries, targets)
		if err != nil {
			fmt.Fprintf(e.stderr, "afl: %v\n", err)
			return mapPlanErr(err)
		}
		var gskip []lock.Skipped
		guardDirs, gskip = lock.GuardPlan(targets, root, guardLevel(entries))
		skipped = append(skipped, gskip...)
	}

	if action == lock.ActionUnlock {
		lock.Reverse(targets) // pre-order: parent directories first
	}

	// Pre-flight 2: privileges, checked before touching anything so we never
	// leave a tree half-locked.
	if !c.dryRun {
		// Guards need the same privilege as a strong lock. Only lock checks
		// for it up front: releasing a guard is fail-safe (leaving it on
		// protects more, not less), and an unlock of a user-level file must
		// keep working without sudo even when strong guards exist elsewhere.
		needStrong := e.needsStrongPrivilege(action, entries, targets)
		if !needStrong && action == lock.ActionLock && len(guardDirs) > 0 && guardLevel(entries) == platform.LevelStrong {
			needStrong = true
		}
		if needStrong {
			if ok, why := e.deps.StrongOK(); !ok {
				if c.elevate && !e.deps.IsRoot() {
					if err := e.deps.Elevate(append([]string{action.String()}, args...)); err != nil {
						fmt.Fprintf(e.stderr, "afl: --elevate failed: %v\n", err)
					}
					return lock.ExitPermission
				}
				fmt.Fprintf(e.stderr, "afl: %s\n", why)
				if !e.deps.IsRoot() {
					fmt.Fprintf(e.stderr, "hint: sudo afl %s %s\n", action, strings.Join(args, " "))
				}
				return lock.ExitPermission
			}
		}
	}

	// Order matters in both directions: guard the ancestors only after the
	// files are immutable (so there is no window where a directory is frozen
	// around still-writable files), and release them only after the files are
	// unlocked (so nothing is left reachable but unguarded).
	results, sum := lock.Apply(targets, e.deps.Locker, action, lock.ApplyOptions{DryRun: c.dryRun, FailFast: c.failFast})
	exit := sum.ExitCode
	if len(guardDirs) > 0 && !(sum.Failed > 0 && c.failFast) {
		var gres []lock.Result
		if action == lock.ActionLock {
			gres, _ = lock.ApplyGuard(guardDirs, e.deps.Locker, c.dryRun)
		} else {
			gres, _ = lock.ReleaseGuard(guardDirs, e.deps.Locker, c.dryRun)
		}
		results = append(results, gres...)
		// The printed summary stays about the files the user asked for;
		// guards get their own line. The exit code covers both, so a lock
		// whose guard could not be set never reports success.
		exit = lock.Summarize(results).ExitCode
	}
	e.printResults(&c, action, results, skipped, sum)
	if action == lock.ActionLock && exit == lock.ExitOK && !c.quiet && !c.json {
		e.printLockedNotice(&c, targets)
	}
	return exit
}

// guardRootFor resolves the boundary the guard walks up to and rejects
// boundaries so broad they would freeze unrelated parts of the filesystem.
func (e *env) guardRootFor(c *common, entries []config.Entry, targets []lock.Target) (string, error) {
	first := ""
	if len(targets) > 0 {
		first = targets[0].Path
	} else if len(entries) > 0 {
		first = entries[0].Path
	}
	root, err := lock.GuardRoot(c.guardRoot, c.config, first)
	if err != nil {
		return "", err
	}
	if err := lock.CheckGuardRoot(root); err != nil {
		return "", err
	}
	return root, nil
}

// guardLevel picks the flag strength for ancestors: strong unless every entry
// asked for a user-level lock.
func guardLevel(entries []config.Entry) platform.Level {
	for _, en := range entries {
		if en.Opts.Level == platform.LevelStrong {
			return platform.LevelStrong
		}
	}
	return platform.LevelUser
}

// printLockedNotice repeats, in the terminal, the sentence the hook will show
// an agent. It is the same text from the same place, so a user reading the
// lock output knows exactly what the agent will be told.
func (e *env) printLockedNotice(c *common, targets []lock.Target) {
	if len(targets) == 0 {
		return
	}
	fmt.Fprintf(e.stdout, "\nAn agent that tries to edit, move or delete these paths is refused with:\n")
	fmt.Fprintf(e.stdout, "  %q\n", notice.Headline)
	fmt.Fprintf(e.stdout, "The kernel enforces it either way; install the hook so the agent is told\nbefore it tries, instead of seeing a bare \"Operation not permitted\":\n  afl hook install --all\n")
}

// needsStrongPrivilege decides whether root is required: for lock, when any
// entry is strong; for unlock, when any target currently carries the strong
// flag (a user-level unlock must keep working without sudo).
func (e *env) needsStrongPrivilege(action lock.Action, entries []config.Entry, targets []lock.Target) bool {
	if action == lock.ActionLock {
		for _, en := range entries {
			if en.Opts.Level == platform.LevelStrong {
				return true
			}
		}
		return false
	}
	for _, t := range targets {
		st, err := e.deps.Locker.Status(t.Path)
		if err == nil && (st.Immutable || st.FlagsUnknown) {
			return true
		}
	}
	return false
}

// elevate re-executes the current binary through sudo. It only returns on
// failure.
func elevate(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return execSudo(exe, args)
}

var errNoSudo = errors.New("sudo not found in PATH")
