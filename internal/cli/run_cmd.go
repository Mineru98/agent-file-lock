package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/Mineru98/agent-file-lock/internal/lock"
	"github.com/Mineru98/agent-file-lock/internal/platform"
)

// RunCommand executes argv with inherited stdio and returns its exit code.
// dropTo, when non-nil, is the uid/gid the child should run as.
type RunCommand func(argv []string, dropTo *Credential) (int, error)

// Credential identifies the user a child process should run as.
type Credential struct {
	UID, GID int
}

type runReport struct {
	Command   []string      `json:"command"`
	Unlock    lock.Summary  `json:"unlock"`
	Command_  int           `json:"command_exit"`
	Relock    lock.Summary  `json:"relock"`
	RelockRes []lock.Result `json:"relock_results,omitempty"`
	Reguard   *lock.Summary `json:"reguard,omitempty"`
	ExitCode  int           `json:"exit_code"`
}

// cmdRun implements `afl run [-f cfg | paths] [--as-root] -- <command...>`:
// unlock the protected set, run the command, and re-lock no matter how the
// command ends. It exists so `sudo afl unlock && git pull && sudo afl lock`
// cannot be left half-done.
func (e *env) cmdRun(args []string) int {
	var c common
	asRoot := false
	fs := e.newFlagSet("run", &c)
	fs.BoolVar(&asRoot, "as-root", false, "keep root for the command instead of dropping to SUDO_UID")

	// Split at the first "--": everything after it is the command.
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || sep == len(args)-1 {
		return e.usageErr("run needs a command after --: afl run -f afl.yaml -- git pull")
	}
	argv := args[sep+1:]
	paths, err := parseInterleaved(fs, args[:sep])
	if err != nil {
		return lock.ExitUsage
	}
	if c.dryRun {
		return e.usageErr("run does not support --dry-run")
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

	// Privilege: re-locking needs root whenever any entry is strong, and
	// unlocking needs it whenever a strong flag is currently set. Check both
	// up front; a run that could unlock but not re-lock must never start.
	if e.needsStrongPrivilege(lock.ActionLock, entries, targets) || e.needsStrongPrivilege(lock.ActionUnlock, entries, targets) {
		if ok, why := e.deps.StrongOK(); !ok {
			if c.elevate && !e.deps.IsRoot() {
				if err := e.deps.Elevate(append([]string{"run"}, args...)); err != nil {
					fmt.Fprintf(e.stderr, "afl: --elevate failed: %v\n", err)
				}
				return lock.ExitPermission
			}
			fmt.Fprintf(e.stderr, "afl: %s\nhint: sudo afl run %s\n", why, strings.Join(args, " "))
			return lock.ExitPermission
		}
	}

	// The parent guards have to come down with the locks. Leaving a directory
	// append-only would make `git pull` fail on exactly the files afl just
	// unlocked, since git replaces them by renaming a temporary file over the
	// old one — which is the failure `afl run` exists to prevent.
	var guardDirs []lock.Target
	if c.guarding() {
		root, err := e.guardRootFor(&c, entries, targets)
		if err != nil {
			fmt.Fprintf(e.stderr, "afl: %v\n", err)
			return mapPlanErr(err)
		}
		guardDirs, _ = lock.GuardPlan(targets, root, guardLevel(entries))
	}

	rep := runReport{Command: argv}

	unlockTargets := append([]lock.Target(nil), targets...)
	lock.Reverse(unlockTargets)
	unlockRes, unlockSum := lock.Apply(unlockTargets, e.deps.Locker, lock.ActionUnlock, lock.ApplyOptions{})
	rep.Unlock = unlockSum
	if unlockSum.Failed > 0 {
		// Roll back whatever we managed to open, then bail.
		for _, r := range unlockRes {
			if r.Outcome == lock.OutcomeFailed {
				fmt.Fprintf(e.stderr, "[FAIL]     unlock %s: %s\n", r.Path, r.Error)
			}
		}
		relockRes, relockSum := lock.Apply(targets, e.deps.Locker, lock.ActionLock, lock.ApplyOptions{})
		e.reportRelock(&c, relockRes, relockSum)
		fmt.Fprintf(e.stderr, "afl: unlock failed; command not run\n")
		return unlockSum.ExitCode
	}
	// ReleaseGuard keeps any directory that still has locked files beneath it,
	// so a config covering part of a tree cannot expose the rest.
	released := 0
	if len(guardDirs) > 0 {
		gres, gsum := lock.ReleaseGuard(guardDirs, e.deps.Locker, false)
		released = gsum.Changed
		for _, r := range gres {
			if r.Outcome == lock.OutcomeFailed {
				fmt.Fprintf(e.stderr, "[FAIL]     unguard %s: %s\n", r.Path, r.Error)
			}
		}
	}
	if !c.quiet && !c.json {
		fmt.Fprintf(e.stdout, "afl: unlocked %d, released %d parent guard(s), running: %s\n",
			unlockSum.Changed, released, strings.Join(argv, " "))
	}

	var drop *Credential
	if !asRoot {
		drop = sudoCaller()
	}
	cmdExit, cmdErr := e.deps.Run(argv, drop)
	rep.Command_ = cmdExit
	if cmdErr != nil {
		fmt.Fprintf(e.stderr, "afl: %v\n", cmdErr)
	}

	relockRes, relockSum := lock.Apply(targets, e.deps.Locker, lock.ActionLock, lock.ApplyOptions{})
	rep.Relock = relockSum
	rep.RelockRes = relockRes
	e.reportRelock(&c, relockRes, relockSum)

	reguardFailed := 0
	if len(guardDirs) > 0 {
		gres, gsum := lock.ApplyGuard(guardDirs, e.deps.Locker, false)
		rep.Reguard = &gsum
		reguardFailed = gsum.Failed
		for _, r := range gres {
			if r.Outcome == lock.OutcomeFailed {
				fmt.Fprintf(e.stderr, "[FAIL]     reguard %s: %s\n", r.Path, r.Error)
			}
		}
		if gsum.Failed > 0 {
			fmt.Fprintf(e.stderr, "afl: WARNING %d parent director(y|ies) are no longer append-only; run: sudo afl lock %s\n",
				gsum.Failed, relockHint(&c))
		}
	}

	switch {
	case relockSum.Failed > 0 || reguardFailed > 0:
		rep.ExitCode = lock.ExitPartial
	case cmdErr != nil && cmdExit == 0:
		rep.ExitCode = lock.ExitPartial
	default:
		rep.ExitCode = cmdExit
	}
	if c.json {
		writeJSON(e.stdout, rep)
	} else if !c.quiet {
		fmt.Fprintf(e.stdout, "afl: command exited %d, re-locked %d (%d already locked, %d failed)\n",
			cmdExit, relockSum.Changed, relockSum.Skipped, relockSum.Failed)
	}
	return rep.ExitCode
}

func (e *env) reportRelock(c *common, res []lock.Result, sum lock.Summary) {
	for _, r := range res {
		if r.Outcome == lock.OutcomeFailed {
			fmt.Fprintf(e.stderr, "[FAIL]     relock %s: %s\n", r.Path, r.Error)
		}
	}
	if sum.Failed > 0 {
		fmt.Fprintf(e.stderr, "afl: WARNING %d path(s) are still unlocked; run: sudo afl lock %s\n", sum.Failed, relockHint(c))
	}
}

func relockHint(c *common) string {
	if c.config != "" {
		return "-f " + c.config
	}
	return "<paths>"
}

// sudoCaller returns the invoking user's credentials when running under sudo,
// so the wrapped command (git pull, an editor, an agent) does not run as root.
func sudoCaller() *Credential {
	if os.Geteuid() != 0 {
		return nil
	}
	uid, gid := os.Getenv("SUDO_UID"), os.Getenv("SUDO_GID")
	if uid == "" || gid == "" {
		return nil
	}
	var u, g int
	if _, err := fmt.Sscanf(uid, "%d", &u); err != nil || u == 0 {
		return nil
	}
	if _, err := fmt.Sscanf(gid, "%d", &g); err != nil {
		return nil
	}
	return &Credential{UID: u, GID: g}
}
