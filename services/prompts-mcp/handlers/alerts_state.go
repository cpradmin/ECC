package handlers

import "time"

// alertState tracks a single metric+severity pair across evaluations.
type alertState struct {
	FirstBreach time.Time // when the current continuous breach started
	LastFired   time.Time // when we last delivered an alert
	Fired       bool      // true once the pending window elapsed and we fired
	Suppressed  int       // duplicates suppressed since LastFired
	LastSeen    time.Time // for stale-entry pruning
}

// staleStateTTL prunes states for metrics that stopped being evaluated (metric
// renamed, threshold removed) so the map cannot grow unbounded.
const staleStateTTL = 2 * time.Hour

// stateKey generates a map key for a metric+severity pair.
func stateKey(metric string, severity AlertSeverity) string {
	return metric + "|" + string(severity)
}
