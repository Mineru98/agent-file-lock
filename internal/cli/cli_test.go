package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mineru98/agent-file-lock/internal/lock"
	"github.com/Mineru98/agent-file-lock/internal/platform"
)

// fakeLocker records operations against real paths (for Plan) but keeps the
// lock state in memory so tests run without privileges.
type fakeLocker struct {
	strong map[string]bool
	user   map[string]bool
	fail   map[string]error
	calls  []string
}

func newFake() *fakeLocker {
	return &fakeLocker{strong: map[string]bool{}, user: map[string]bool{}, fail: map[string]error{}}
}

func (f *fakeLocker) Status(p string) (platform.State, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return platform.State{Path: p}, err
	}
	return platform.State{Path: p, IsDir: fi.IsDir(), IsSymlink: fi.Mode()&os.ModeSymlink != 0,
		Immutable: f.strong[p], Writable: !f.user[p], FSType: "fakefs"}, nil
}
func (f *fakeLocker) Lock(p string, lvl platform.Level) error {
	f.calls = append(f.calls, "lock "+filepath.Base(p))
	if err := f.fail[p]; err != nil {
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

func TestPartialFailureAndOrder(t *testing.T) {
	h := newHarness(t)
	h.root = true
	h.fake.fail[h.p("docs/specs/a.md")] = errors.New("disk on fire")
	c := h.run("lock", "-R", "--include-dirs", "--exclude", "*.tmp", "@docs")
	if c != lock.ExitPartial || !strings.Contains(h.stderr.String(), "disk on fire") {
		t.Errorf("partial: %d %s", c, h.stderr.String())
	}
	// post-order: the docs directory itself must be last
	if last := h.fake.calls[len(h.fake.calls)-1]; last != "lock docs" {
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
