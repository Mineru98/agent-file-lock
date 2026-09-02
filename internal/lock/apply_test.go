package lock

import (
	"errors"
	"os"
	"testing"

	"github.com/Mineru98/agent-file-lock/internal/platform"
)

// fakeLocker is an in-memory Locker for exercising Apply/Check.
type fakeLocker struct {
	states map[string]platform.State
	errs   map[string]error // returned by Lock/Unlock
	silent map[string]bool  // Lock "succeeds" but state doesn't change
	calls  []string
}

func newFake() *fakeLocker {
	return &fakeLocker{states: map[string]platform.State{}, errs: map[string]error{}, silent: map[string]bool{}}
}

func (f *fakeLocker) add(p string, st platform.State) { st.Path = p; f.states[p] = st }

func (f *fakeLocker) Status(p string) (platform.State, error) {
	st, ok := f.states[p]
	if !ok {
		// os.ErrNotExist, not a generic error: callers distinguish "missing"
		// from "unreadable", and a real Locker reports it that way.
		return platform.State{Path: p}, &os.PathError{Op: "stat", Path: p, Err: os.ErrNotExist}
	}
	return st, nil
}

func (f *fakeLocker) Lock(p string, lvl platform.Level) error {
	f.calls = append(f.calls, "lock:"+p)
	if err := f.errs[p]; err != nil {
		return err
	}
	if f.silent[p] {
		return nil
	}
	st := f.states[p]
	if lvl == platform.LevelStrong {
		st.Immutable = true
	} else {
		st.Writable = false
	}
	f.states[p] = st
	return nil
}

func (f *fakeLocker) Unlock(p string) error {
	f.calls = append(f.calls, "unlock:"+p)
	if err := f.errs[p]; err != nil {
		return err
	}
	st := f.states[p]
	st.Immutable, st.UserImmutable, st.Writable = false, false, true
	f.states[p] = st
	return nil
}

func (f *fakeLocker) Supports(string, platform.Level) (bool, string) { return true, "" }

func (f *fakeLocker) Guard(p string, lvl platform.Level) error {
	if err := f.errs[p]; err != nil {
		return err
	}
	st := f.states[p]
	st.Path, st.IsDir = p, true
	if !f.silent[p] {
		st.Append = true
	}
	f.states[p] = st
	return nil
}

func (f *fakeLocker) Unguard(p string) error {
	if err := f.errs[p]; err != nil {
		return err
	}
	st := f.states[p]
	st.Append = false
	f.states[p] = st
	return nil
}

func targets(lvl platform.Level, paths ...string) []Target {
	out := make([]Target, len(paths))
	for i, p := range paths {
		out[i] = Target{Path: p, Level: lvl}
	}
	return out
}

func TestApplyLockIdempotentAndVerify(t *testing.T) {
	f := newFake()
	f.add("/new", platform.State{Writable: true})
	f.add("/done", platform.State{Writable: true, Immutable: true})
	f.add("/silent", platform.State{Writable: true})
	f.silent["/silent"] = true
	f.add("/perm", platform.State{Writable: true})
	f.errs["/perm"] = platform.ErrPermission

	res, sum := Apply(targets(platform.LevelStrong, "/new", "/done", "/silent", "/perm", "/missing"), f, ActionLock, ApplyOptions{})
	want := map[string]Outcome{"/new": OutcomeChanged, "/done": OutcomeSkipped, "/silent": OutcomeFailed, "/perm": OutcomeFailed, "/missing": OutcomeFailed}
	for _, r := range res {
		if r.Outcome != want[r.Path] {
			t.Errorf("%s: outcome %s, want %s (%s)", r.Path, r.Outcome, want[r.Path], r.Error)
		}
	}
	if !errors.Is(res[2].Err, ErrVerify) {
		t.Errorf("silent lock should fail verification, got %v", res[2].Err)
	}
	if sum.Changed != 1 || sum.Skipped != 1 || sum.Failed != 3 || sum.ExitCode != ExitPartial {
		t.Errorf("summary = %+v", sum)
	}
	if len(f.calls) != 3 { // /new, /silent, /perm — not /done, not /missing
		t.Errorf("calls = %v", f.calls)
	}
}

func TestApplyExitCodes(t *testing.T) {
	f := newFake()
	f.add("/a", platform.State{Writable: true})
	f.add("/b", platform.State{Writable: true})
	f.errs["/a"], f.errs["/b"] = platform.ErrPermission, platform.ErrPermission
	if _, s := Apply(targets(platform.LevelStrong, "/a", "/b"), f, ActionLock, ApplyOptions{}); s.ExitCode != ExitPermission {
		t.Errorf("all-permission failures should exit %d, got %d", ExitPermission, s.ExitCode)
	}
	f.errs["/a"] = platform.ErrUnsupportedFS
	if _, s := Apply(targets(platform.LevelStrong, "/a", "/b"), f, ActionLock, ApplyOptions{}); s.ExitCode != ExitPartial {
		t.Errorf("mixed failures should exit %d, got %d", ExitPartial, s.ExitCode)
	}
	f.errs["/b"] = platform.ErrUnsupportedFS
	if _, s := Apply(targets(platform.LevelStrong, "/a", "/b"), f, ActionLock, ApplyOptions{}); s.ExitCode != ExitUnsupported {
		t.Errorf("all-unsupported failures should exit %d, got %d", ExitUnsupported, s.ExitCode)
	}
}

func TestApplyDryRunAndFailFast(t *testing.T) {
	f := newFake()
	f.add("/a", platform.State{Writable: true})
	f.add("/b", platform.State{Writable: true})
	res, sum := Apply(targets(platform.LevelUser, "/a", "/b"), f, ActionLock, ApplyOptions{DryRun: true})
	if len(f.calls) != 0 || res[0].Outcome != OutcomePlanned || sum.ExitCode != ExitOK {
		t.Errorf("dry-run touched the locker: %v %+v", f.calls, sum)
	}
	f.errs["/a"] = errors.New("boom")
	res, _ = Apply(targets(platform.LevelUser, "/a", "/b"), f, ActionLock, ApplyOptions{FailFast: true})
	if len(res) != 1 {
		t.Errorf("fail-fast should stop after first failure, got %d results", len(res))
	}
}

func TestApplyUnlockAndStatus(t *testing.T) {
	f := newFake()
	f.add("/locked", platform.State{Writable: true, Immutable: true})
	f.add("/open", platform.State{Writable: true})
	f.add("/link", platform.State{IsSymlink: true})
	res, sum := Apply(targets(0, "/locked", "/open", "/link"), f, ActionUnlock, ApplyOptions{})
	if res[0].Outcome != OutcomeChanged || res[1].Outcome != OutcomeSkipped || !errors.Is(res[2].Err, platform.ErrSymlink) {
		t.Errorf("unlock outcomes: %+v", res)
	}
	if sum.ExitCode != ExitPartial {
		t.Errorf("symlink failure should be partial, got %d", sum.ExitCode)
	}
	res, _ = Apply(targets(0, "/open"), f, ActionStatus, ApplyOptions{})
	if res[0].Outcome != OutcomeInfo || len(f.calls) != 1 {
		t.Errorf("status must not mutate: %+v calls=%v", res, f.calls)
	}
}

func TestCheck(t *testing.T) {
	f := newFake()
	f.add("/strong-ok", platform.State{Immutable: true, Writable: true})
	f.add("/user-ok", platform.State{Writable: false})
	f.add("/weak", platform.State{Writable: false}) // expected strong, only user
	f.add("/open", platform.State{Writable: true})
	ts := append(targets(platform.LevelStrong, "/strong-ok", "/weak", "/gone"), targets(platform.LevelUser, "/user-ok", "/open")...)
	mm := Check(ts, f)
	got := map[string]string{}
	for _, m := range mm {
		got[m.Path] = m.Actual
	}
	if len(mm) != 3 || got["/weak"] != "user" || got["/open"] != "unlocked" || got["/gone"] != "error" {
		t.Errorf("mismatches = %+v", mm)
	}
}
