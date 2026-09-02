package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Mineru98/agent-file-lock/internal/hook"
	"github.com/Mineru98/agent-file-lock/internal/lock"
	"github.com/Mineru98/agent-file-lock/internal/notice"
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
	all := fs.Bool("all", false, "every known harness")
	rest, err := parseInterleaved(fs, args)
	if err != nil {
		return lock.ExitUsage
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

	code := lock.ExitOK
	for _, h := range targets {
		path, err := configPath(h, *global)
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
	name := "generic"
	if len(args) > 0 {
		name = args[0]
	}
	if strings.EqualFold(name, "generic") {
		fmt.Fprint(e.stdout, hook.GenericContract)
		fmt.Fprintf(e.stdout, "\nThe message the agent receives:\n\n%s\n",
			indent(notice.Block([]notice.Reason{{Path: "docs/SSOT.md", Level: "strong", Op: notice.OpModify}}), "    "))
		return lock.ExitOK
	}
	h, ok := hook.FindHarness(name)
	if !ok {
		return e.usageErr("unknown harness %q (known: %s)", name, strings.Join(hook.PrintNames(), ", "))
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

func indent(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}
