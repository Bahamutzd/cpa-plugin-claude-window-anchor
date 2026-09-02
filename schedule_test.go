package main

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load location %s: %v", name, err)
	}
	return loc
}

func TestEvaluateSlot_NoKnownResetAnchorsAtSlot(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	now := time.Date(2026, 9, 2, 16, 0, 0, 0, loc)
	anchor := time.Date(2026, 9, 2, 16, 30, 0, 0, loc)

	got := evaluateSlot(now, anchor, time.Time{}, 90*time.Second, time.Hour)
	if got.Decision != decisionAnchorNow {
		t.Fatalf("decision = %v, want anchor-now", formatSlotOutput(got.Decision))
	}
	if got.WindowKey != "2026-09-02T16:30+08:00" {
		t.Fatalf("window key = %q", got.WindowKey)
	}
	if !got.AnchorTime.Equal(anchor) {
		t.Fatalf("anchor time = %v, want %v", got.AnchorTime, anchor)
	}
}

func TestEvaluateSlot_WindowEndedBeforeSlotAnchorsAtSlot(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	now := time.Date(2026, 9, 2, 16, 20, 0, 0, loc)
	anchor := time.Date(2026, 9, 2, 16, 30, 0, 0, loc)
	knownReset := time.Date(2026, 9, 2, 16, 0, 0, 0, loc) // ended before slot

	got := evaluateSlot(now, anchor, knownReset, 90*time.Second, time.Hour)
	if got.Decision != decisionAnchorNow {
		t.Fatalf("decision = %v, want anchor-now", formatSlotOutput(got.Decision))
	}
	if !got.AnchorTime.Equal(anchor) {
		t.Fatalf("anchor time = %v, want %v", got.AnchorTime, anchor)
	}
}

func TestEvaluateSlot_WindowOpen_DefersToResetPlusGrace(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	now := time.Date(2026, 9, 2, 16, 30, 0, 0, loc)
	anchor := time.Date(2026, 9, 2, 16, 30, 0, 0, loc)
	knownReset := time.Date(2026, 9, 2, 16, 45, 0, 0, loc) // still open

	got := evaluateSlot(now, anchor, knownReset, 90*time.Second, time.Hour)
	if got.Decision != decisionAnchorNow {
		t.Fatalf("decision = %v, want anchor-now", formatSlotOutput(got.Decision))
	}
	want := knownReset.Add(90 * time.Second)
	if !got.AnchorTime.Equal(want) {
		t.Fatalf("anchor time = %v, want %v", got.AnchorTime, want)
	}
}

func TestEvaluateSlot_WindowExtendsPastMaxDeferral_Skips(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	now := time.Date(2026, 9, 2, 16, 0, 0, 0, loc)
	anchor := time.Date(2026, 9, 2, 16, 30, 0, 0, loc)
	knownReset := time.Date(2026, 9, 2, 21, 0, 0, 0, loc) // far past

	got := evaluateSlot(now, anchor, knownReset, 90*time.Second, time.Hour)
	if got.Decision != decisionSkipped {
		t.Fatalf("decision = %v, want skipped", formatSlotOutput(got.Decision))
	}
}

func TestEvaluateSlot_WithinMaxDeferral(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	now := time.Date(2026, 9, 2, 16, 0, 0, 0, loc)
	anchor := time.Date(2026, 9, 2, 16, 30, 0, 0, loc)
	knownReset := time.Date(2026, 9, 2, 17, 0, 0, 0, loc) // 30m past, within 60m

	got := evaluateSlot(now, anchor, knownReset, 90*time.Second, time.Hour)
	if got.Decision != decisionAnchorNow {
		t.Fatalf("decision = %v, want anchor-now", formatSlotOutput(got.Decision))
	}
	if !got.AnchorTime.Equal(knownReset.Add(90 * time.Second)) {
		t.Fatalf("anchor time = %v, want %v", got.AnchorTime, knownReset.Add(90*time.Second))
	}
}

func TestNextAnchorOnOrAfter_NoAnchorEarlierNow(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	anchors := []anchorTime{{Hour: 6, Minute: 30}, {Hour: 11, Minute: 30}, {Hour: 16, Minute: 30}}
	now := time.Date(2026, 9, 2, 9, 0, 0, 0, loc)

	next, entry := nextAnchorOnOrAfter(now, anchors, loc)
	if next.Hour() != 11 || next.Minute() != 30 {
		t.Fatalf("next = %v, want 11:30", next)
	}
	if entry.Hour != 11 || entry.Minute != 30 {
		t.Fatalf("entry = %v, want 11:30", entry)
	}
}

func TestNextAnchorOnOrAfter_RollsToNextDay(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	anchors := []anchorTime{{Hour: 16, Minute: 30}}
	now := time.Date(2026, 9, 2, 18, 0, 0, 0, loc)

	next, _ := nextAnchorOnOrAfter(now, anchors, loc)
	if next.Day() != 3 || next.Hour() != 16 {
		t.Fatalf("next = %v, want 16:30 next day", next)
	}
}

func TestPreviousAnchorOnOrBefore(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	anchors := []anchorTime{{Hour: 6, Minute: 30}, {Hour: 16, Minute: 30}}
	now := time.Date(2026, 9, 2, 17, 0, 0, 0, loc)

	prev, _, ok := previousAnchorOnOrBefore(now, anchors, loc)
	if !ok {
		t.Fatal("expected a previous anchor")
	}
	if prev.Hour() != 16 || prev.Minute() != 30 {
		t.Fatalf("prev = %v, want 16:30", prev)
	}
}

func TestPreviousAnchorOnOrBefore_EarlyMorning(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	anchors := []anchorTime{{Hour: 6, Minute: 30}, {Hour: 16, Minute: 30}}
	now := time.Date(2026, 9, 2, 1, 0, 0, 0, loc)

	prev, _, ok := previousAnchorOnOrBefore(now, anchors, loc)
	if !ok {
		t.Fatal("expected a previous anchor")
	}
	if prev.Hour() != 16 || prev.Day() != 1 {
		t.Fatalf("prev = %v, want 16:30 previous day", prev)
	}
}

func TestCatchUpDue(t *testing.T) {
	loc := mustLoc(t, "Asia/Shanghai")
	missed := time.Date(2026, 9, 2, 16, 30, 0, 0, loc)
	now := missed.Add(10 * time.Minute)
	if !catchUpDue(now, missed, 45*time.Minute, time.Time{}) {
		t.Fatal("should catch up within window with no known reset")
	}
	// Window still open: no catch-up.
	if catchUpDue(now, missed, 45*time.Minute, missed.Add(30*time.Minute)) {
		t.Fatal("should not catch up while old window still open")
	}
	// Too late.
	if catchUpDue(missed.Add(3*time.Hour), missed, 45*time.Minute, time.Time{}) {
		t.Fatal("should not catch up after 3 hours")
	}
}

func TestParseAnchors_SortsAndRejectsDuplicates(t *testing.T) {
	parsed, err := parseAnchors([]string{"16:30", "06:30", "11:30"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(parsed) != 3 || parsed[0].Hour != 6 || parsed[1].Hour != 11 || parsed[2].Hour != 16 {
		t.Fatalf("parsed = %v, want sorted", parsed)
	}
	if _, err := parseAnchors([]string{"06:30", "06:30"}); err == nil {
		t.Fatal("expected duplicate error")
	}
	if _, err := parseAnchors([]string{"25:00"}); err == nil {
		t.Fatal("expected out-of-range error")
	}
	if _, err := parseAnchors([]string{"ab:cd"}); err == nil {
		t.Fatal("expected invalid anchor error")
	}
}

func TestAnchorTimeWallClock_DST(t *testing.T) {
	// Asia/Shanghai has no DST, so wall-clock construction is exact — this is
	// the deployment target and must never shift.
	sh := mustLoc(t, "Asia/Shanghai")
	exact := anchorTime{Hour: 16, Minute: 30}.wallClock(2026, 9, 2, sh)
	if exact.Hour() != 16 || exact.Minute() != 30 {
		t.Fatalf("shanghai wall clock = %v, want 16:30", exact)
	}

	// America/New_York has real DST. A spring-forward gap time (which does
	// not exist) is normalized by time.Date to an adjacent instant; the
	// exact value is implementation-defined. Assert only that a valid,
	// non-zero time is produced and that the date does not shift.
	loc := mustLoc(t, "America/New_York")
	gap := anchorTime{Hour: 2, Minute: 30}.wallClock(2026, 3, 8, loc)
	if gap.IsZero() {
		t.Fatal("spring-forward gap must produce a non-zero time")
	}
	if gap.Year() != 2026 || gap.Month() != time.March || gap.Day() != 8 {
		t.Fatalf("gap must stay on 2026-03-08, got %v", gap)
	}
}
