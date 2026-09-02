package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// Format selects the response dialect.
type Format string

const (
	// FormatAuto emits everything at once: the deny JSON that Claude Code and
	// Codex parse, the reason on stderr, and exit code 2. Every harness
	// recognises at least one of the three, and a harness that recognises
	// none still sees a non-zero exit — which fails closed, the only safe
	// direction for a tool whose job is refusing writes.
	FormatAuto Format = "auto"
	// FormatJSON emits the deny JSON and exits 0, for harnesses that treat a
	// non-zero exit as a broken hook rather than a refusal.
	FormatJSON Format = "json"
	// FormatExitCode emits only the reason on stderr and exits 2.
	FormatExitCode Format = "exit-code"
)

// ParseFormat validates a --format value. The harness names are accepted as
// aliases so `--format claude-code` reads naturally in a settings file.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto":
		return FormatAuto, nil
	case "json", "claude-code", "claude", "codex":
		return FormatJSON, nil
	case "exit-code", "exit", "text", "generic":
		return FormatExitCode, nil
	}
	return "", fmt.Errorf("unknown hook format %q (auto, json, exit-code)", s)
}

// Exit codes used by the hook. 2 is the value Claude Code and Codex read as
// "blocked, feed stderr back to the model".
const (
	ExitAllow = 0
	ExitDeny  = 2
)

// Write renders a decision and returns the process exit code.
func Write(d Decision, f Format, stdout, stderr io.Writer) int {
	if !d.Deny {
		return ExitAllow
	}
	msg := d.Message
	switch f {
	case FormatExitCode:
		fmt.Fprintln(stderr, msg)
		return ExitDeny
	case FormatJSON:
		writeDenyJSON(stdout, msg)
		return ExitAllow
	default:
		writeDenyJSON(stdout, msg)
		fmt.Fprintln(stderr, msg)
		return ExitDeny
	}
}

// denyPayload is the union of the shapes hook protocols accept. Unknown keys
// are ignored by every harness, so emitting all of them costs nothing and
// covers the current schema, the legacy one, and the plain-text field some
// harnesses surface to the user.
type denyPayload struct {
	HookSpecificOutput hookSpecific `json:"hookSpecificOutput"`
	Decision           string       `json:"decision"`
	Reason             string       `json:"reason"`
	Continue           bool         `json:"continue"`
	StopReason         string       `json:"stopReason"`
	SystemMessage      string       `json:"systemMessage"`
}

type hookSpecific struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision"`
	PermissionDecisionReason string `json:"permissionDecisionReason"`
}

func writeDenyJSON(w io.Writer, msg string) {
	p := denyPayload{
		HookSpecificOutput: hookSpecific{
			HookEventName:            "PreToolUse",
			PermissionDecision:       "deny",
			PermissionDecisionReason: msg,
		},
		Decision:      "block",
		Reason:        msg,
		Continue:      true,
		StopReason:    msg,
		SystemMessage: msg,
	}
	enc := json.NewEncoder(w)
	_ = enc.Encode(p)
}
