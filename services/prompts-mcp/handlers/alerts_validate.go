package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
)

// ValidateAlertMatrix verifies every AlertMatrix entry is deliverable BEFORE the
// service starts serving traffic. A broken runbook link, or a threshold on a
// metric nobody emits, is a silent and permanent hole in the on-call path — so
// this is a hard startup failure, not a warning.
//
// repoRoot is the directory that Action.Runbook paths ("/docs/runbooks/x.md")
// are resolved against.
func ValidateAlertMatrix(repoRoot string) error {
	var problems []string

	seen := map[string]bool{}
	for i, th := range AlertMatrix {
		label := fmt.Sprintf("AlertMatrix[%d] (%s/%s)", i, th.MetricName, th.Severity)

		key := th.MetricName + "|" + string(th.Severity)
		if seen[key] {
			problems = append(problems, fmt.Sprintf("%s: duplicate metric+severity pair — GetAlertThreshold would shadow it", label))
		}
		seen[key] = true

		if _, ok := EmittedMetrics[th.MetricName]; !ok {
			problems = append(problems, fmt.Sprintf("%s: metric is not emitted by /mcp/metrics (see EmittedMetrics)", label))
		}

		switch th.Comparison {
		case ComparisonBelow, ComparisonAbove:
		default:
			problems = append(problems, fmt.Sprintf("%s: Comparison must be %q or %q, got %q",
				label, ComparisonBelow, ComparisonAbove, th.Comparison))
		}

		if th.Action.Message == "" {
			problems = append(problems, fmt.Sprintf("%s: Action.Message is empty", label))
		} else if _, err := template.New("msg").Parse(th.Action.Message); err != nil {
			problems = append(problems, fmt.Sprintf("%s: Action.Message template does not parse: %v", label, err))
		}

		if th.Action.Channel == "" {
			problems = append(problems, fmt.Sprintf("%s: Action.Channel is empty", label))
		}

		switch th.Action.Notification {
		case "slack", "pagerduty", "email":
		default:
			problems = append(problems, fmt.Sprintf("%s: unknown Action.Notification %q", label, th.Action.Notification))
		}

		if th.Action.Runbook == "" {
			problems = append(problems, fmt.Sprintf("%s: Action.Runbook is empty", label))
			continue
		}
		path := resolveRunbookPath(repoRoot, th.Action.Runbook)
		info, err := os.Stat(path)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("%s: runbook %q not found at %s: %v", label, th.Action.Runbook, path, err))
		case info.IsDir():
			problems = append(problems, fmt.Sprintf("%s: runbook %q resolves to a directory (%s)", label, th.Action.Runbook, path))
		case info.Size() == 0:
			problems = append(problems, fmt.Sprintf("%s: runbook %q is empty (%s)", label, th.Action.Runbook, path))
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("alert matrix validation failed (%d problem(s)):\n  - %s",
			len(problems), strings.Join(problems, "\n  - "))
	}
	return nil
}

// resolveRunbookPath maps a repo-absolute runbook reference onto the filesystem.
func resolveRunbookPath(repoRoot, runbook string) string {
	if repoRoot == "" {
		return filepath.FromSlash(runbook)
	}
	return filepath.Join(repoRoot, filepath.FromSlash(strings.TrimPrefix(runbook, "/")))
}
