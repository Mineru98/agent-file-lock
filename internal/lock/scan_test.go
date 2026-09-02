package lock

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Mineru98/agent-file-lock/internal/platform"
)

func scanTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{
		"docs/SSOT.md", "docs/other.md", "src/main.go",
		"node_modules/pkg/index.js", ".git/config", "deep/a/b/c/d.md",
	} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(p), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func TestScanFindsLocksAndGuards(t *testing.T) {
	root := scanTree(t)
	f := newFake()
	f.add(filepath.Join(root, "docs/SSOT.md"), platform.State{Immutable: true})
	f.add(filepath.Join(root, "docs"), platform.State{IsDir: true, Append: true, Writable: true})
	f.add(root, platform.State{IsDir: true, Append: true, Writable: true})

	res, err := Scan(ScanOptions{Root: root}, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Locked) != 1 || res.Locked[0].Rel != filepath.Join("docs", "SSOT.md") {
		t.Errorf("locked: %+v", res.Locked)
	}
	if res.Locked[0].Level != "strong" {
		t.Errorf("level: %q", res.Locked[0].Level)
	}
	if len(res.Guards) != 2 {
		t.Errorf("guards: %+v", res.Guards)
	}
}

// The point of skipping build directories is that a bare `afl status` stays
// fast on a real repository; the scan must also say so rather than silently
// hiding a lock.
func TestScanSkipsNoiseDirectoriesUnlessAsked(t *testing.T) {
	root := scanTree(t)
	f := newFake()
	buried := filepath.Join(root, "node_modules/pkg/index.js")
	f.add(buried, platform.State{Immutable: true})

	res, err := Scan(ScanOptions{Root: root}, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Locked) != 0 {
		t.Errorf("node_modules should be skipped by default: %+v", res.Locked)
	}
	res, err = Scan(ScanOptions{Root: root, All: true}, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Locked) != 1 {
		t.Errorf("-a should find it: %+v", res.Locked)
	}
	// .git is skipped in both modes only when not All; with All it is walked
	if res.Files < 5 {
		t.Errorf("All mode walked too little: %d files", res.Files)
	}
}

func TestScanRespectsDepthAndExclude(t *testing.T) {
	root := scanTree(t)
	f := newFake()
	deep := filepath.Join(root, "deep/a/b/c/d.md")
	f.add(deep, platform.State{Immutable: true})

	res, _ := Scan(ScanOptions{Root: root, MaxDepth: 2}, f)
	if len(res.Locked) != 0 {
		t.Errorf("depth 2 should not reach %s: %+v", deep, res.Locked)
	}
	res, _ = Scan(ScanOptions{Root: root}, f)
	if len(res.Locked) != 1 {
		t.Errorf("unbounded scan should reach it: %+v", res.Locked)
	}
	res, _ = Scan(ScanOptions{Root: root, Exclude: []string{"deep/**"}}, f)
	if len(res.Locked) != 0 {
		t.Errorf("exclude ignored: %+v", res.Locked)
	}
}

func TestScanCountsWhatItCovered(t *testing.T) {
	root := scanTree(t)
	res, err := Scan(ScanOptions{Root: root}, newFake())
	if err != nil {
		t.Fatal(err)
	}
	if res.Files == 0 || res.Dirs == 0 {
		t.Errorf("counters not filled: %+v", res)
	}
	if res.Root != root {
		t.Errorf("root = %s, want %s", res.Root, root)
	}
}

func TestLookupStateSeparatesMissingFromLocked(t *testing.T) {
	root := scanTree(t)
	f := newFake()
	locked := filepath.Join(root, "docs/SSOT.md")
	f.add(locked, platform.State{Immutable: true})
	f.add(filepath.Join(root, "docs"), platform.State{IsDir: true, Append: true, Writable: true})
	f.add(filepath.Join(root, "docs/other.md"), platform.State{Writable: true})

	lk, err := LookupState(locked, f)
	if err != nil || !lk.Exists || lk.State.Level() != platform.LevelStrong {
		t.Fatalf("locked: %+v %v", lk, err)
	}
	if lk.Guard != filepath.Join(root, "docs") {
		t.Errorf("guard = %q", lk.Guard)
	}

	// A path that does not exist must never look locked: the zero State has no
	// write bits, and reading that as a lock would refuse new files.
	lk, err = LookupState(filepath.Join(root, "docs/nope.md"), f)
	if err != nil {
		t.Fatal(err)
	}
	if lk.Exists {
		t.Error("missing path reported as existing")
	}
	if lk.Guard == "" {
		t.Error("a missing path still has a guarded ancestor")
	}

	lk, _ = LookupState(filepath.Join(root, "docs/other.md"), f)
	if !lk.Exists || lk.State.Level() != 0 {
		t.Errorf("free file: %+v", lk)
	}
}
