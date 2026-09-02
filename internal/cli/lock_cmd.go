package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Mineru98/agent-file-lock/internal/config"
	"github.com/Mineru98/agent-file-lock/internal/lock"
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
	if action == lock.ActionUnlock {
		lock.Reverse(targets) // pre-order: parent directories first
	}

	// Pre-flight 2: privileges, checked before touching anything so we never
	// leave a tree half-locked.
	if !c.dryRun {
		if needStrong := e.needsStrongPrivilege(action, entries, targets); needStrong {
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

	results, sum := lock.Apply(targets, e.deps.Locker, action, lock.ApplyOptions{DryRun: c.dryRun, FailFast: c.failFast})
	e.printResults(&c, action, results, skipped, sum)
	return sum.ExitCode
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
