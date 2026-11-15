package auth

import (
	"strconv"
	"strings"
	"time"
)

// Quota reset signal names, lowercased. Long windows come first: a weekly
// window that resets tomorrow should be burned before one that resets in
// four days, so fill-first prefers the credential whose long window ends
// soonest. Short windows are a fallback for credentials with no long window.
var (
	longWindowResetSignals = []string{
		"anthropic-ratelimit-unified-7d-reset",
		"x-codex-secondary-reset-at",
	}
	shortWindowResetSignals = []string{
		"anthropic-ratelimit-unified-5h-reset",
		"anthropic-ratelimit-unified-reset",
		"x-codex-primary-reset-at",
	}
	// Relative forms, measured from Quota.ObservedAt.
	longWindowResetAfterSignals  = []string{"x-codex-secondary-reset-after-seconds"}
	shortWindowResetAfterSignals = []string{"x-codex-primary-reset-after-seconds"}
)

// authQuotaResetAt returns when the credential's quota window resets, taken
// from the last observed upstream quota headers. It prefers the long window.
// It returns the zero time when no signal is known, or when every known reset
// is already in the past. A past reset means the window rolled over and the
// next reset is far away, so such credentials sort last.
func authQuotaResetAt(auth *Auth, now time.Time) time.Time {
	if auth == nil || len(auth.Quota.Signals) == 0 {
		return time.Time{}
	}
	signals := make(map[string]string, len(auth.Quota.Signals))
	for key, value := range auth.Quota.Signals {
		signals[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	if at := earliestFutureReset(signals, longWindowResetSignals, longWindowResetAfterSignals, auth.Quota.ObservedAt, now); !at.IsZero() {
		return at
	}
	return earliestFutureReset(signals, shortWindowResetSignals, shortWindowResetAfterSignals, auth.Quota.ObservedAt, now)
}

func earliestFutureReset(signals map[string]string, absolute, relative []string, observedAt, now time.Time) time.Time {
	var earliest time.Time
	consider := func(at time.Time) {
		if at.After(now) && (earliest.IsZero() || at.Before(earliest)) {
			earliest = at
		}
	}
	for _, name := range absolute {
		if seconds, ok := parseSignalSeconds(signals[name]); ok && seconds > 0 {
			consider(time.Unix(seconds, 0))
		}
	}
	if earliest.IsZero() && !observedAt.IsZero() {
		for _, name := range relative {
			if seconds, ok := parseSignalSeconds(signals[name]); ok && seconds > 0 {
				consider(observedAt.Add(time.Duration(seconds) * time.Second))
			}
		}
	}
	return earliest
}

func parseSignalSeconds(raw string) (int64, bool) {
	if raw == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return seconds, true
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil {
		return int64(seconds), true
	}
	return 0, false
}

// resetsSooner reports whether a should be burned before b. Known resets come
// before unknown ones, and earlier resets come first. Equal keys return false,
// so callers keep their existing (ID) order as the tiebreak.
func resetsSooner(a, b *Auth, now time.Time) bool {
	ra, rb := authQuotaResetAt(a, now), authQuotaResetAt(b, now)
	switch {
	case ra.IsZero():
		return false
	case rb.IsZero():
		return true
	default:
		return ra.Before(rb)
	}
}

// pickSoonestReset returns the matching ready entry whose quota resets first.
// With no reset signals it returns the same entry as pickFirst.
func (v *readyView) pickSoonestReset(predicate func(*scheduledAuth) bool, now time.Time) *scheduledAuth {
	var best *scheduledAuth
	for _, entry := range v.flat {
		if predicate != nil && !predicate(entry) {
			continue
		}
		if best == nil || resetsSooner(entry.auth, best.auth, now) {
			best = entry
		}
	}
	return best
}

// soonestResetAuth returns the candidate whose quota resets first. With no
// reset signals it returns candidates[0], matching the old fill-first order.
func soonestResetAuth(candidates []*Auth, now time.Time) *Auth {
	var best *Auth
	for _, candidate := range candidates {
		if best == nil || resetsSooner(candidate, best, now) {
			best = candidate
		}
	}
	return best
}
