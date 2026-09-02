package lock

import (
	"errors"
	"fmt"

	"github.com/Mineru98/agent-file-lock/internal/platform"
)

// Action is what Apply does to each target.
type Action int

const (
	ActionLock Action = iota + 1
	ActionUnlock
	ActionStatus
)

func (a Action) String() string {
	switch a {
	case ActionLock:
		return "lock"
	case ActionUnlock:
		return "unlock"
	case ActionStatus:
		return "status"
	}
	return "?"
}

// ErrVerify means the operation returned success but a re-read did not show
// the expected state (e.g. a filesystem that silently ignores the flag).
var ErrVerify = errors.New("verification failed: state did not change")

// Outcome classifies a Result.
type Outcome string

const (
	OutcomeChanged Outcome = "changed"
	OutcomeSkipped Outcome = "skipped" // already in the desired state
	OutcomePlanned Outcome = "planned" // dry-run
	OutcomeFailed  Outcome = "failed"
	OutcomeInfo    Outcome = "info" // status action
)

// Result is the per-target outcome.
type Result struct {
	Path    string         `json:"path"`
	Action  string         `json:"action"`
	Level   string         `json:"level,omitempty"`
	Outcome Outcome        `json:"outcome"`
	Before  platform.State `json:"before"`
	After   platform.State `json:"after"`
	Err     error          `json:"-"`
	Error   string         `json:"error,omitempty"`
}

// Summary aggregates results and decides the exit code.
type Summary struct {
	Total    int `json:"total"`
	Changed  int `json:"changed"`
	Skipped  int `json:"skipped"`
	Failed   int `json:"failed"`
	ExitCode int `json:"exit_code"`
}

// Exit codes shared with the CLI.
const (
	ExitOK          = 0
	ExitPartial     = 1
	ExitUsage       = 2
	ExitPermission  = 3
	ExitUnsupported = 4
)

// ApplyOptions tunes Apply.
type ApplyOptions struct {
	DryRun   bool
	FailFast bool
}

// Apply runs action over targets in order, verifying every change by
// re-reading the state. Errors are collected per target; processing continues
// unless FailFast is set.
func Apply(targets []Target, l platform.Locker, action Action, opts ApplyOptions) ([]Result, Summary) {
	results := make([]Result, 0, len(targets))
	for _, t := range targets {
		r := applyOne(t, l, action, opts.DryRun)
		results = append(results, r)
		if r.Outcome == OutcomeFailed && opts.FailFast {
			break
		}
	}
	return results, Summarize(results)
}

func applyOne(t Target, l platform.Locker, action Action, dryRun bool) Result {
	r := Result{Path: t.Path, Action: action.String()}
	if action == ActionLock {
		r.Level = t.Level.String()
	}
	before, err := l.Status(t.Path)
	r.Before, r.After = before, before
	if err != nil {
		return fail(r, err)
	}
	if before.IsSymlink {
		return fail(r, platform.ErrSymlink)
	}
	switch action {
	case ActionStatus:
		r.Outcome = OutcomeInfo
		return r
	case ActionLock:
		if before.LockedAt(t.Level) {
			r.Outcome = OutcomeSkipped
			return r
		}
		if dryRun {
			r.Outcome = OutcomePlanned
			return r
		}
		if err := l.Lock(t.Path, t.Level); err != nil {
			return fail(r, err)
		}
	case ActionUnlock:
		if before.Level() == 0 {
			r.Outcome = OutcomeSkipped
			return r
		}
		if dryRun {
			r.Outcome = OutcomePlanned
			return r
		}
		if err := l.Unlock(t.Path); err != nil {
			return fail(r, err)
		}
	default:
		return fail(r, fmt.Errorf("unknown action %v", action))
	}
	after, err := l.Status(t.Path)
	if err != nil {
		return fail(r, err)
	}
	r.After = after
	want := action == ActionLock
	if got := after.LockedAt(t.Level); want != got || (action == ActionUnlock && after.Level() != 0) {
		return fail(r, ErrVerify)
	}
	r.Outcome = OutcomeChanged
	return r
}

func fail(r Result, err error) Result {
	r.Outcome = OutcomeFailed
	r.Err = err
	r.Error = err.Error()
	return r
}

// Summarize computes counts and the exit code. If every failure shares one
// root cause (permission or unsupported fs), the specific code is used;
// otherwise mixed failures yield ExitPartial.
func Summarize(results []Result) Summary {
	var s Summary
	s.Total = len(results)
	allPerm, allUnsup := true, true
	for _, r := range results {
		switch r.Outcome {
		case OutcomeChanged, OutcomePlanned:
			s.Changed++
		case OutcomeSkipped:
			s.Skipped++
		case OutcomeFailed:
			s.Failed++
			if !errors.Is(r.Err, platform.ErrPermission) {
				allPerm = false
			}
			if !errors.Is(r.Err, platform.ErrUnsupportedFS) {
				allUnsup = false
			}
		}
	}
	switch {
	case s.Failed == 0:
		s.ExitCode = ExitOK
	case allPerm:
		s.ExitCode = ExitPermission
	case allUnsup:
		s.ExitCode = ExitUnsupported
	default:
		s.ExitCode = ExitPartial
	}
	return s
}

// Mismatch is a target whose observed state is weaker than expected.
type Mismatch struct {
	Path     string `json:"path"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Err      string `json:"error,omitempty"`
}

// Check compares every target against its expected level without changing
// anything. It is used by `afl check` (CI / pre-commit).
func Check(targets []Target, l platform.Locker) []Mismatch {
	var out []Mismatch
	for _, t := range targets {
		st, err := l.Status(t.Path)
		if err != nil {
			out = append(out, Mismatch{Path: t.Path, Expected: t.Level.String(), Actual: "error", Err: err.Error()})
			continue
		}
		if st.IsSymlink {
			out = append(out, Mismatch{Path: t.Path, Expected: t.Level.String(), Actual: "symlink"})
			continue
		}
		if !st.LockedAt(t.Level) {
			actual := levelName(st)
			if st.FlagsUnknown {
				actual = "unknown (cannot read inode flags; re-run with sudo)"
			}
			out = append(out, Mismatch{Path: t.Path, Expected: t.Level.String(), Actual: actual})
		}
	}
	return out
}

func levelName(st platform.State) string {
	if lvl := st.Level(); lvl != 0 {
		return lvl.String()
	}
	return "unlocked"
}
