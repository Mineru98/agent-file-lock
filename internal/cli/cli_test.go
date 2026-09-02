package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mineru98/agent-file-lock/internal/lock"
	"github.com/Mineru98/agent-file-lock/internal/platform"
)

// fakeLocker records operations against real paths (for Plan) but keeps the
// lock state in memory so tests run without privileges.
type fakeLocker struct {
	strong  map[string]bool
	user    map[string]bool
	append_ map[string]bool
	fail    map[string]error // Lock and Unlock
	failLk  map[string]error // Lock only
	calls   []string
}

func newFake() *fakeLocker {
	return &fakeLocker{strong: map[string]bool{}, user: map[string]bool{}, append_: map[string]bool{},
		fail: map[string]error{}, failLk: map[string]error{}}
}

func (f *fakeLocker) Status(p string) (platform.State, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return platform.State{Path: p}, err
	}
	return platform.State{Path: p, IsDir: fi.IsDir(), IsSymlink: fi.Mode()&os.ModeSymlink != 0,
		Immutable: f.strong[p], Append: f.append_[p], Writable: !f.user[p], FSType: "fakefs"}, nil
}
func (f *fakeLocker) Lock(p string, lvl platform.Level) error {
	f.calls = append(f.calls, "lock "+filepath.Base(p))
	if err := f.fail[p]; err != nil {
		return err
	}
	if err := f.failLk[p]; err != nil {
		return err
	}
	if lvl == platform.LevelStrong {
		f.strong[p] = true
	} else {
		f.user[p] = true
	}
	return nil
}
func (f *fakeLocker) Unlock(p string) error {
	f.calls = append(f.calls, "unlock "+filepath.Base(p))
	if err := f.fail[p]; err != nil {
		return err
	}
	delete(f.strong, p)
	delete(f.user, p)
	return nil
}
func (f *fakeLocker) Guard(p string, lvl platform.Level) error {
	f.calls = append(f.calls, "guard "+filepath.Base(p))
	if err := f.fail[p]; err != nil {
		return err
	}
	f.append_[p] = true
	return nil
}

func (f *fakeLocker) Unguard(p string) error {
	f.calls = append(f.calls, "unguard "+filepath.Base(p))
	if err := f.fail[p]; err != nil {
		return err
	}
	delete(f.append_, p)
	return nil
}

func (f *fakeLocker) Supports(p string, lvl platform.Level) (bool, string) {
	if strings.Contains(p, "ninep") && lvl == platform.LevelStrong {
		return false, "9p mount"
	}
	return true, ""
}

type harness struct {
	t      *testing.T
	dir    string
	fake   *fakeLocker
	root   bool
	elev   []string
	stdout bytes.Buffer
	stderr bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, dir: t.TempDir(), fake: newFake()}
	for _, p := range []string{"docs/POLICY.md", "docs/specs/a.md", "docs/specs/b.tmp", "README.md", "ninep/x.md"} {
		full := filepath.Join(h.dir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(p), 0o644)
	}
	os.WriteFile(filepath.Join(h.dir, "afl.yaml"), []byte(`version: 1
level: strong
exclude:
  - "*.tmp"
paths:
  - docs/POLICY.md
  - path: docs/specs
    recursive: true
  - path: README.md
    level: user
`), 0o644)
	return h
}

func (h *harness) run(args ...string) int {
	h.stdout.Reset()
	h.stderr.Reset()
	for i, a := range args {
		if strings.HasPrefix(a, "@") {
			args[i] = filepath.Join(h.dir, a[1:])
		}
	}
	return RunWith(args, &h.stdout, &h.stderr, Deps{
		Locker: h.fake,
		IsRoot: func() bool { return h.root },
		StrongOK: func() (bool, string) {
			if h.root {
				return true, ""
			}
			return false, "need root"
		},
		Elevate: func(a []string) error { h.elev = a; return errors.New("exec stub") },
	})
}

func (h *harness) p(rel string) string { return filepath.Join(h.dir, rel) }

func TestUsageErrors(t *testing.T) {
	h := newHarness(t)
	if c := h.run(); c != lock.ExitUsage {
		t.Errorf("no args: %d", c)
	}
	if c := h.run("bogus"); c != lock.ExitUsage {
		t.Errorf("unknown cmd: %d", c)
	}
	if c := h.run("lock"); c != lock.ExitUsage {
		t.Errorf("lock without paths: %d", c)
	}
	if c := h.run("lock", "@docs"); c != lock.ExitUsage || !strings.Contains(h.stderr.String(), "-R") {
		t.Errorf("dir without -R: %d %s", c, h.stderr.String())
	}
	if c := h.run("lock", "--level", "medium", "@README.md"); c != lock.ExitUsage {
		t.Errorf("bad level: %d", c)
	}
	if c := h.run("lock", "-f", "@afl.yaml", "@README.md"); c != lock.ExitUsage {
		t.Errorf("config and paths: %d", c)
	}
	if c := h.run("check"); c != lock.ExitUsage {
		t.Errorf("check without -f: %d", c)
	}
	if c := h.run("completion", "powershell"); c != lock.ExitUsage {
		t.Errorf("bad shell: %d", c)
	}
	if c := h.run("lock", "-R", "--dir-only", "@docs"); c != lock.ExitUsage {
		t.Errorf("-R with --dir-only: %d", c)
	}
}

func TestStrongNeedsRootAndElevate(t *testing.T) {
	h := newHarness(t)
	if c := h.run("lock", "@README.md"); c != lock.ExitPermission || !strings.Contains(h.stderr.String(), "sudo afl lock") {
		t.Errorf("non-root strong: %d %q", c, h.stderr.String())
	}
	if len(h.fake.calls) != 0 {
		t.Errorf("locker touched before privilege check: %v", h.fake.calls)
	}
	if c := h.run("lock", "--elevate", "@README.md"); c != lock.ExitPermission || len(h.elev) == 0 || h.elev[0] != "lock" {
		t.Errorf("elevate: %d %v", c, h.elev)
	}
	// dry-run never needs root
	if c := h.run("lock", "-n", "@README.md"); c != lock.ExitOK || !strings.Contains(h.stdout.String(), "would lock") {
		t.Errorf("dry-run: %d %q", c, h.stdout.String())
	}
	// user level never needs root
	if c := h.run("lock", "--level", "user", "@README.md"); c != lock.ExitOK {
		t.Errorf("user lock: %d %s", c, h.stderr.String())
	}
}

func TestUnsupportedFS(t *testing.T) {
	h := newHarness(t)
	h.root = true
	if c := h.run("lock", "@ninep/x.md"); c != lock.ExitUnsupported || !strings.Contains(h.stderr.String(), "9p") {
		t.Errorf("9p: %d %q", c, h.stderr.String())
	}
	if c := h.run("lock", "--level", "user", "@ninep/x.md"); c != lock.ExitOK {
		t.Errorf("user on 9p should work: %d", c)
	}
	if c := h.run("doctor", "@ninep"); c != lock.ExitUnsupported || !strings.Contains(h.stdout.String(), "unsupported") {
		t.Errorf("doctor 9p: %d %q", c, h.stdout.String())
	}
}

func TestConfigRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.root = true
	if c := h.run("lock", "-f", "@afl.yaml"); c != lock.ExitOK {
		t.Fatalf("lock -f: %d %s", c, h.stderr.String())
	}
	out := h.stdout.String()
	if !strings.Contains(out, "lock: 3 changed") || strings.Contains(out, "b.tmp") {
		t.Errorf("lock output: %s", out)
	}
	if !h.fake.strong[h.p("docs/POLICY.md")] || !h.fake.strong[h.p("docs/specs/a.md")] || !h.fake.user[h.p("README.md")] {
		t.Errorf("state: strong=%v user=%v", h.fake.strong, h.fake.user)
	}
	if c := h.run("check", "-f", "@afl.yaml"); c != lock.ExitOK {
		t.Errorf("check after lock: %d %s", c, h.stderr.String())
	}
	if c := h.run("lock", "-f", "@afl.yaml"); c != lock.ExitOK || !strings.Contains(h.stdout.String(), "3 skipped") {
		t.Errorf("idempotent: %d %s", c, h.stdout.String())
	}
	// drift
	delete(h.fake.strong, h.p("docs/POLICY.md"))
	if c := h.run("check", "-f", "@afl.yaml", "--json"); c != lock.ExitPartial {
		t.Errorf("check drift: %d", c)
	}
	var rep checkReport
	if err := json.Unmarshal(h.stdout.Bytes(), &rep); err != nil || rep.OK || len(rep.Mismatches) != 1 || rep.Mismatches[0].Actual != "unlocked" {
		t.Errorf("check json: %v %+v", err, rep)
	}
	// unlock needs root only when strong flags exist; README (user) alone does not
	h.root = false
	if c := h.run("unlock", "@README.md"); c != lock.ExitOK {
		t.Errorf("user unlock without root: %d %s", c, h.stderr.String())
	}
	if c := h.run("unlock", "-f", "@afl.yaml"); c != lock.ExitPermission {
		t.Errorf("strong unlock without root: %d", c)
	}
	h.root = true
	if c := h.run("unlock", "-f", "@afl.yaml"); c != lock.ExitOK || len(h.fake.strong) != 0 {
		t.Errorf("unlock -f: %d %v", c, h.fake.strong)
	}
}

func TestStatusAndJSON(t *testing.T) {
	h := newHarness(t)
	h.fake.strong[h.p("docs/POLICY.md")] = true
	h.fake.user[h.p("README.md")] = true
	if c := h.run("status", "-R", "@docs", "@README.md"); c != lock.ExitOK {
		t.Fatalf("status: %d %s", c, h.stderr.String())
	}
	out := h.stdout.String()
	for _, want := range []string{"strong    ", "user      ", "unlocked  "} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
	// bare directory shows the inode instead of erroring
	if c := h.run("status", "@docs"); c != lock.ExitOK || !strings.Contains(h.stdout.String(), "docs") {
		t.Errorf("status dir: %d %s", c, h.stdout.String())
	}
	if c := h.run("status", "--json", "@README.md"); c != lock.ExitOK {
		t.Fatalf("status json: %d", c)
	}
	var rep jsonReport
	if err := json.Unmarshal(h.stdout.Bytes(), &rep); err != nil || len(rep.Results) != 1 || rep.Results[0].Before.Writable {
		t.Errorf("json: %v %+v", err, rep)
	}
}

// lastCall returns the final call with the given prefix.
func lastCall(calls []string, prefix string) string {
	out := ""
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			out = c
		}
	}
	return out
}

func TestPartialFailureAndOrder(t *testing.T) {
	h := newHarness(t)
	h.root = true
	h.fake.fail[h.p("docs/specs/a.md")] = errors.New("disk on fire")
	c := h.run("lock", "-R", "--include-dirs", "--exclude", "*.tmp", "@docs")
	if c != lock.ExitPartial || !strings.Contains(h.stderr.String(), "disk on fire") {
		t.Errorf("partial: %d %s", c, h.stderr.String())
	}
	// post-order: the docs directory itself must be the last lock (parent
	// guards are applied afterwards, on purpose)
	if last := lastCall(h.fake.calls, "lock "); last != "lock docs" {
		t.Errorf("lock order: %v", h.fake.calls)
	}
	h.fake.calls = nil
	h.fake.fail = map[string]error{}
	if c := h.run("unlock", "-R", "--include-dirs", "@docs"); c != lock.ExitOK {
		t.Errorf("unlock: %d %s", c, h.stderr.String())
	}
	if first := h.fake.calls[0]; first != "unlock docs" {
		t.Errorf("unlock order should start with the root dir: %v", h.fake.calls)
	}
	// interleaved flags after positionals
	if c := h.run("lock", "@README.md", "--level", "user", "-q"); c != lock.ExitOK || h.stdout.Len() != 0 {
		t.Errorf("interleaved/quiet: %d %q", c, h.stdout.String())
	}
}

func TestCompletionAndVersion(t *testing.T) {
	h := newHarness(t)
	for _, sh := range []string{"bash", "zsh", "fish"} {
		if c := h.run("completion", sh); c != lock.ExitOK || !strings.Contains(h.stdout.String(), "afl") {
			t.Errorf("completion %s: %d", sh, c)
		}
	}
	if c := h.run("version"); c != lock.ExitOK || !strings.HasPrefix(h.stdout.String(), "afl ") {
		t.Errorf("version: %d %q", c, h.stdout.String())
	}
	if c := h.run("doctor", "--json", "@docs"); c != lock.ExitOK || !strings.Contains(h.stdout.String(), `"fstype": "fakefs"`) {
		t.Errorf("doctor json: %d %s", c, h.stdout.String())
	}
}

func TestReviewRegressions(t *testing.T) {
	h := newHarness(t)
	h.root = true

	// bare "-" must not hang and is treated as a (nonexistent) path
	done := make(chan int, 1)
	go func() { done <- h.run("status", "-") }()
	select {
	case c := <-done:
		if c == lock.ExitOK {
			t.Errorf("status - should fail on a missing file, got %d", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("parseInterleaved hung on \"-\"")
	}

	// leading "--" protects flag-like filenames
	os.WriteFile(h.p("-R"), []byte("x"), 0o644)
	if c := h.run("status", "--", "@-R"); c != lock.ExitOK || !strings.Contains(h.stdout.String(), "-R") {
		t.Errorf("-- before flag-like path: %d %q %q", c, h.stdout.String(), h.stderr.String())
	}

	// check fails closed when a protected file was replaced by a symlink
	os.Remove(h.p("docs/POLICY.md"))
	os.Symlink(h.p("README.md"), h.p("docs/POLICY.md"))
	if c := h.run("check", "-f", "@afl.yaml"); c != lock.ExitPartial || !strings.Contains(h.stderr.String(), "symlink") {
		t.Errorf("check with symlinked target: %d %q", c, h.stderr.String())
	}
	// lock with nothing lockable is not a success
	if c := h.run("lock", "@docs/POLICY.md"); c != lock.ExitPartial {
		t.Errorf("lock of only-symlink entry: %d %q", c, h.stderr.String())
	}
	// unknown command prints usage to stderr, nothing on stdout
	if c := h.run("nope"); c != lock.ExitUsage || h.stdout.Len() != 0 {
		t.Errorf("unknown command stdout=%q", h.stdout.String())
	}
}

func TestRunWrapper(t *testing.T) {
	h := newHarness(t)
	h.root = true
	var ran [][]string
	exit := 0
	runFn := func(argv []string, drop *Credential) (int, error) {
		ran = append(ran, argv)
		// while the command runs, everything must be unlocked
		if len(h.fake.strong) != 0 || len(h.fake.user) != 0 {
			t.Errorf("targets still locked during command: strong=%v user=%v", h.fake.strong, h.fake.user)
		}
		return exit, nil
	}
	run := func(args ...string) int {
		h.stdout.Reset()
		h.stderr.Reset()
		for i, a := range args {
			if strings.HasPrefix(a, "@") {
				args[i] = filepath.Join(h.dir, a[1:])
			}
		}
		return RunWith(args, &h.stdout, &h.stderr, Deps{
			Locker: h.fake, IsRoot: func() bool { return h.root },
			StrongOK: func() (bool, string) { return h.root, "need root" },
			Run:      runFn,
		})
	}

	if c := run("run", "-f", "@afl.yaml"); c != lock.ExitUsage {
		t.Errorf("run without --: %d", c)
	}
	if c := run("run", "-f", "@afl.yaml", "--"); c != lock.ExitUsage {
		t.Errorf("run with empty command: %d", c)
	}
	h.root = false
	if c := run("run", "-f", "@afl.yaml", "--", "true"); c != lock.ExitPermission || len(ran) != 0 {
		t.Errorf("run without root: %d ran=%v", c, ran)
	}
	h.root = true

	// pre-lock the set, run a command, expect unlock→run→relock
	h.run("lock", "-f", "@afl.yaml")
	if c := run("run", "-f", "@afl.yaml", "--", "git", "pull"); c != 0 || len(ran) != 1 || ran[0][1] != "pull" {
		t.Fatalf("run: %d ran=%v err=%s", c, ran, h.stderr.String())
	}
	if !h.fake.strong[h.p("docs/POLICY.md")] || !h.fake.user[h.p("README.md")] {
		t.Errorf("not re-locked after run: strong=%v user=%v", h.fake.strong, h.fake.user)
	}
	// command failure propagates but relock still happens
	exit = 7
	if c := run("run", "-q", "-f", "@afl.yaml", "--", "false"); c != 7 || !h.fake.strong[h.p("docs/POLICY.md")] {
		t.Errorf("failing command: %d strong=%v", c, h.fake.strong)
	}
	// relock failure beats the command's exit code and warns
	exit = 0
	h.fake.failLk[h.p("README.md")] = errors.New("relock boom")
	if c := run("run", "-f", "@afl.yaml", "--", "true"); c != lock.ExitPartial || !strings.Contains(h.stderr.String(), "still unlocked") {
		t.Errorf("relock failure: %d %q", c, h.stderr.String())
	}
	h.fake.failLk = map[string]error{}
	// unlock failure: command must not run, set is rolled back to locked
	h.run("lock", "-f", "@afl.yaml")
	before := len(ran)
	h.fake.fail[h.p("docs/POLICY.md")] = errors.New("unlock boom")
	if c := run("run", "-f", "@afl.yaml", "--", "true"); c == 0 || len(ran) != before || !strings.Contains(h.stderr.String(), "command not run") {
		t.Errorf("unlock failure: %d ran=%d %q", c, len(ran)-before, h.stderr.String())
	}
	if !h.fake.user[h.p("README.md")] {
		t.Error("rollback did not re-lock the entries that were unlocked")
	}
	h.fake.fail = map[string]error{}
	// json report
	if c := run("run", "--json", "-f", "@afl.yaml", "--", "true"); c != 0 {
		t.Fatalf("json run: %d", c)
	}
	var rep runReport
	if err := json.Unmarshal(h.stdout.Bytes(), &rep); err != nil || rep.Relock.Failed != 0 || rep.Command[0] != "true" {
		t.Errorf("json: %v %+v", err, rep)
	}
}

func TestSudoCaller(t *testing.T) {
	t.Setenv("SUDO_UID", "501")
	t.Setenv("SUDO_GID", "20")
	got := sudoCaller()
	if os.Geteuid() != 0 {
		if got != nil {
			t.Errorf("non-root should never drop: %+v", got)
		}
		return
	}
	if got == nil || got.UID != 501 || got.GID != 20 {
		t.Errorf("sudoCaller = %+v", got)
	}
}
