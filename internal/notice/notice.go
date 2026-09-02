// Package notice holds the one English text afl shows an agent that tries to
// modify a protected path. Every channel (the PreToolUse hook, `afl guard
// check`, the CLI) renders the same wording so the agent cannot conclude that
// one refusal is softer than another.
//
// The kernel itself can only answer EPERM ("Operation not permitted"); it
// carries no room for an explanation. That is why the text lives here and is
// delivered by a hook that runs *before* the syscall, not after it.
package notice

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Op is the kind of change that was attempted, used in the first line.
type Op string

const (
	OpModify Op = "modify"
	OpMove   Op = "move or rename"
	OpDelete Op = "delete"
	OpUnlock Op = "unlock"
)

// Headline is the sentence the user asked for, and the only line that must
// survive truncation by a harness that shortens hook output.
const Headline = "The user has NOT authorized this agent to modify this file."

// UnlockHint tells the agent what to do instead of trying harder.
const UnlockHint = "If the change is genuinely required, stop and ask the user to unlock it:\n" +
	"  sudo afl --help        # then, once the user agrees:\n" +
	"  sudo afl unlock <path>"

// Reason describes why a path is refused. Level is "strong", "user" or "" and
// Guard marks a directory that is append-only because something beneath it is
// locked.
type Reason struct {
	Path  string
	Level string
	Guard bool
	Op    Op
}

// Block renders the full refusal for one or more paths.
func Block(reasons []Reason) string {
	if len(reasons) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("BLOCKED by agent-file-lock (afl)\n\n")
	b.WriteString(Headline + "\n\n")
	for _, r := range reasons {
		b.WriteString("  " + line(r) + "\n")
	}
	b.WriteString("\nThese paths are locked at the kernel level (macOS schg / Linux chattr +i)\n")
	b.WriteString("and their parent directories are append-only, so the usual workarounds are\n")
	b.WriteString("closed too and are treated as a violation of the user's instruction:\n")
	b.WriteString("  - renaming or replacing a parent directory to recreate the path\n")
	b.WriteString("  - writing the content to a different path and calling it done\n")
	b.WriteString("  - clearing the flag with chflags / chattr / sudo\n\n")
	b.WriteString(UnlockHint + "\n")
	return b.String()
}

// Short is the single-line form used where only one line fits.
func Short(r Reason) string { return Headline + " " + line(r) }

func line(r Reason) string {
	p := display(r.Path)
	op := r.Op
	if op == "" {
		op = OpModify
	}
	if r.Guard {
		return fmt.Sprintf("%s — append-only: it holds locked files, so it cannot be renamed or emptied (attempted: %s)", p, op)
	}
	lvl := r.Level
	if lvl == "" {
		lvl = "strong"
	}
	return fmt.Sprintf("%s — locked by the user (level: %s, attempted: %s)", p, lvl, op)
}

// display shortens an absolute path to something readable in a hook message
// without hiding which file is meant.
func display(p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	if wd, err := filepath.Abs("."); err == nil {
		if rel, err := filepath.Rel(wd, p); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return p
}
