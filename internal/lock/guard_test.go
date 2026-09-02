package lock

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mineru98/agent-file-lock/internal/platform"
)

// tree builds  root/docs/specs/a.md  plus root/docs/b.md and root/top.md.
func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"docs/specs/a.md", "docs/b.md", "top.md"} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// EvalSymlinks: on macOS t.TempDir() lives under /var -> /private/var, and
	// the guard compares cleaned absolute paths.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestGuardPlanCoversEveryAncestor(t *testing.T) {
	root := tree(t)
	target := Target{Path: filepath.Join(root, "docs/specs/a.md")}
	dirs, skipped := GuardPlan([]Target{target}, root, platform.LevelStrong)
	if len(skipped) != 0 {
		t.Fatalf("unexpected skips: %v", skipped)
	}
	want := []string{
		filepath.Join(root, "docs/specs"),
		filepath.Join(root, "docs"),
		root,
	}
	if len(dirs) != len(want) {
		t.Fatalf("got %d dirs %v, want %v", len(dirs), paths(dirs), want)
	}
	// deepest first, so a partial failure keeps the innermost guard
	for i, w := range want {
		if dirs[i].Path != w {
			t.Errorf("dirs[%d] = %s, want %s", i, dirs[i].Path, w)
		}
		if !dirs[i].IsDir {
			t.Errorf("%s should be marked as a directory", dirs[i].Path)
		}
	}
}

func TestGuardPlanDeduplicatesSharedAncestors(t *testing.T) {
	root := tree(t)
	dirs, _ := GuardPlan([]Target{
		{Path: filepath.Join(root, "docs/specs/a.md")},
		{Path: filepath.Join(root, "docs/b.md")},
		{Path: filepath.Join(root, "top.md")},
	}, root, platform.LevelStrong)
	seen := map[string]int{}
	for _, d := range dirs {
		seen[d.Path]++
	}
	for p, n := range seen {
		if n != 1 {
			t.Errorf("%s planned %d times", p, n)
		}
	}
	if len(dirs) != 3 {
		t.Errorf("want docs/specs, docs and root: %v", paths(dirs))
	}
}

func TestGuardPlanStopsAtRoot(t *testing.T) {
	root := tree(t)
	sub := filepath.Join(root, "docs")
	dirs, _ := GuardPlan([]Target{{Path: filepath.Join(root, "docs/specs/a.md")}}, sub, platform.LevelStrong)
	for _, d := range dirs {
		if d.Path == root {
			t.Errorf("guard walked above the root: %v", paths(dirs))
		}
	}
	if len(dirs) != 2 {
		t.Errorf("want docs/specs and docs: %v", paths(dirs))
	}
}

func TestGuardPlanReportsTargetsOutsideRoot(t *testing.T) {
	root := tree(t)
	other := t.TempDir()
	dirs, skipped := GuardPlan([]Target{{Path: filepath.Join(other, "x.md")}}, root, platform.LevelStrong)
	if len(skipped) != 1 || !strings.Contains(skipped[0].Reason, "outside the guard root") {
		t.Fatalf("expected an outside-root skip, got %v", skipped)
	}
	if len(dirs) != 1 {
		t.Errorf("its own parent should still be guarded: %v", paths(dirs))
	}
}

func TestCheckGuardRootRefusesBroadBoundaries(t *testing.T) {
	if err := CheckGuardRoot("/"); err == nil {
		t.Error("/ must be refused")
	}
	if home, err := os.UserHomeDir(); err == nil {
		if err := CheckGuardRoot(home); err == nil {
			t.Error("$HOME must be refused")
		}
	}
	if err := CheckGuardRoot("/tmp"); err == nil {
		t.Error("a top-level directory must be refused")
	}
	if err := CheckGuardRoot(t.TempDir()); err != nil {
		t.Errorf("a project directory must be accepted: %v", err)
	}
}

func TestGuardRootPrefersConfigThenGit(t *testing.T) {
	root := tree(t)
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := GuardRoot("", "", filepath.Join(root, "docs/specs/a.md"))
	if err != nil || got != root {
		t.Errorf("git root: %s %v, want %s", got, err, root)
	}
	cfg := filepath.Join(root, "docs", "afl.yaml")
	got, err = GuardRoot("", cfg, filepath.Join(root, "docs/specs/a.md"))
	if err != nil || got != filepath.Join(root, "docs") {
		t.Errorf("config dir wins over git root: %s %v", got, err)
	}
	got, err = GuardRoot(filepath.Join(root, "docs", "specs"), cfg, "")
	if err != nil || got != filepath.Join(root, "docs", "specs") {
		t.Errorf("explicit wins over everything: %s %v", got, err)
	}
	if _, err := GuardRoot(filepath.Join(root, "top.md"), "", ""); err == nil {
		t.Error("a file is not a guard root")
	}
}

func TestApplyAndReleaseGuard(t *testing.T) {
	root := tree(t)
	f := newFake()
	docs := filepath.Join(root, "docs")
	locked := filepath.Join(root, "docs/b.md")
	f.add(docs, platform.State{IsDir: true, Writable: true})
	f.add(root, platform.State{IsDir: true, Writable: true})
	f.add(locked, platform.State{Immutable: true, Writable: false})

	dirs := []Target{{Path: docs, IsDir: true, Level: platform.LevelStrong}, {Path: root, IsDir: true, Level: platform.LevelStrong}}
	res, sum := ApplyGuard(dirs, f, false)
	if sum.Failed != 0 || sum.Changed != 2 {
		t.Fatalf("apply: %+v %v", sum, res)
	}
	for _, d := range dirs {
		if st, _ := f.Status(d.Path); !st.Append {
			t.Errorf("%s not guarded", d.Path)
		}
	}
	// a second pass is a no-op
	if _, sum := ApplyGuard(dirs, f, false); sum.Changed != 0 || sum.Skipped != 2 {
		t.Errorf("idempotent apply: %+v", sum)
	}

	// a locked file beneath keeps the guard in place
	res, _ = ReleaseGuard(dirs, f, false)
	for _, r := range res {
		if r.Outcome != OutcomeSkipped || !strings.Contains(r.Error, "remain beneath") {
			t.Errorf("guard should be kept while %s is locked: %+v", locked, r)
		}
	}

	// once nothing is locked, the guard is released
	f.add(locked, platform.State{Writable: true})
	if _, sum := ReleaseGuard(dirs, f, false); sum.Changed != 2 {
		t.Errorf("release: %+v", sum)
	}
	for _, d := range dirs {
		if st, _ := f.Status(d.Path); st.Append {
			t.Errorf("%s still guarded", d.Path)
		}
	}
}

func TestApplyGuardVerifies(t *testing.T) {
	root := tree(t)
	f := newFake()
	f.add(root, platform.State{IsDir: true, Writable: true})
	f.silent[root] = true // a filesystem that accepts the flag and drops it
	res, sum := ApplyGuard([]Target{{Path: root, IsDir: true, Level: platform.LevelStrong}}, f, false)
	if sum.Failed != 1 || res[0].Err != ErrVerify {
		t.Errorf("silent failure must be caught: %+v %v", sum, res)
	}
}

func TestGuardDryRunChangesNothing(t *testing.T) {
	root := tree(t)
	f := newFake()
	f.add(root, platform.State{IsDir: true, Writable: true})
	res, _ := ApplyGuard([]Target{{Path: root, IsDir: true, Level: platform.LevelStrong}}, f, true)
	if res[0].Outcome != OutcomePlanned {
		t.Errorf("dry run: %+v", res[0])
	}
	if st, _ := f.Status(root); st.Append {
		t.Error("dry run set the flag")
	}
}

func paths(ts []Target) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Path
	}
	return out
}
