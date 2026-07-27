package handlers

import "time"

// breached reports whether value is on the unhealthy side of the threshold.
func breached(th *AlertThreshold, value float32) bool {
	if th.Comparison == ComparisonAbove {
		return value > th.Threshold
	}
	return value < th.Threshold
}

// cooldownFor returns the effective cooldown for a threshold.
// If Cooldown is 0, defaults to DefaultAlertCooldown.
func cooldownFor(th *AlertThreshold) time.Duration {
	if th.Cooldown > 0 {
		return th.Cooldown
	}
	return DefaultAlertCooldown
}
