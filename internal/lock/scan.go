package lock

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"github.com/Mineru98/agent-file-lock/internal/platform"
)

// SkipDirs are directories a scan walks past by default. They are large,
// machine-generated, and nothing in them is ever hand-locked; walking them is
// most of what would make a bare `afl status` feel slow.
var SkipDirs = []string{
	".git", ".hg", ".svn", "node_modules", "vendor", "target", "dist", "build",
	".venv", "venv", "__pycache__", ".mypy_cache", ".pytest_cache", ".tox",
	".next", ".nuxt", ".svelte-kit", ".gradle", ".idea", ".terraform",
	"DerivedData", ".cache", "Pods", ".stack-work", "bin", "obj",
}

// ScanOptions tunes Scan.
type ScanOptions struct {
	Root     string   // directory to walk (default ".")
	All      bool     // do not skip SkipDirs or dot-directories
	MaxDepth int      // 0 = unlimited
	Exclude  []string // additional globs, matched against the path relative to Root
	Workers  int      // 0 = GOMAXPROCS (only used when the Locker needs a syscall per file)
}

// Found is one entry a scan reports.
type Found struct {
	Path  string         `json:"path"`
	Rel   string         `json:"rel"`
	IsDir bool           `json:"is_dir"`
	Level string         `json:"level,omitempty"` // "strong" / "user" for locked files
	Guard bool           `json:"guard,omitempty"` // append-only directory
	State platform.State `json:"state"`
}

// ScanResult is what a scan saw, including how much ground it covered so the
// caller can say "nothing is locked" honestly rather than by omission.
type ScanResult struct {
	Root    string    `json:"root"`
	Files   int       `json:"files_scanned"`
	Dirs    int       `json:"dirs_scanned"`
	Locked  []Found   `json:"locked"`
	Guards  []Found   `json:"guards"`
	Skipped []Skipped `json:"skipped,omitempty"`
}

// Scan walks Root and reports every locked file and every append-only guard
// directory beneath it. It reads state and never changes anything, so it needs
// no privileges.
//
// On BSD/macOS the Locker satisfies platform.FastStatuser and the whole scan
// costs one lstat per entry. On Linux the inode flags need an ioctl on an open
// descriptor, so those lookups are spread over a worker pool.
func Scan(opts ScanOptions, l platform.Locker) (ScanResult, error) {
	root := opts.Root
	if root == "" {
		root = "."
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return ScanResult{}, err
	}
	for _, g := range opts.Exclude {
		if err := ValidateGlob(g); err != nil {
			return ScanResult{}, err
		}
	}
	res := ScanResult{Root: absRoot}
	fast, hasFast := l.(platform.FastStatuser)

	skip := map[string]bool{}
	if !opts.All {
		for _, d := range SkipDirs {
			skip[d] = true
		}
	}

	type job struct {
		path string
		rel  string
		dir  bool
	}
	var jobs []job

	walkErr := filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			res.Skipped = append(res.Skipped, Skipped{p, err.Error()})
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(absRoot, p)
		if p == absRoot {
			rel = "."
		}
		if d.IsDir() && p != absRoot {
			name := d.Name()
			if skip[name] || (!opts.All && len(name) > 1 && name[0] == '.') {
				return fs.SkipDir
			}
			if opts.MaxDepth > 0 && len(splitClean(filepath.ToSlash(rel))) > opts.MaxDepth {
				return fs.SkipDir
			}
		}
		if p != absRoot && excluded(rel, opts.Exclude) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // a symlink carries no lock of its own
		}
		if d.IsDir() {
			res.Dirs++
		} else if d.Type().IsRegular() {
			res.Files++
		} else {
			return nil
		}
		if hasFast {
			fi, err := d.Info()
			if err != nil {
				res.Skipped = append(res.Skipped, Skipped{p, err.Error()})
				return nil
			}
			if st, ok := fast.StateFromInfo(p, fi); ok {
				collect(&res, p, rel, d.IsDir(), st)
				return nil
			}
		}
		jobs = append(jobs, job{p, rel, d.IsDir()})
		return nil
	})
	if walkErr != nil {
		return res, walkErr
	}
	if len(jobs) > 0 {
		workers := opts.Workers
		if workers <= 0 {
			workers = runtime.GOMAXPROCS(0)
		}
		if workers > len(jobs) {
			workers = len(jobs)
		}
		states := make([]platform.State, len(jobs))
		errs := make([]error, len(jobs))
		var wg sync.WaitGroup
		ch := make(chan int)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for idx := range ch {
					states[idx], errs[idx] = l.Status(jobs[idx].path)
				}
			}()
		}
		for i := range jobs {
			ch <- i
		}
		close(ch)
		wg.Wait()
		for i, j := range jobs {
			if errs[i] != nil {
				res.Skipped = append(res.Skipped, Skipped{j.path, errs[i].Error()})
				continue
			}
			collect(&res, j.path, j.rel, j.dir, states[i])
		}
	}
	sort.Slice(res.Locked, func(i, j int) bool { return res.Locked[i].Rel < res.Locked[j].Rel })
	sort.Slice(res.Guards, func(i, j int) bool { return res.Guards[i].Rel < res.Guards[j].Rel })
	return res, nil
}

func collect(res *ScanResult, path, rel string, isDir bool, st platform.State) {
	if lvl := st.Level(); lvl != 0 {
		// A directory that is merely read-only (0755 has no group/other write
		// bit anyway) is not evidence of an afl lock; only flag directories
		// that really carry an immutable flag.
		if !isDir || st.Immutable || st.UserImmutable {
			res.Locked = append(res.Locked, Found{Path: path, Rel: rel, IsDir: isDir, Level: lvl.String(), State: st})
		}
	}
	if st.Append && isDir {
		res.Guards = append(res.Guards, Found{Path: path, Rel: rel, IsDir: true, Guard: true, State: st})
	}
}

// Lookup is what the hook needs to know about one path: its own state,
// whether it exists at all, and the nearest guarded ancestor — the directory
// whose append-only flag is what makes a rename of that branch fail.
type Lookup struct {
	Path   string
	State  platform.State
	Exists bool
	Guard  string // nearest append-only ancestor, "" if none
}

// LookupState resolves path without changing anything. A path that does not
// exist is reported as such rather than as locked: the zero State has no write
// bits set, and reading that as "locked" would refuse writes to files the
// agent is about to create.
func LookupState(path string, l platform.Locker) (Lookup, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Lookup{Path: path}, err
	}
	out := Lookup{Path: abs}
	st, err := l.Status(abs)
	switch {
	case err == nil:
		out.State, out.Exists = st, true
	case os.IsNotExist(err):
		// keep going: the guarded ancestor still matters for a create
	default:
		return out, err
	}
	dir := filepath.Dir(abs)
	for {
		if st, e := l.Status(dir); e == nil && st.Append {
			out.Guard = dir
			return out, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return out, nil
		}
		dir = parent
	}
}
