package lock

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mineru98/agent-file-lock/internal/platform"
)

func mkTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"a.md", "sub/b.md", "sub/deep/c.md", "sub/x.tmp", "skip/d.md"} {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(p), 0o644)
	}
	os.Symlink(filepath.Join(root, "a.md"), filepath.Join(root, "link.md"))
	return root
}

func rels(root string, ts []Target) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		r, _ := filepath.Rel(root, t.Path)
		out[i] = filepath.ToSlash(r)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPlanFile(t *testing.T) {
	root := mkTree(t)
	ts, _, err := Plan(filepath.Join(root, "a.md"), PlanOptions{Level: platform.LevelStrong})
	if err != nil || len(ts) != 1 || ts[0].IsDir || ts[0].Level != platform.LevelStrong {
		t.Fatalf("%v %v", ts, err)
	}
}

func TestPlanDirRequiresRecursive(t *testing.T) {
	root := mkTree(t)
	if _, _, err := Plan(root, PlanOptions{}); !errors.Is(err, ErrDirNeedsRecursive) {
		t.Fatalf("want ErrDirNeedsRecursive, got %v", err)
	}
	ts, _, err := Plan(root, PlanOptions{DirOnly: true})
	if err != nil || len(ts) != 1 || !ts[0].IsDir {
		t.Fatalf("dir-only: %v %v", ts, err)
	}
}

func TestPlanRecursive(t *testing.T) {
	root := mkTree(t)
	ts, skipped, err := Plan(root, PlanOptions{Recursive: true, Exclude: []string{"*.tmp", "skip/**"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sub/deep/c.md", "sub/b.md", "a.md"}
	if got := rels(root, ts); !eq(got, want) {
		t.Errorf("targets = %v, want %v", got, want)
	}
	if len(skipped) != 1 || skipped[0].Reason != "symlink" {
		t.Errorf("skipped = %+v", skipped)
	}
}

func TestPlanIncludeDirsPostOrder(t *testing.T) {
	root := mkTree(t)
	ts, _, err := Plan(root, PlanOptions{Recursive: true, IncludeDirs: true, Exclude: []string{"*.tmp", "skip"}})
	if err != nil {
		t.Fatal(err)
	}
	got := rels(root, ts)
	if len(got) != 6 || got[len(got)-1] != "." {
		t.Fatalf("targets = %v", got)
	}
	// post-order invariant: every directory appears after everything beneath it
	for i, d := range ts {
		if !d.IsDir {
			continue
		}
		for j := i + 1; j < len(ts); j++ {
			if rel, _ := filepath.Rel(d.Path, ts[j].Path); !strings.HasPrefix(rel, "..") {
				t.Errorf("%s listed before its child %s", got[i], got[j])
			}
		}
	}
	Reverse(ts)
	if got := rels(root, ts); got[0] != "." || got[len(got)-1] != "sub/deep/c.md" {
		t.Errorf("reversed = %v", got)
	}
}

func TestPlanFollowSymlinks(t *testing.T) {
	root := mkTree(t)
	link := filepath.Join(root, "link.md")
	if _, s, _ := Plan(link, PlanOptions{}); len(s) != 1 {
		t.Errorf("symlink root should be skipped: %+v", s)
	}
	ts, _, err := Plan(link, PlanOptions{FollowSymlinks: true})
	if err != nil || len(ts) != 1 || filepath.Base(ts[0].Path) != "a.md" {
		t.Errorf("follow: %v %v", ts, err)
	}
	ts, _, _ = Plan(root, PlanOptions{Recursive: true, FollowSymlinks: true, Exclude: []string{"*.tmp", "skip"}})
	// a.md appears twice (direct + via link); plan does not dedupe, Apply is idempotent.
	if len(ts) != 4 {
		t.Errorf("follow recursive: %v", rels(root, ts))
	}
}
