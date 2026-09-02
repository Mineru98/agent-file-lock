// Package hook implements afl's agent-facing guard.
//
// The kernel can only answer EPERM. An agent that gets "Operation not
// permitted" back from its editor learns that a write failed, not that a
// human decided it must not happen — which is how the parent-directory
// workaround gets invented in the first place. A PreToolUse hook runs before
// the syscall and can say so in words.
//
// The protocol here is the one Claude Code introduced and Codex adopted
// verbatim (stdin JSON with tool_name/tool_input, deny via
// hookSpecificOutput.permissionDecision or exit code 2 plus stderr), so a
// single binary serves both. For any other harness the exit-code contract —
// 0 allow, 2 deny with the reason on stderr — is the lowest common
// denominator, and paths can be passed as arguments instead of JSON.
package hook

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/Mineru98/agent-file-lock/internal/lock"
	"github.com/Mineru98/agent-file-lock/internal/notice"
	"github.com/Mineru98/agent-file-lock/internal/platform"
)

// Request is the harness-independent view of one tool call.
type Request struct {
	Event string         // "PreToolUse" when the harness said so
	Tool  string         // "Edit", "Bash", "apply_patch", ...
	Cwd   string         // working directory the tool call runs in
	Input map[string]any // tool_input, when it was an object
	Raw   map[string]any // the whole payload, for generic path discovery
}

// Tools that never modify anything. Matching is case-insensitive and covers
// the names used by Claude Code, Codex, Cursor and friends.
var readOnlyTools = map[string]bool{
	"read": true, "grep": true, "glob": true, "ls": true, "search": true,
	"webfetch": true, "websearch": true, "todowrite": true, "task": true,
	"view": true, "list": true, "codebase_search": true, "read_file": true,
	"file_search": true, "grep_search": true, "list_dir": true, "shell_read": true,
}

// Keys whose string values are treated as paths wherever they appear in the
// payload. Different harnesses spell this differently; accepting all of them
// is what makes one hook binary work everywhere.
var pathKeys = map[string]bool{
	"file_path": true, "filepath": true, "path": true, "notebook_path": true,
	"target_file": true, "file": true, "filename": true, "abs_path": true,
	"destination": true, "dest": true, "dst": true, "source": true, "src": true,
	"old_path": true, "new_path": true, "target_path": true, "relative_workspace_path": true,
}

// Keys holding a shell command line.
var commandKeys = map[string]bool{"command": true, "cmd": true, "script": true, "shell_command": true}

// Keys holding a patch/diff body.
var patchKeys = map[string]bool{"patch": true, "diff": true, "input": true, "content": true, "changes": true}

// Parse reads a harness payload. An empty or unparsable body yields a request
// with no tool, which callers combine with paths given on the command line.
func Parse(body []byte) *Request {
	req := &Request{}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" || trimmed[0] != '{' {
		return req
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return req
	}
	req.Raw = raw
	req.Event = str(raw, "hook_event_name", "hookEventName", "event", "event_name")
	req.Tool = str(raw, "tool_name", "toolName", "tool")
	req.Cwd = str(raw, "cwd", "workspace_root", "working_directory")
	for _, k := range []string{"tool_input", "toolInput", "input", "arguments", "params"} {
		if m, ok := raw[k].(map[string]any); ok {
			req.Input = m
			break
		}
	}
	if req.Input == nil {
		req.Input = raw
	}
	return req
}

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// Decision is the verdict for one tool call.
type Decision struct {
	Deny    bool            `json:"deny"`
	Reasons []notice.Reason `json:"reasons,omitempty"`
	Message string          `json:"message,omitempty"`
	Checked []string        `json:"checked,omitempty"`
}

// Options tunes Evaluate.
type Options struct {
	// Strict also denies commands afl could not classify when they mention a
	// locked path, instead of leaving those to the kernel.
	Strict bool
	// Extra paths supplied on the command line (for harnesses that pass
	// arguments rather than JSON).
	Paths []string
}

// Evaluate decides whether a tool call may proceed. It never modifies
// anything and needs no privileges.
func Evaluate(req *Request, l platform.Locker, opts Options) Decision {
	var d Decision
	if req == nil {
		req = &Request{}
	}
	if req.Event != "" && !strings.EqualFold(req.Event, "PreToolUse") {
		return d // PostToolUse and friends are none of our business
	}
	tool := strings.ToLower(req.Tool)
	if tool != "" && readOnlyTools[tool] {
		return d
	}

	cands := Candidates(req, opts)
	if len(cands) == 0 {
		return d
	}
	base := req.Cwd
	if base == "" {
		base = "."
	}

	seen := map[string]bool{}
	for _, c := range cands {
		p := c.Path
		if p == "" || strings.ContainsAny(p, "*?[$`") {
			continue // globs and substitutions are not resolvable here
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(base, p)
		}
		p = filepath.Clean(p)
		key := p + "\x00" + string(c.Op)
		if seen[key] {
			continue
		}
		seen[key] = true
		d.Checked = append(d.Checked, p)

		lk, err := lock.LookupState(p, l)
		if err != nil || !lk.Exists {
			// A path that is not there yet cannot be locked, and cannot be
			// moved or deleted either. Creating it is the agent's business.
			continue
		}
		st := lk.State
		removes := c.Op == notice.OpMove || c.Op == notice.OpDelete
		switch {
		case st.Level() != 0:
			d.Deny = true
			d.Reasons = append(d.Reasons, notice.Reason{Path: p, Level: st.Level().String(), Op: c.Op})
		case st.Append && st.IsDir && (removes || c.Op == notice.OpUnlock):
			d.Deny = true
			d.Reasons = append(d.Reasons, notice.Reason{Path: p, Guard: true, Op: c.Op})
		case lk.Guard != "" && removes:
			// Creating entries in a guarded directory is fine; removing or
			// renaming one is not, and the kernel would refuse it anyway.
			d.Deny = true
			d.Reasons = append(d.Reasons, notice.Reason{Path: lk.Guard, Guard: true, Op: c.Op})
		}
	}
	if d.Deny {
		d.Message = notice.Block(d.Reasons)
	}
	return d
}

// Candidates lists every path the tool call would create, overwrite, move or
// delete, from all the shapes a harness might use.
func Candidates(req *Request, opts Options) []Candidate {
	var out []Candidate
	for _, p := range opts.Paths {
		out = append(out, Candidate{p, notice.OpModify})
	}
	if req.Input != nil {
		out = append(out, walkValue(req.Input, defaultOp(req.Tool), 0)...)
		if opts.Strict {
			// Strict mode stops trusting the command classifier: every word
			// of every command line is treated as a path that might be
			// written. It costs false positives on read-only commands, which
			// is why it is not the default.
			for _, cmd := range collectCommands(req.Input, 0) {
				for _, sc := range splitSimple(tokenize(cmd)) {
					out = append(out, operands(sc, notice.OpModify)...)
				}
			}
		}
	}
	return out
}

// collectCommands finds every shell command string in a payload.
func collectCommands(v any, depth int) []string {
	if depth > 8 {
		return nil
	}
	var out []string
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if s, ok := val.(string); ok && commandKeys[strings.ToLower(k)] {
				out = append(out, s)
				continue
			}
			out = append(out, collectCommands(val, depth+1)...)
		}
	case []any:
		for _, item := range t {
			out = append(out, collectCommands(item, depth+1)...)
		}
	}
	return out
}

// defaultOp maps a tool name to the change it performs when the payload only
// names a path.
func defaultOp(tool string) notice.Op {
	switch strings.ToLower(tool) {
	case "delete", "delete_file", "remove", "rm":
		return notice.OpDelete
	case "move", "rename", "move_file":
		return notice.OpMove
	}
	return notice.OpModify
}

// walkValue collects candidates from an arbitrary JSON value: path-like keys
// become paths, command keys are parsed as shell, patch keys are parsed as
// diffs. Depth is bounded so a pathological payload cannot spin.
func walkValue(v any, op notice.Op, depth int) []Candidate {
	if depth > 8 {
		return nil
	}
	var out []Candidate
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			lk := strings.ToLower(k)
			s, isStr := val.(string)
			switch {
			case isStr && pathKeys[lk]:
				out = append(out, Candidate{s, opForKey(lk, op)})
			case isStr && commandKeys[lk]:
				out = append(out, candidatesFromCommand(s)...)
				out = append(out, candidatesFromPatch(s)...)
			case isStr && patchKeys[lk]:
				out = append(out, candidatesFromPatch(s)...)
			default:
				out = append(out, walkValue(val, op, depth+1)...)
			}
		}
	case []any:
		for _, item := range t {
			out = append(out, walkValue(item, op, depth+1)...)
		}
	}
	return out
}

func opForKey(key string, fallback notice.Op) notice.Op {
	switch key {
	case "old_path", "source", "src":
		return notice.OpMove
	}
	return fallback
}

// candidatesFromPatch extracts the files an apply_patch envelope or a unified
// diff would write. Codex's apply_patch tool passes the whole envelope as the
// command string, so this runs over command values too.
func candidatesFromPatch(s string) []Candidate {
	if !strings.Contains(s, "*** ") && !strings.Contains(s, "+++ ") && !strings.Contains(s, "--- ") {
		return nil
	}
	var out []Candidate
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimRight(ln, "\r")
		switch {
		case strings.HasPrefix(ln, "*** Update File:"):
			out = append(out, Candidate{strings.TrimSpace(strings.TrimPrefix(ln, "*** Update File:")), notice.OpModify})
		case strings.HasPrefix(ln, "*** Add File:"):
			out = append(out, Candidate{strings.TrimSpace(strings.TrimPrefix(ln, "*** Add File:")), notice.OpModify})
		case strings.HasPrefix(ln, "*** Delete File:"):
			out = append(out, Candidate{strings.TrimSpace(strings.TrimPrefix(ln, "*** Delete File:")), notice.OpDelete})
		case strings.HasPrefix(ln, "*** Move to:"):
			out = append(out, Candidate{strings.TrimSpace(strings.TrimPrefix(ln, "*** Move to:")), notice.OpMove})
		case strings.HasPrefix(ln, "+++ ") || strings.HasPrefix(ln, "--- "):
			p := strings.TrimSpace(ln[4:])
			if i := strings.IndexAny(p, "\t"); i >= 0 {
				p = p[:i]
			}
			if p == "/dev/null" || p == "" {
				continue
			}
			p = strings.TrimPrefix(strings.TrimPrefix(p, "a/"), "b/")
			out = append(out, Candidate{p, notice.OpModify})
		}
	}
	return out
}
