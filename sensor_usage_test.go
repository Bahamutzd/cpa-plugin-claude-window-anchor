package main

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

func TestHandleUsage_ParsesFiveHourReset(t *testing.T) {
	resetEpoch := time.Date(2026, 9, 2, 21, 30, 0, 0, time.UTC).Unix()
	record := usageRecord{
		Provider: "claude",
		AuthID:   "claude-a@example.com",
		ResponseHeaders: map[string][]string{
			// Exact match on the 5h reset.
			"Anthropic-Ratelimit-Unified-Reset":  {intToStr(resetEpoch)},
			"Anthropic-Ratelimit-Unified-Status": {"allowed"},
		},
	}
	raw, _ := json.Marshal(record)
	_, err := handleUsage(raw)
	if err != nil {
		t.Fatalf("handleUsage error: %v", err)
	}

	entry := accountByID("claude-a@example.com")
	if entry == nil {
		t.Fatal("account state not created")
	}
	if entry.ResetsAt.Unix() != resetEpoch {
		t.Fatalf("resets at = %v, want %d", entry.ResetsAt, resetEpoch)
	}
	if entry.Status != "allowed" {
		t.Fatalf("status = %q, want allowed", entry.Status)
	}
}

func TestHandleUsage_IgnoresSevenDayResetAlone(t *testing.T) {
	// Only the 7d header present: must NOT populate ResetsAt.
	sevenDayEpoch := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC).Unix()
	record := usageRecord{
		Provider: "claude",
		AuthID:   "claude-b@example.com",
		ResponseHeaders: map[string][]string{
			"Anthropic-Ratelimit-Unified-7d-Reset":  {intToStr(sevenDayEpoch)},
			"Anthropic-Ratelimit-Unified-7d-Status": {"allowed"},
		},
	}
	raw, _ := json.Marshal(record)
	_, _ = handleUsage(raw)

	entry := accountByID("claude-b@example.com")
	if entry == nil {
		t.Fatal("account state not created")
	}
	if !entry.ResetsAt.IsZero() {
		t.Fatalf("resets at = %v, want zero (7d must not populate 5h)", entry.ResetsAt)
	}
	if entry.SevenDayReset.Unix() != sevenDayEpoch {
		t.Fatalf("7d reset = %v, want %d", entry.SevenDayReset, sevenDayEpoch)
	}
}

func TestHandleUsage_BothHeadersPickFiveHour(t *testing.T) {
	fiveHourEpoch := time.Date(2026, 9, 2, 21, 30, 0, 0, time.UTC).Unix()
	sevenDayEpoch := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC).Unix()
	record := usageRecord{
		Provider: "claude",
		AuthID:   "claude-c@example.com",
		ResponseHeaders: map[string][]string{
			"Anthropic-Ratelimit-Unified-Reset":    {intToStr(fiveHourEpoch)},
			"Anthropic-Ratelimit-Unified-7d-Reset": {intToStr(sevenDayEpoch)},
		},
	}
	raw, _ := json.Marshal(record)
	_, _ = handleUsage(raw)

	entry := accountByID("claude-c@example.com")
	if entry.ResetsAt.Unix() != fiveHourEpoch {
		t.Fatalf("5h reset = %v, want %d", entry.ResetsAt, fiveHourEpoch)
	}
	if entry.SevenDayReset.Unix() != sevenDayEpoch {
		t.Fatalf("7d reset = %v, want %d", entry.SevenDayReset, sevenDayEpoch)
	}
}

func TestHandleUsage_MixedHeaderCasingIsCanonicalized(t *testing.T) {
	resetEpoch := time.Now().Add(3 * time.Hour).Unix()
	record := usageRecord{
		Provider: "claude",
		AuthID:   "claude-d@example.com",
		ResponseHeaders: map[string][]string{
			// Go canonicalizes header names when parsed via http.Header.Add,
			// but the raw wire map may keep any casing. http.Header.Get is
			// case-insensitive for canonical forms; our Add-loop preserves
			// whatever casing the host sends, simulating mixed casing.
			"anthropic-ratelimit-unified-reset": {intToStr(resetEpoch)},
		},
	}
	raw, _ := json.Marshal(record)
	_, _ = handleUsage(raw)

	entry := accountByID("claude-d@example.com")
	if entry.ResetsAt.Unix() != resetEpoch {
		t.Fatalf("resets at = %v, want %d (mixed casing must still parse)", entry.ResetsAt, resetEpoch)
	}
}

func TestHandleUsage_InvalidValuesIgnored(t *testing.T) {
	badValues := []string{"", "-1", "abc", "99999999999999999999"}
	for _, bad := range badValues {
		record := usageRecord{
			Provider: "claude",
			AuthID:   "claude-e@example.com",
			ResponseHeaders: map[string][]string{
				"Anthropic-Ratelimit-Unified-Reset": {bad},
			},
		}
		raw, _ := json.Marshal(record)
		_, _ = handleUsage(raw)
		entry := accountByID("claude-e@example.com")
		if entry != nil && !entry.ResetsAt.IsZero() {
			t.Fatalf("value %q should be ignored, got resets at %v", bad, entry.ResetsAt)
		}
	}
}

func TestHandleUsage_NonClaudeProviderIgnored(t *testing.T) {
	record := usageRecord{
		Provider: "gemini",
		AuthID:   "gemini-a",
		ResponseHeaders: map[string][]string{
			"Anthropic-Ratelimit-Unified-Reset": {intToStr(time.Now().Unix())},
		},
	}
	raw, _ := json.Marshal(record)
	_, _ = handleUsage(raw)
	if accountByID("gemini-a") != nil {
		t.Fatal("non-claude provider must not create state")
	}
}

func TestHandleUsage_EmptyAuthIDIgnored(t *testing.T) {
	record := usageRecord{
		Provider:        "claude",
		ResponseHeaders: map[string][]string{},
	}
	raw, _ := json.Marshal(record)
	_, err := handleUsage(raw)
	if err != nil {
		t.Fatalf("handleUsage error: %v", err)
	}
}

func TestHandleUsage_NeverFailsOnMalformedJSON(t *testing.T) {
	_, err := handleUsage([]byte("not json"))
	if err != nil {
		t.Fatalf("malformed json must not fail: %v", err)
	}
}

func intToStr(v int64) string {
	return strconv.FormatInt(v, 10)
}
