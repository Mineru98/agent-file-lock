package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mineru98/agent-file-lock/internal/notice"
	"github.com/Mineru98/agent-file-lock/internal/platform"
)

// fakeLocker reports lock state for paths registered up front. Everything
// registered exists; anything else is reported as missing, which is what
// separates "not locked" from "not there".
type fakeLocker struct {
	locked map[string]bool
	guard  map[string]bool
	dirs   map[string]bool
	exists map[string]bool
}

func newFake() *fakeLocker {
	return &fakeLocker{locked: map[string]bool{}, guard: map[string]bool{}, dirs: map[string]bool{}, exists: map[string]bool{}}
}

func (f *fakeLocker) Status(p string) (platform.State, error) {
	if !f.exists[p] {
		return platform.State{Path: p}, os.ErrNotExist
	}
	return platform.State{
		Path: p, IsDir: f.dirs[p], Immutable: f.locked[p], Append: f.guard[p],
		Writable: !f.locked[p],
	}, nil
}
func (f *fakeLocker) Lock(string, platform.Level) error              { return nil }
func (f *fakeLocker) Unlock(string) error                            { return nil }
func (f *fakeLocker) Guard(string, platform.Level) error             { return nil }
func (f *fakeLocker) Unguard(string) error                           { return nil }
func (f *fakeLocker) Supports(string, platform.Level) (bool, string) { return true, "" }

// world builds the layout the tests reason about:
//
//	/w                 guarded (append-only)
//	/w/docs            guarded
//	/w/docs/SSOT.md    locked
//	/w/docs/other.md   present, not locked
//	/w/README.md       present, not locked
func world() *fakeLocker {
	f := newFake()
	for _, d := range []string{"/w", "/w/docs"} {
		f.exists[d], f.dirs[d], f.guard[d] = true, true, true
	}
	f.exists["/w/docs/SSOT.md"], f.locked["/w/docs/SSOT.md"] = true, true
	f.exists["/w/docs/other.md"] = true
	f.exists["/w/README.md"] = true
	return f
}

func decide(t *testing.T, l platform.Locker, payload string) Decision {
	t.Helper()
	return Evaluate(Parse([]byte(payload)), l, Options{})
}

func bash(cmd string) string {
	b, _ := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "tool_name": "Bash", "cwd": "/w",
		"tool_input": map[string]any{"command": cmd},
	})
	return string(b)
}

func TestBashDecisions(t *testing.T) {
	l := world()
	cases := []struct {
		cmd  string
		deny bool
	}{
		// the bypass this whole feature exists for
		{"mv docs docs.locked && mkdir docs", true},
		{"mv /w/docs /w/elsewhere", true},
		{"rm -rf docs", true},
		{"rm docs/SSOT.md", true},
		{"mv docs/SSOT.md docs/SSOT.old.md", true},
		// direct writes
		{"echo x > docs/SSOT.md", true},
		{"echo x >docs/SSOT.md", true},
		{"cat /etc/hosts >> docs/SSOT.md", true},
		{"cp /etc/hosts docs/SSOT.md", true},
		{"sed -i '' s/a/b/ docs/SSOT.md", true},
		{"tee docs/SSOT.md < /dev/null", true},
		{"git checkout -- docs/SSOT.md", true},
		{"truncate -s 0 docs/SSOT.md", true},
		// lifting the lock is refused with its own wording
		{"sudo chflags noschg docs/SSOT.md", true},
		{"sudo chattr -i docs/SSOT.md", true},
		{"sudo afl unlock docs/SSOT.md", true},
		// deleting an unrelated file inside a guarded directory: the kernel
		// refuses it too, so saying why beats a bare EPERM
		{"rm docs/other.md", true},
		// allowed
		{"cat docs/SSOT.md", false},
		{"grep -n foo docs/SSOT.md", false},
		{"diff docs/SSOT.md /etc/hosts", false},
		{"echo hi > docs/new.md", false},
		{"echo hi > README.md", false},
		{"touch docs/brand-new.md", false},
		{"rm docs/nothing-here.md", false},
		{"ls -la docs", false},
		{"afl status docs/SSOT.md", false},
		{"git status", false},
	}
	for _, c := range cases {
		d := decide(t, l, bash(c.cmd))
		if d.Deny != c.deny {
			t.Errorf("%q: deny=%v want %v (checked %v)", c.cmd, d.Deny, c.deny, d.Checked)
		}
	}
}

func TestToolDecisions(t *testing.T) {
	l := world()
	cases := []struct {
		name    string
		payload map[string]any
		deny    bool
	}{
		{"edit locked", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": "docs/SSOT.md"}}, true},
		{"write locked", map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": "/w/docs/SSOT.md"}}, true},
		{"write free", map[string]any{"tool_name": "Write", "tool_input": map[string]any{"file_path": "docs/other.md"}}, false},
		{"read locked", map[string]any{"tool_name": "Read", "tool_input": map[string]any{"file_path": "docs/SSOT.md"}}, false},
		{"multiedit", map[string]any{"tool_name": "MultiEdit", "tool_input": map[string]any{
			"edits": []any{map[string]any{"file_path": "docs/SSOT.md"}}}}, true},
		{"apply_patch", map[string]any{"tool_name": "apply_patch", "tool_input": map[string]any{
			"command": "*** Begin Patch\n*** Update File: docs/SSOT.md\n*** End Patch"}}, true},
		{"apply_patch add elsewhere", map[string]any{"tool_name": "apply_patch", "tool_input": map[string]any{
			"command": "*** Begin Patch\n*** Add File: docs/fresh.md\n*** End Patch"}}, false},
		{"unified diff", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{
			"patch": "--- a/docs/SSOT.md\n+++ b/docs/SSOT.md\n"}}, true},
		{"cursor-style key", map[string]any{"tool_name": "edit_file", "tool_input": map[string]any{
			"target_file": "docs/SSOT.md"}}, true},
		{"post tool use is ignored", map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Edit",
			"tool_input": map[string]any{"file_path": "docs/SSOT.md"}}, false},
	}
	for _, c := range cases {
		if _, ok := c.payload["hook_event_name"]; !ok {
			c.payload["hook_event_name"] = "PreToolUse"
		}
		c.payload["cwd"] = "/w"
		b, _ := json.Marshal(c.payload)
		if d := decide(t, l, string(b)); d.Deny != c.deny {
			t.Errorf("%s: deny=%v want %v (checked %v)", c.name, d.Deny, c.deny, d.Checked)
		}
	}
}

// A payload afl cannot make sense of must not block the agent: the kernel is
// still there, and a hook that fails closed on every malformed input would
// make the harness unusable.
func TestUnparsableInputAllows(t *testing.T) {
	l := world()
	for _, body := range []string{"", "not json", "[]", "{}", `{"tool_name":"Bash"}`} {
		if d := decide(t, l, body); d.Deny {
			t.Errorf("%q should not deny: %+v", body, d.Reasons)
		}
	}
}

// Paths given as arguments are the fallback for harnesses that cannot pipe
// JSON, and the basis of `afl hook check`.
func TestPathArguments(t *testing.T) {
	l := world()
	d := Evaluate(&Request{Cwd: "/w"}, l, Options{Paths: []string{"docs/SSOT.md"}})
	if !d.Deny {
		t.Fatal("locked path argument should deny")
	}
	if !strings.Contains(d.Message, notice.Headline) {
		t.Errorf("message missing headline: %s", d.Message)
	}
	if d := Evaluate(&Request{Cwd: "/w"}, l, Options{Paths: []string{"docs/other.md"}}); d.Deny {
		t.Error("free path argument should allow")
	}
}

func TestStrictCatchesUnknownCommands(t *testing.T) {
	l := world()
	body := bash("some-unknown-tool --write docs/SSOT.md")
	if d := decide(t, l, body); d.Deny {
		t.Error("default mode leaves unknown commands to the kernel")
	}
	if d := Evaluate(Parse([]byte(body)), l, Options{Strict: true}); !d.Deny {
		t.Error("strict mode should deny an unknown command naming a locked path")
	}
}

func TestWriteFormats(t *testing.T) {
	l := world()
	d := decide(t, l, bash("rm docs/SSOT.md"))
	if !d.Deny {
		t.Fatal("expected deny")
	}
	var out, errb bytes.Buffer
	if code := Write(d, FormatAuto, &out, &errb); code != ExitDeny {
		t.Errorf("auto exit = %d, want %d", code, ExitDeny)
	}
	var payload map[string]any
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("auto stdout is not JSON: %v (%s)", err, out.String())
	}
	hso, _ := payload["hookSpecificOutput"].(map[string]any)
	if hso["permissionDecision"] != "deny" || payload["decision"] != "block" {
		t.Errorf("deny payload: %v", payload)
	}
	if !strings.Contains(errb.String(), notice.Headline) {
		t.Errorf("auto stderr: %s", errb.String())
	}

	out.Reset()
	errb.Reset()
	if code := Write(d, FormatJSON, &out, &errb); code != ExitAllow || out.Len() == 0 || errb.Len() != 0 {
		t.Errorf("json: code=%d stdout=%d stderr=%q", code, out.Len(), errb.String())
	}
	out.Reset()
	errb.Reset()
	if code := Write(d, FormatExitCode, &out, &errb); code != ExitDeny || out.Len() != 0 || errb.Len() == 0 {
		t.Errorf("exit-code: code=%d stdout=%q stderr=%d", code, out.String(), errb.Len())
	}
	out.Reset()
	errb.Reset()
	if code := Write(Decision{}, FormatAuto, &out, &errb); code != ExitAllow || out.Len() != 0 || errb.Len() != 0 {
		t.Errorf("allow must be silent: code=%d %q %q", code, out.String(), errb.String())
	}
}

func TestParseFormatAliases(t *testing.T) {
	for _, s := range []string{"", "auto"} {
		if f, err := ParseFormat(s); err != nil || f != FormatAuto {
			t.Errorf("%q -> %v %v", s, f, err)
		}
	}
	for _, s := range []string{"json", "claude-code", "codex"} {
		if f, err := ParseFormat(s); err != nil || f != FormatJSON {
			t.Errorf("%q -> %v %v", s, f, err)
		}
	}
	for _, s := range []string{"exit-code", "generic", "text"} {
		if f, err := ParseFormat(s); err != nil || f != FormatExitCode {
			t.Errorf("%q -> %v %v", s, f, err)
		}
	}
	if _, err := ParseFormat("nope"); err == nil {
		t.Error("unknown format should fail")
	}
}

func TestTokenizer(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`rm "a b.md"`, []string{"rm", "a b.md"}},
		{`rm 'a b.md'`, []string{"rm", "a b.md"}},
		{`rm a\ b.md`, []string{"rm", "a b.md"}},
		{`a && b`, []string{"a", "&&", "b"}},
		{`a | b`, []string{"a", "|", "b"}},
	}
	for _, c := range cases {
		got := []string{}
		for _, tk := range tokenize(c.in) {
			got = append(got, tk.text)
		}
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("tokenize(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestInstallIsIdempotentAndReversible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// pre-existing settings must survive
	if err := os.WriteFile(path, []byte(`{"model":"opus","hooks":{"PostToolUse":[{"matcher":"Bash"}]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h, _ := FindHarness("claude-code")
	changed, err := Install(path, h)
	if err != nil || !changed {
		t.Fatalf("install: %v %v", changed, err)
	}
	b, _ := os.ReadFile(path)
	var cfg map[string]any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["model"] != "opus" {
		t.Errorf("unrelated settings lost: %s", b)
	}
	hooks := cfg["hooks"].(map[string]any)
	if _, ok := hooks["PostToolUse"]; !ok {
		t.Errorf("other hooks lost: %s", b)
	}
	if changed, _ := Install(path, h); changed {
		t.Error("second install should be a no-op")
	}
	changed, err = Uninstall(path)
	if err != nil || !changed {
		t.Fatalf("uninstall: %v %v", changed, err)
	}
	b, _ = os.ReadFile(path)
	if strings.Contains(string(b), "afl hook") {
		t.Errorf("hook left behind: %s", b)
	}
	if err := json.Unmarshal(b, &cfg); err != nil || cfg["model"] != "opus" {
		t.Errorf("uninstall damaged the file: %s", b)
	}
	if changed, _ := Uninstall(path); changed {
		t.Error("second uninstall should be a no-op")
	}
}

func TestInstallRefusesBrokenJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	os.WriteFile(path, []byte("{not json"), 0o644)
	h, _ := FindHarness("claude-code")
	if _, err := Install(path, h); err == nil {
		t.Error("expected an error rather than an overwrite")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "{not json" {
		t.Errorf("file was rewritten: %s", b)
	}
}
