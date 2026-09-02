package hook

import (
	"strings"

	"github.com/Mineru98/agent-file-lock/internal/notice"
)

// Candidate is one path a tool call would touch, with the kind of change.
type Candidate struct {
	Path string
	Op   notice.Op
}

// Commands that only read. A locked path appearing as an argument to one of
// these is never a reason to interrupt the agent.
var readOnlyCmds = map[string]bool{
	"cat": true, "bat": true, "head": true, "tail": true, "less": true, "more": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true, "ack": true,
	"ls": true, "ll": true, "tree": true, "find": true, "fd": true, "stat": true,
	"file": true, "wc": true, "diff": true, "cmp": true, "md5": true, "md5sum": true,
	"shasum": true, "sha256sum": true, "awk": true, "cut": true, "sort": true,
	"uniq": true, "jq": true, "yq": true, "which": true, "echo": true, "printf": true,
	"pwd": true, "env": true, "date": true, "afl": true, "code": true, "open": true,
}

// Commands that delete, and the argument positions that matter.
var deleteCmds = map[string]bool{"rm": true, "rmdir": true, "unlink": true, "shred": true, "trash": true}

// Commands that rename or move.
var moveCmds = map[string]bool{"mv": true, "rename": true}

// Commands that overwrite their operands.
var writeCmds = map[string]bool{
	"cp": true, "install": true, "ln": true, "tee": true, "truncate": true,
	"touch": true, "patch": true, "dd": true, "gzip": true, "gunzip": true,
	"zip": true, "unzip": true, "tar": true, "sponge": true,
}

// Commands that would clear the lock itself. Attempting one is worth its own
// message: the agent is not being told "you cannot write", it is being told
// "you may not lift the user's restriction either".
var unlockCmds = map[string]bool{"chflags": true, "chattr": true, "chmod": true, "chown": true, "xattr": true, "setfattr": true}

// git subcommands that rewrite files given as path operands.
var gitWriteSubcmds = map[string]bool{
	"checkout": true, "restore": true, "apply": true, "clean": true,
	"mv": true, "rm": true, "stash": true, "reset": true,
}

// candidatesFromCommand tokenises a shell command line and returns every path
// it would create, overwrite, move or delete. It is deliberately syntactic:
// the goal is to recognise the shapes an agent actually types, not to be a
// shell. Anything it cannot classify is left to the kernel, which still
// refuses the write.
func candidatesFromCommand(cmd string) []Candidate {
	var out []Candidate
	for _, simple := range splitSimple(tokenize(cmd)) {
		out = append(out, candidatesFromSimple(simple)...)
	}
	return out
}

func candidatesFromSimple(tokens []token) []Candidate {
	var out []Candidate
	// Redirections apply whatever the command is: `foo > locked.md`.
	var words []token
	for i := 0; i < len(tokens); i++ {
		t := tokens[i]
		if !t.quoted && isRedirect(t.text) {
			if i+1 < len(tokens) {
				out = append(out, Candidate{tokens[i+1].text, notice.OpModify})
				i++
			}
			continue
		}
		if !t.quoted {
			if op, rest, ok := splitRedirectSuffix(t.text); ok {
				if rest != "" {
					out = append(out, Candidate{rest, notice.OpModify})
				} else if i+1 < len(tokens) {
					out = append(out, Candidate{tokens[i+1].text, notice.OpModify})
					i++
				}
				_ = op
				continue
			}
		}
		words = append(words, t)
	}
	if len(words) == 0 {
		return out
	}
	name, args := commandName(words)
	if name == "" {
		return out
	}
	switch {
	case deleteCmds[name]:
		return append(out, operands(args, notice.OpDelete)...)
	case moveCmds[name]:
		return append(out, operands(args, notice.OpMove)...)
	case unlockCmds[name]:
		return append(out, operands(args, notice.OpUnlock)...)
	case writeCmds[name]:
		return append(out, operands(args, notice.OpModify)...)
	case name == "sed" || name == "perl" || name == "ruby" || name == "gsed":
		if hasInPlaceFlag(args) {
			return append(out, operands(args, notice.OpModify)...)
		}
	case name == "git":
		sub := firstOperand(args)
		if gitWriteSubcmds[sub] {
			return append(out, operands(args, notice.OpModify)...)
		}
	case name == "afl":
		if firstOperand(args) == "unlock" {
			return append(out, operands(args, notice.OpUnlock)...)
		}
	case readOnlyCmds[name]:
		return out
	}
	return out
}

// commandName strips env assignments and privilege wrappers so that
// `sudo -n env FOO=1 rm -f locked.md` is classified as `rm`.
func commandName(words []token) (string, []token) {
	for i := 0; i < len(words); i++ {
		w := words[i].text
		switch {
		case strings.Contains(w, "=") && !strings.HasPrefix(w, "-") && isAssignment(w):
			continue
		case w == "sudo" || w == "doas" || w == "env" || w == "command" || w == "nohup" || w == "time" || w == "xargs":
			continue
		case strings.HasPrefix(w, "-"):
			continue
		default:
			return base(w), words[i+1:]
		}
	}
	return "", nil
}

func isAssignment(w string) bool {
	i := strings.Index(w, "=")
	if i <= 0 {
		return false
	}
	for _, c := range w[:i] {
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func base(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// operands returns the non-flag arguments as candidates.
func operands(args []token, op notice.Op) []Candidate {
	var out []Candidate
	for _, a := range args {
		if !a.quoted && strings.HasPrefix(a.text, "-") {
			continue
		}
		if a.text == "" {
			continue
		}
		out = append(out, Candidate{a.text, op})
	}
	return out
}

func firstOperand(args []token) string {
	for _, a := range args {
		if !strings.HasPrefix(a.text, "-") {
			return a.text
		}
	}
	return ""
}

func hasInPlaceFlag(args []token) bool {
	for _, a := range args {
		if a.quoted {
			continue
		}
		if a.text == "-i" || strings.HasPrefix(a.text, "-i.") || strings.HasPrefix(a.text, "--in-place") {
			return true
		}
		// bundled short flags such as -ie or -ri
		if strings.HasPrefix(a.text, "-") && !strings.HasPrefix(a.text, "--") && strings.Contains(a.text, "i") {
			return true
		}
	}
	return false
}

func isRedirect(s string) bool {
	switch s {
	case ">", ">>", ">|", "1>", "2>", "&>", ">&", "1>>", "2>>":
		return true
	}
	return false
}

// splitRedirectSuffix handles `>file` written without a space.
func splitRedirectSuffix(s string) (string, string, bool) {
	for _, pre := range []string{">>", ">|", "1>", "2>", "&>", ">"} {
		if strings.HasPrefix(s, pre) && len(s) > len(pre) {
			return pre, s[len(pre):], true
		}
	}
	return "", "", false
}

type token struct {
	text   string
	quoted bool
	sep    string // set for operators: ; && || | & newline
}

// tokenize splits a command line into words and operators, honouring single
// and double quotes and backslash escapes. Substitutions ($(...), backticks)
// are kept verbatim inside the word; they are never treated as paths.
func tokenize(s string) []token {
	var out []token
	var cur strings.Builder
	quoted, has := false, false
	flush := func() {
		if has {
			out = append(out, token{text: cur.String(), quoted: quoted})
			cur.Reset()
			quoted, has = false, false
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				cur.WriteString(s[i+1:])
				has, quoted = true, true
				i = len(s)
				continue
			}
			cur.WriteString(s[i+1 : i+1+j])
			has, quoted = true, true
			i += j + 1
		case '"':
			j := i + 1
			var b strings.Builder
			for j < len(s) && s[j] != '"' {
				if s[j] == '\\' && j+1 < len(s) {
					j++
				}
				b.WriteByte(s[j])
				j++
			}
			cur.WriteString(b.String())
			has, quoted = true, true
			i = j
		case '\\':
			if i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
				has = true
			}
		case ' ', '\t':
			flush()
		case '\n', ';', '&', '|':
			flush()
			op := string(c)
			if i+1 < len(s) && (s[i+1] == c) && (c == '&' || c == '|') {
				op += string(c)
				i++
			}
			out = append(out, token{text: op, sep: op})
		default:
			cur.WriteByte(c)
			has = true
		}
	}
	flush()
	return out
}

// splitSimple breaks a token stream at shell operators into simple commands.
func splitSimple(tokens []token) [][]token {
	var out [][]token
	var cur []token
	for _, t := range tokens {
		if t.sep != "" {
			if len(cur) > 0 {
				out = append(out, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}
