package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Harness describes where one agent runtime keeps its hook configuration.
// Claude Code defined this file shape and Codex adopted it unchanged, so both
// entries differ only in location and in which tools fire PreToolUse.
type Harness struct {
	Name        string
	Aliases     []string // accepted on the command line, not advertised
	Project     string   // config path relative to the project root
	Global      string   // config path relative to $HOME
	Matcher     string   // regex over tool names
	Note        string
	TOMLSnippet string // for harnesses whose config is TOML
}

// Harnesses is the built-in table. Anything not listed here has to be wired
// up by hand against the exit-code contract documented in the README.
var Harnesses = []Harness{
	{
		Name:    "claude",
		Aliases: []string{"claude-code"},
		Project: ".claude/settings.json",
		Global:  ".claude/settings.json",
		Matcher: "Edit|Write|MultiEdit|NotebookEdit|Bash",
	},
	{
		Name:    "codex",
		Project: ".codex/hooks.json",
		Global:  ".codex/hooks.json",
		Matcher: "Bash|apply_patch",
		Note:    "Codex fires PreToolUse for the shell tool (and apply_patch); hooks are experimental and must be enabled in config.toml.",
		TOMLSnippet: `[[hooks.PreToolUse]]
matcher = "Bash|apply_patch"

[[hooks.PreToolUse.hooks]]
type = "command"
command = "afl hook"
timeout = 10
statusMessage = "Checking agent-file-lock"
`,
	},
}

// FindHarness looks up a harness by name or by one of its older aliases.
func FindHarness(name string) (Harness, bool) {
	for _, h := range Harnesses {
		if strings.EqualFold(h.Name, name) {
			return h, true
		}
		for _, alias := range h.Aliases {
			if strings.EqualFold(alias, name) {
				return h, true
			}
		}
	}
	return Harness{}, false
}

// HarnessNames lists the harnesses `hook install` can actually write to.
func HarnessNames() []string {
	out := make([]string, 0, len(Harnesses))
	for _, h := range Harnesses {
		out = append(out, h.Name)
	}
	sort.Strings(out)
	return out
}

// Command is the hook command line written into a config file.
const Command = "afl hook"

// Install adds the PreToolUse hook to path, creating the file and its parent
// directory if needed. Existing settings are preserved: the file is parsed,
// the hook appended if it is not already there, and written back. Returns
// whether anything changed.
func Install(path string, h Harness) (changed bool, err error) {
	cfg := map[string]any{}
	if b, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return false, fmt.Errorf("%s: %w (fix or move the file, afl will not overwrite it)", path, err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	hooks, _ := cfg["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	list, _ := hooks["PreToolUse"].([]any)
	for _, entry := range list {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		inner, _ := m["hooks"].([]any)
		for _, hv := range inner {
			hm, ok := hv.(map[string]any)
			if !ok {
				continue
			}
			if cmd, _ := hm["command"].(string); strings.Contains(cmd, "afl hook") {
				return false, nil // already installed
			}
		}
	}
	list = append(list, map[string]any{
		"matcher": h.Matcher,
		"hooks": []any{map[string]any{
			"type":    "command",
			"command": Command,
			"timeout": 10,
		}},
	})
	hooks["PreToolUse"] = list
	cfg["hooks"] = hooks
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(path, append(b, '\n'), 0o644)
}

// Uninstall removes every afl hook entry from path.
func Uninstall(path string) (changed bool, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return false, fmt.Errorf("%s: %w", path, err)
	}
	hooks, _ := cfg["hooks"].(map[string]any)
	if hooks == nil {
		return false, nil
	}
	list, _ := hooks["PreToolUse"].([]any)
	kept := make([]any, 0, len(list))
	for _, entry := range list {
		if isAflEntry(entry) {
			changed = true
			continue
		}
		kept = append(kept, entry)
	}
	if !changed {
		return false, nil
	}
	if len(kept) == 0 {
		delete(hooks, "PreToolUse")
	} else {
		hooks["PreToolUse"] = kept
	}
	if len(hooks) == 0 {
		delete(cfg, "hooks")
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(path, append(out, '\n'), 0o644)
}

func isAflEntry(entry any) bool {
	m, ok := entry.(map[string]any)
	if !ok {
		return false
	}
	inner, _ := m["hooks"].([]any)
	for _, hv := range inner {
		hm, ok := hv.(map[string]any)
		if !ok {
			continue
		}
		if cmd, _ := hm["command"].(string); strings.Contains(cmd, "afl hook") {
			return true
		}
	}
	return false
}

// Snippet renders the configuration a harness needs, for pasting by hand.
func Snippet(h Harness) string {
	return fmt.Sprintf(`{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": %q,
        "hooks": [
          { "type": "command", "command": %q, "timeout": 10 }
        ]
      }
    ]
  }
}
`, h.Matcher, Command)
}
