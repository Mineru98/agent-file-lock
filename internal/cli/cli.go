// Package cli implements the afl command line.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Mineru98/agent-file-lock/internal/lock"
	"github.com/Mineru98/agent-file-lock/internal/platform"
)

// Version is injected by main via -ldflags.
var Version = "dev"

// Deps lets tests substitute the platform layer.
type Deps struct {
	Locker   platform.Locker
	StrongOK func() (bool, string)
	IsRoot   func() bool
	Elevate  func(args []string) error // re-exec through sudo; returns only on failure
	Env      *env
}

type env struct {
	stdout, stderr io.Writer
	deps           Deps
}

// Run executes afl with the real platform layer.
func Run(args []string, stdout, stderr io.Writer) int {
	return RunWith(args, stdout, stderr, Deps{})
}

// RunWith executes afl with injected dependencies (nil fields use defaults).
func RunWith(args []string, stdout, stderr io.Writer, d Deps) int {
	if d.Locker == nil {
		d.Locker = platform.New()
	}
	if d.StrongOK == nil {
		d.StrongOK = platform.StrongPrivilege
	}
	if d.IsRoot == nil {
		d.IsRoot = platform.IsRoot
	}
	if d.Elevate == nil {
		d.Elevate = elevate
	}
	e := &env{stdout: stdout, stderr: stderr, deps: d}
	if len(args) == 0 {
		e.usage()
		return lock.ExitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "lock":
		return e.cmdLock(rest, lock.ActionLock)
	case "unlock":
		return e.cmdLock(rest, lock.ActionUnlock)
	case "status":
		return e.cmdStatus(rest)
	case "check":
		return e.cmdCheck(rest)
	case "doctor":
		return e.cmdDoctor(rest)
	case "completion":
		return e.cmdCompletion(rest)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "afl %s\n", Version)
		return lock.ExitOK
	case "help", "--help", "-h":
		e.usage()
		return lock.ExitOK
	}
	fmt.Fprintf(stderr, "afl: unknown command %q\n\n", cmd)
	e.usage()
	return lock.ExitUsage
}

func (e *env) usage() {
	fmt.Fprint(e.stdout, `afl — lock files that coding agents must never modify

Usage:
  afl lock   [flags] <path>...        lock files (strong = immutable, needs root)
  afl lock   [flags] -f <config>      lock everything listed in afl.yaml / afl.json
  afl unlock [flags] <path>... | -f <config>
  afl status [flags] <path>... | -f <config>
  afl check  -f <config>              exit 1 if any protected path is not locked (CI / pre-commit)
  afl doctor [--json] [<path>]        diagnose OS, privileges, filesystem support
  afl completion bash|zsh|fish        print shell completion script
  afl version

Flags (lock / unlock / status):
  -f, --config <file>   YAML or JSON config (paths resolve relative to the file)
  -R, --recursive       directories: act on every regular file beneath
      --include-dirs    with -R: also lock directory inodes (blocks new files)
      --dir-only        act on the directory inode only, not its contents
      --level strong|user   strong = chattr +i / schg (default); user = chmod a-w (+uchg)
      --exclude <glob>  skip matching paths (repeatable; ** supported)
      --follow-symlinks act on symlink targets instead of skipping them
  -n, --dry-run         print the plan without changing anything
      --fail-fast       stop at the first failure
      --json            machine-readable output
  -q, --quiet           only print failures
      --elevate         re-exec through sudo when not running as root

Exit codes: 0 ok · 1 partial failure / check drift · 2 usage · 3 privileges · 4 unsupported filesystem
`)
}

// common holds the flags shared by lock/unlock/status/check.
type common struct {
	config         string
	recursive      bool
	includeDirs    bool
	dirOnly        bool
	level          string
	exclude        multiFlag
	followSymlinks bool
	dryRun         bool
	failFast       bool
	json           bool
	quiet          bool
	elevate        bool
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }

func (e *env) newFlagSet(name string, c *common) *flag.FlagSet {
	fs := flag.NewFlagSet("afl "+name, flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.Usage = func() { e.usage() }
	fs.StringVar(&c.config, "f", "", "config file")
	fs.StringVar(&c.config, "config", "", "config file")
	fs.BoolVar(&c.recursive, "R", false, "recursive")
	fs.BoolVar(&c.recursive, "recursive", false, "recursive")
	fs.BoolVar(&c.includeDirs, "include-dirs", false, "include directory inodes")
	fs.BoolVar(&c.dirOnly, "dir-only", false, "directory inode only")
	fs.StringVar(&c.level, "level", "", "strong|user")
	fs.Var(&c.exclude, "exclude", "glob to skip")
	fs.BoolVar(&c.followSymlinks, "follow-symlinks", false, "follow symlinks")
	fs.BoolVar(&c.dryRun, "n", false, "dry run")
	fs.BoolVar(&c.dryRun, "dry-run", false, "dry run")
	fs.BoolVar(&c.failFast, "fail-fast", false, "stop at first failure")
	fs.BoolVar(&c.json, "json", false, "json output")
	fs.BoolVar(&c.quiet, "q", false, "quiet")
	fs.BoolVar(&c.quiet, "quiet", false, "quiet")
	fs.BoolVar(&c.elevate, "elevate", false, "re-exec via sudo")
	return fs
}

// parseInterleaved lets flags appear after positional arguments
// (`afl lock docs/a.md -R` works like `afl lock -R docs/a.md`).
func parseInterleaved(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		i := 0
		for i < len(rest) && !strings.HasPrefix(rest[i], "-") {
			positional = append(positional, rest[i])
			i++
		}
		if i == len(rest) {
			return positional, nil
		}
		if rest[i] == "--" {
			return append(positional, rest[i+1:]...), nil
		}
		args = rest[i:]
	}
}

func (e *env) usageErr(format string, a ...any) int {
	fmt.Fprintf(e.stderr, "afl: "+format+"\n", a...)
	return lock.ExitUsage
}

// mapPlanErr converts plan-time failures into exit codes.
func mapPlanErr(err error) int {
	switch {
	case errors.Is(err, lock.ErrDirNeedsRecursive):
		return lock.ExitUsage
	case errors.Is(err, platform.ErrPermission):
		return lock.ExitPermission
	}
	return lock.ExitPartial
}
