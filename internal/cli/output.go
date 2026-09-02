package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Mineru98/agent-file-lock/internal/lock"
)

type jsonReport struct {
	Action  string         `json:"action"`
	DryRun  bool           `json:"dry_run"`
	Results []lock.Result  `json:"results"`
	Skipped []lock.Skipped `json:"skipped,omitempty"`
	Summary lock.Summary   `json:"summary"`
}

func (e *env) printResults(c *common, action lock.Action, results []lock.Result, skipped []lock.Skipped, sum lock.Summary) {
	if c.json {
		writeJSON(e.stdout, jsonReport{Action: action.String(), DryRun: c.dryRun, Results: results, Skipped: skipped, Summary: sum})
		return
	}
	guards := 0
	for _, r := range results {
		isGuard := r.Action == "guard" || r.Action == "unguard"
		if isGuard && r.Outcome == lock.OutcomeChanged {
			guards++
		}
		switch r.Outcome {
		case lock.OutcomeFailed:
			fmt.Fprintf(e.stderr, "%-11s%s: %s\n", "[FAIL]", r.Path, r.Error)
		case lock.OutcomeChanged:
			if !c.quiet {
				fmt.Fprintf(e.stdout, "%-11s%s%s\n", "["+changedTag(r, action)+"]", r.Path, levelSuffix(r))
			}
		case lock.OutcomeSkipped:
			if !c.quiet {
				fmt.Fprintf(e.stdout, "%-11s%s (%s)\n", "[skip]", r.Path, skippedWhy(r, action))
			}
		case lock.OutcomePlanned:
			if !c.quiet {
				verb := action.String()
				if isGuard {
					verb = r.Action
				}
				fmt.Fprintf(e.stdout, "%-11s would %s %s%s\n", "[plan]", verb, r.Path, levelSuffix(r))
			}
		}
	}
	if !c.quiet {
		for _, s := range skipped {
			fmt.Fprintf(e.stdout, "%-11s%s (%s)\n", "[skip]", s.Path, s.Reason)
		}
		verb := "changed"
		if c.dryRun {
			verb = "planned"
		}
		fmt.Fprintf(e.stdout, "%s: %d %s, %d skipped, %d failed\n", action, sum.Changed, verb, sum.Skipped, sum.Failed)
		if guards > 0 {
			word := "guarded"
			if action == lock.ActionUnlock {
				word = "released"
			}
			fmt.Fprintf(e.stdout, "%s: %d parent director%s %s (append-only: new files still allowed, renames and deletes are not)\n",
				action, guards, map[bool]string{true: "y", false: "ies"}[guards == 1], word)
		}
	}
}

// changedTag labels a line: guard results carry their own action, so a
// directory frozen as a side effect of a lock is never reported as "locked".
func changedTag(r lock.Result, a lock.Action) string {
	switch r.Action {
	case "guard":
		return "guarded"
	case "unguard":
		return "released"
	}
	return pastTense(a)
}

// skippedWhy explains a no-op, including the case that keeps a guard in place
// because siblings of the unlocked file are still protected.
func skippedWhy(r lock.Result, a lock.Action) string {
	switch r.Action {
	case "guard":
		return "already append-only"
	case "unguard":
		if r.Error != "" {
			return "kept: " + r.Error
		}
		return "not guarded"
	}
	return "already " + skipReason(a, r)
}

func pastTense(a lock.Action) string {
	if a == lock.ActionUnlock {
		return "unlocked"
	}
	return "locked"
}

func levelSuffix(r lock.Result) string {
	if r.Level != "" {
		return " (" + r.Level + ")"
	}
	return ""
}

func skipReason(a lock.Action, r lock.Result) string {
	if a == lock.ActionUnlock {
		return "unlocked"
	}
	if lvl := r.Before.Level(); lvl != 0 {
		return "locked: " + lvl.String()
	}
	return "locked"
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
