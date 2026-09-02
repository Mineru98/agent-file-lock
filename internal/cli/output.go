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
	for _, r := range results {
		switch r.Outcome {
		case lock.OutcomeFailed:
			fmt.Fprintf(e.stderr, "%-11s%s: %s\n", "[FAIL]", r.Path, r.Error)
		case lock.OutcomeChanged:
			if !c.quiet {
				fmt.Fprintf(e.stdout, "%-11s%s%s\n", "["+pastTense(action)+"]", r.Path, levelSuffix(r))
			}
		case lock.OutcomeSkipped:
			if !c.quiet {
				fmt.Fprintf(e.stdout, "%-11s%s (already %s)\n", "[skip]", r.Path, skipReason(action, r))
			}
		case lock.OutcomePlanned:
			if !c.quiet {
				fmt.Fprintf(e.stdout, "%-11s would %s %s%s\n", "[plan]", action, r.Path, levelSuffix(r))
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
	}
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
