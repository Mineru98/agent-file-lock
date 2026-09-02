package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mineru98/agent-file-lock/internal/hook"
	"github.com/Mineru98/agent-file-lock/internal/lock"
)

func (e *env) cmdHook(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "install":
			return e.cmdHookInstall(args[1:], true)
		case "uninstall":
			return e.cmdHookInstall(args[1:], false)
		case "print":
			return e.cmdHookPrint(args[1:])
		case "check":
			return e.cmdHookRun(args[1:], true)
		}
	}
	return e.cmdHookRun(args, false)
}

// cmdHookRun is the hook proper: read the tool call, decide, answer in the
// dialect the harness understands. It is also `afl hook check <path>...`,
// which any harness or pre-commit script can call with plain arguments.
func (e *env) cmdHookRun(args []string, checkMode bool) int {
	fs := flag.NewFlagSet("afl hook", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	// `hook check` is run by humans and by scripts that only read the exit
	// code, so it stays quiet on stdout unless a format is asked for.
	defaultFormat := "auto"
	if checkMode {
		defaultFormat = "exit-code"
	}
	format := fs.String("format", defaultFormat, "auto|json|exit-code")
	strict := fs.Bool("strict", false, "also deny unclassifiable commands that mention a locked path")
	quiet := fs.Bool("q", false, "no output, exit code only")
	fs.BoolVar(quiet, "quiet", false, "no output, exit code only")
	paths, err := parseInterleaved(fs, args)
	if err != nil {
		return lock.ExitUsage
	}
	f, err := hook.ParseFormat(*format)
	if err != nil {
		return e.usageErr("%v", err)
	}

	var body []byte
	if !checkMode {
		body = readStdin(e.stdin)
	}
	req := hook.Parse(body)
	if req.Cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			req.Cwd = wd
		}
	}
	d := hook.Evaluate(req, e.deps.Locker, hook.Options{Strict: *strict, Paths: paths})
	if *quiet {
		if d.Deny {
			return hook.ExitDeny
		}
		return hook.ExitAllow
	}
	return hook.Write(d, f, e.stdout, e.stderr)
}

// readStdin returns the payload when one is piped in, and nothing when stdin
// is a terminal — so `afl hook check <path>` from a shell does not hang.
func readStdin(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	if f, ok := r.(*os.File); ok {
		fi, err := f.Stat()
		if err != nil || fi.Mode()&os.ModeCharDevice != 0 {
			return nil
		}
	}
	b, err := io.ReadAll(io.LimitReader(r, 4<<20))
	if err != nil {
		return nil
	}
	return b
}

func (e *env) cmdHookInstall(args []string, install bool) int {
	fs := flag.NewFlagSet("afl hook install", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	global := fs.Bool("global", false, "write to the user-level config instead of the project one")
	fs.BoolVar(global, "user", false, "alias for --global")
	project := fs.Bool("project", false, "write to the project config without asking")
	all := fs.Bool("all", false, "every known harness")
	rest, err := parseInterleaved(fs, args)
	if err != nil {
		return lock.ExitUsage
	}
	if *global && *project {
		return e.usageErr("--project and --global are mutually exclusive")
	}
	var targets []hook.Harness
	switch {
	case *all:
		targets = hook.Harnesses
	case len(rest) == 1:
		h, ok := hook.FindHarness(rest[0])
		if !ok {
			return e.usageErr("unknown harness %q (known: %s)", rest[0], strings.Join(hook.HarnessNames(), ", "))
		}
		targets = []hook.Harness{h}
	default:
		return e.usageErr("hook %s needs one harness (%s) or --all",
			map[bool]string{true: "install", false: "uninstall"}[install], strings.Join(hook.HarnessNames(), ", "))
	}

	// Neither scope was named: ask, because writing to the wrong one is the
	// difference between protecting this repository and protecting all of
	// them. A non-interactive stdin (a script, a pipe, CI) gets the default
	// instead of a prompt nobody can answer.
	toUser := *global
	if !*global && !*project {
		toUser = e.askScope(targets, install)
	}

	code := lock.ExitOK
	for _, h := range targets {
		path, err := configPath(h, toUser)
		if err != nil {
			fmt.Fprintf(e.stderr, "afl: %v\n", err)
			code = lock.ExitPartial
			continue
		}
		var changed bool
		if install {
			changed, err = hook.Install(path, h)
		} else {
			changed, err = hook.Uninstall(path)
		}
		if err != nil {
			fmt.Fprintf(e.stderr, "afl: %v\n", err)
			code = lock.ExitPartial
			continue
		}
		verb := map[bool]string{true: "installed", false: "removed"}[install]
		if !changed {
			verb = map[bool]string{true: "already installed", false: "not present"}[install]
		}
		fmt.Fprintf(e.stdout, "%-15s%s (%s)\n", "["+h.Name+"]", verb, path)
		if install && changed && h.Note != "" {
			fmt.Fprintf(e.stdout, "%-15s%s\n", "", h.Note)
		}
	}
	if install && code == lock.ExitOK {
		fmt.Fprintf(e.stdout, "\nThe hook refuses edits to locked paths before the tool runs and tells the\nagent why. It needs no privileges. Verify with: afl hook check <locked path>\n")
	}
	return code
}

// askScope asks where the hook belongs and returns true for the user scope.
// The answer defaults to the project, so an empty line — or a stdin that is
// not a terminal — installs into the repository the user is standing in.
func (e *env) askScope(targets []hook.Harness, install bool) bool {
	if !isTerminal(e.stdin) {
		return false
	}
	names := make([]string, 0, len(targets))
	for _, h := range targets {
		names = append(names, h.Name)
	}
	verb := "installed"
	if !install {
		verb = "removed"
	}
	fmt.Fprintf(e.stdout, "Where should the %s hook be %s?\n", strings.Join(names, " + "), verb)
	fmt.Fprintf(e.stdout, "  1) this project — %s   (default)\n", scopeExample(targets, false))
	fmt.Fprintf(e.stdout, "  2) your user    — %s, and every other repository\n", scopeExample(targets, true))

	in := bufio.NewReader(e.stdin)
	for attempt := 0; attempt < 3; attempt++ {
		fmt.Fprint(e.stdout, "scope [1/2] (default 1): ")
		line, err := in.ReadString('\n')
		if toUser, ok := parseScopeAnswer(line); ok {
			return toUser
		}
		if err != nil { // EOF mid-answer: take the default rather than loop
			return false
		}
		fmt.Fprintf(e.stdout, "afl: answer 1 (this project) or 2 (your user)\n")
	}
	fmt.Fprintf(e.stdout, "afl: no answer, using this project\n")
	return false
}

// parseScopeAnswer reads one reply to the scope question. An empty line is
// the default (this project); anything unrecognised is not an answer.
func parseScopeAnswer(line string) (toUser, ok bool) {
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "1", "p", "project", "local", "repo":
		return false, true
	case "2", "u", "user", "g", "global", "home":
		return true, true
	}
	return false, false
}

// scopeExample names the file the scope writes to: the exact path when one
// harness was asked for, and the directory when several were.
func scopeExample(targets []hook.Harness, global bool) string {
	if len(targets) == 1 {
		if p, err := configPath(targets[0], global); err == nil {
			return prettyHome(p)
		}
	}
	if global {
		return prettyHome(filepath.Join(homeOr("~"), "…"))
	}
	if wd, err := os.Getwd(); err == nil {
		return filepath.Join(wd, "…")
	}
	return "the config in this directory"
}

func homeOr(fallback string) string {
	if home, err := os.UserHomeDir(); err == nil {
		return home
	}
	return fallback
}

// prettyHome shortens $HOME to ~, which is how people read these paths.
func prettyHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(p, home+string(filepath.Separator)) {
		return p
	}
	return "~" + p[len(home):]
}

// isTerminal reports whether r is a terminal a question can be asked on.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func configPath(h hook.Harness, global bool) (string, error) {
	if global {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, h.Global), nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(wd, h.Project), nil
}

func (e *env) cmdHookPrint(args []string) int {
	if len(args) == 0 {
		return e.usageErr("hook print needs a harness (known: %s)", strings.Join(hook.HarnessNames(), ", "))
	}
	name := args[0]
	h, ok := hook.FindHarness(name)
	if !ok {
		return e.usageErr("unknown harness %q (known: %s)", name, strings.Join(hook.HarnessNames(), ", "))
	}
	fmt.Fprintf(e.stdout, "# %s — %s (project) or ~/%s (user)\n%s", h.Name, h.Project, h.Global, hook.Snippet(h))
	if h.TOMLSnippet != "" {
		fmt.Fprintf(e.stdout, "\n# or, inline in config.toml:\n%s", h.TOMLSnippet)
	}
	if h.Note != "" {
		fmt.Fprintf(e.stdout, "\n# %s\n", h.Note)
	}
	return lock.ExitOK
}
