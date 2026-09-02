package main

import (
	"strings"
	"time"
)

// windowKeyFormat is the stable per-slot identifier: the anchor wall-clock
// time rendered in the plugin timezone. It survives clock jitter, duplicate
// ticks and reconfigures.
const windowKeyFormat = "2006-01-02T15:04-07:00"

// scheduleDecision describes what the anchor loop should do for one account
// at one tick.
type scheduleDecision int

const (
	// decisionNone means nothing is due for this account right now.
	decisionNone scheduleDecision = iota
	// decisionAnchorNow means the slot is due and should be anchored now.
	decisionAnchorNow
	// decisionSkipped means the slot is due but anchoring would waste quota
	// (window extends far past the anchor); wait for the next slot.
	decisionSkipped
)

// slotResult is the pure outcome of evaluating an anchor slot.
type slotResult struct {
	Decision   scheduleDecision
	WindowKey  string // stable slot key, non-empty when Decision != decisionNone
	AnchorTime time.Time
	Reason     string
}

// anchorTime returns the wall-clock time of this anchor on the given date in
// the given location. If the rendered time does not exist (spring-forward
// gap), time.Date normalizes forward — acceptable. If it is ambiguous
// (fall-back overlap), Go picks the first occurrence — acceptable.
func (a anchorTime) wallClock(year int, month time.Month, day int, loc *time.Location) time.Time {
	return time.Date(year, month, day, a.Hour, a.Minute, 0, 0, loc)
}

// nextAnchorOnOrAfter returns the next anchor wall-clock time that is >= now.
// It checks today first, then rolls forward day by day. Pure function of
// (now, anchors, loc).
func nextAnchorOnOrAfter(now time.Time, anchors []anchorTime, loc *time.Location) (time.Time, anchorTime) {
	if len(anchors) == 0 {
		return time.Time{}, anchorTime{}
	}
	year, month, day := now.In(loc).Date()
	for offset := 0; offset < 2; offset++ {
		date := time.Date(year, month, day+offset, 0, 0, 0, 0, loc)
		for _, entry := range anchors {
			candidate := entry.wallClock(date.Year(), date.Month(), date.Day(), loc)
			if !candidate.Before(now) {
				return candidate, entry
			}
		}
	}
	// Unreachable: two days span all anchors.
	return time.Time{}, anchorTime{}
}

// previousAnchorOnOrBefore returns the most recent anchor time at or before
// now. It scans today first, then the previous day, so an anchor earlier
// today is never shadowed by yesterday's later one. Used for catch-up after
// restarts and suspend/resume.
func previousAnchorOnOrBefore(now time.Time, anchors []anchorTime, loc *time.Location) (time.Time, anchorTime, bool) {
	if len(anchors) == 0 {
		return time.Time{}, anchorTime{}, false
	}
	year, month, day := now.In(loc).Date()
	for offset := 0; offset >= -1; offset-- {
		date := time.Date(year, month, day+offset, 0, 0, 0, 0, loc)
		for i := len(anchors) - 1; i >= 0; i-- {
			entry := anchors[i]
			candidate := entry.wallClock(date.Year(), date.Month(), date.Day(), loc)
			if !candidate.After(now) {
				return candidate, entry, true
			}
		}
	}
	return time.Time{}, anchorTime{}, false
}

// evaluateSlot is the core decision rule for one account. It is a pure
// function of (now, knownResetsAt, grace, maxDeferral, anchor time).
//
// Decision matrix:
//
//	known == zero   -> anchor now (no info, best effort)
//	known <= anchor -> anchor now (anchor falls at or after window end)
//	anchor < known  -> defer to known+grace
//	                   if known+grace > anchor+maxDeferral -> skip entirely
//
// AnchorTime reports when the anchor request should actually fire; the loop
// uses it to schedule sleep/wake.
func evaluateSlot(now, anchor time.Time, knownResetsAt time.Time, grace, maxDeferral time.Duration) slotResult {
	windowKey := anchor.Format(windowKeyFormat)
	if knownResetsAt.IsZero() {
		return slotResult{
			Decision:   decisionAnchorNow,
			WindowKey:  windowKey,
			AnchorTime: anchor,
			Reason:     "no reset signal; anchoring at slot",
		}
	}
	if !knownResetsAt.After(anchor) {
		// Window already ended before (or exactly at) the anchor slot.
		fireAt := anchor
		if knownResetsAt.Add(grace).After(fireAt) {
			fireAt = knownResetsAt.Add(grace)
		}
		return slotResult{
			Decision:   decisionAnchorNow,
			WindowKey:  windowKey,
			AnchorTime: fireAt,
			Reason:     "window ended before slot; anchoring at slot",
		}
	}
	// Window still open at the anchor slot. The real boundary is knownResetsAt.
	fireAt := knownResetsAt.Add(grace)
	limit := anchor.Add(maxDeferral)
	if fireAt.After(limit) {
		return slotResult{
			Decision:   decisionSkipped,
			WindowKey:  windowKey,
			AnchorTime: anchor,
			Reason:     "window extends past max deferral; skipping this slot",
		}
	}
	return slotResult{
		Decision:   decisionAnchorNow,
		WindowKey:  windowKey,
		AnchorTime: fireAt,
		Reason:     "window open at slot; anchoring at reset+grace",
	}
}

// catchUpDue reports whether a missed anchor (within catchUpWindow) should be
// fired now. A slot missed by more than catchUpWindow is discarded.
func catchUpDue(now, missedAnchor time.Time, catchUpWindow time.Duration, knownResetsAt time.Time) bool {
	if missedAnchor.IsZero() {
		return false
	}
	if now.Sub(missedAnchor) > catchUpWindow {
		return false
	}
	// Only bother if the old window has actually ended by now.
	if !knownResetsAt.IsZero() && knownResetsAt.After(now) {
		return false
	}
	return true
}

// windowKeyFor returns the stable slot key for any anchor wall-clock time.
func windowKeyFor(anchor time.Time) string {
	if anchor.IsZero() {
		return ""
	}
	return anchor.Format(windowKeyFormat)
}

// formatSlotOutput renders a decision for logging; pure value formatting.
func formatSlotOutput(decision scheduleDecision) string {
	switch decision {
	case decisionAnchorNow:
		return "anchor-now"
	case decisionSkipped:
		return "skipped"
	default:
		return "none"
	}
}

// normalizeAnchorInput trims whitespace from anchor strings defensively.
func normalizeAnchorInput(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
