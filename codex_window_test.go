package main

import (
	"net/http"
	"testing"
	"time"
)

// codexTestConfig builds a config with the Codex window defaults applied.
func codexTestConfig(t *testing.T) *Config {
	t.Helper()
	cfg := &Config{}
	if errNormalize := cfg.normalize(); errNormalize != nil {
		t.Fatalf("normalize: %v", errNormalize)
	}
	return cfg
}

// The base "primary" window is the weekly limit in real payloads. Anchoring
// against it would fire once a week instead of every five hours, so the
// 300-minute window nested under an additional limit must win.
func TestObserveCodexUsage_PrefersFiveHourWindowOverWeeklyPrimary(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Codex-Primary-Used-Percent", "48")
	headers.Set("X-Codex-Primary-Window-Minutes", "10080")
	headers.Set("X-Codex-Primary-Reset-At", "1786677299")
	headers.Set("X-Codex-Additional-GPT-5.3-Codex-Spark-Primary-Used-Percent", "3")
	headers.Set("X-Codex-Additional-GPT-5.3-Codex-Spark-Primary-Window-Minutes", "300")
	headers.Set("X-Codex-Additional-GPT-5.3-Codex-Spark-Primary-Reset-At", "1787231961")

	observation, ok := observeCodexUsage(codexTestConfig(t), headers)
	if !ok {
		t.Fatal("expected an observation")
	}
	if got := observation.ResetsAt.Unix(); got != 1787231961 {
		t.Fatalf("five-hour reset = %d, want 1787231961 (the 300-minute window)", got)
	}
	if got := observation.SecondaryReset.Unix(); got != 1786677299 {
		t.Fatalf("weekly reset = %d, want 1786677299", got)
	}
	if observation.Status != "allowed" {
		t.Fatalf("status = %q, want allowed", observation.Status)
	}
}

// A response carrying only the weekly window must not produce a five-hour
// reset: a zero ResetsAt keeps the scheduler in "no signal" mode rather than
// anchoring against the wrong boundary.
func TestObserveCodexUsage_WeeklyOnlyDoesNotSetFiveHourReset(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Codex-Primary-Used-Percent", "12")
	headers.Set("X-Codex-Primary-Window-Minutes", "10080")
	headers.Set("X-Codex-Primary-Reset-At", "1786677299")

	observation, ok := observeCodexUsage(codexTestConfig(t), headers)
	if !ok {
		t.Fatal("expected the weekly window to still be reported")
	}
	if !observation.ResetsAt.IsZero() {
		t.Fatalf("five-hour reset = %v, want zero", observation.ResetsAt)
	}
	if got := observation.SecondaryReset.Unix(); got != 1786677299 {
		t.Fatalf("weekly reset = %d, want 1786677299", got)
	}
}

// Reset-After-Seconds is relative; it must be resolved against the
// observation time when no absolute Reset-At is present.
func TestObserveCodexUsage_ResolvesRelativeReset(t *testing.T) {
	fixed := time.Date(2026, 9, 2, 16, 0, 0, 0, time.UTC)
	restore := timeNow
	timeNow = func() time.Time { return fixed }
	defer func() { timeNow = restore }()

	headers := http.Header{}
	headers.Set("X-Codex-Secondary-Used-Percent", "5")
	headers.Set("X-Codex-Secondary-Window-Minutes", "300")
	headers.Set("X-Codex-Secondary-Reset-After-Seconds", "3600")

	observation, ok := observeCodexUsage(codexTestConfig(t), headers)
	if !ok {
		t.Fatal("expected an observation")
	}
	if want := fixed.Add(time.Hour); !observation.ResetsAt.Equal(want) {
		t.Fatalf("reset = %v, want %v", observation.ResetsAt, want)
	}
}

// An absolute Reset-At outranks the relative form for the same window.
func TestObserveCodexUsage_AbsoluteResetWins(t *testing.T) {
	fixed := time.Date(2026, 9, 2, 16, 0, 0, 0, time.UTC)
	restore := timeNow
	timeNow = func() time.Time { return fixed }
	defer func() { timeNow = restore }()

	headers := http.Header{}
	headers.Set("X-Codex-Primary-Window-Minutes", "300")
	headers.Set("X-Codex-Primary-Reset-After-Seconds", "3600")
	headers.Set("X-Codex-Primary-Reset-At", "1787231961")

	observation, ok := observeCodexUsage(codexTestConfig(t), headers)
	if !ok {
		t.Fatal("expected an observation")
	}
	if got := observation.ResetsAt.Unix(); got != 1787231961 {
		t.Fatalf("reset = %d, want the absolute 1787231961", got)
	}
}

// A window missing either its length or its reset time cannot drive
// scheduling and must be dropped rather than half-applied.
func TestParseCodexWindows_DropsIncompleteWindows(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Codex-Primary-Used-Percent", "42")
	headers.Set("X-Codex-Secondary-Window-Minutes", "300")

	if windows := parseCodexWindows(headers, time.Now()); len(windows) != 0 {
		t.Fatalf("windows = %#v, want none", windows)
	}
}

// limit_reached maps to the rejected badge regardless of utilization.
func TestObserveCodexUsage_LimitReachedMapsToRejected(t *testing.T) {
	headers := http.Header{}
	headers.Set("X-Codex-Primary-Window-Minutes", "300")
	headers.Set("X-Codex-Primary-Reset-At", "1787231961")
	headers.Set("X-Codex-Primary-Used-Percent", "100")
	headers.Set("X-Codex-Primary-Limit-Reached", "true")

	observation, ok := observeCodexUsage(codexTestConfig(t), headers)
	if !ok {
		t.Fatal("expected an observation")
	}
	if observation.Status != "rejected" {
		t.Fatalf("status = %q, want rejected", observation.Status)
	}
}

// Non-Codex headers must never be mistaken for a window.
func TestObserveCodexUsage_IgnoresUnrelatedHeaders(t *testing.T) {
	headers := http.Header{}
	headers.Set("Anthropic-Ratelimit-Unified-Reset", "1788354000")
	headers.Set("Content-Type", "application/json")

	if _, ok := observeCodexUsage(codexTestConfig(t), headers); ok {
		t.Fatal("expected no Codex observation from Anthropic headers")
	}
}

// The target window length is configurable so a future upstream change does
// not require a new plugin build.
func TestObserveCodexUsage_HonoursConfiguredTargetWindow(t *testing.T) {
	cfg := codexTestConfig(t)
	cfg.CodexWindowMinutes = 10080
	cfg.CodexWindowTolerance = 60

	headers := http.Header{}
	headers.Set("X-Codex-Primary-Window-Minutes", "10080")
	headers.Set("X-Codex-Primary-Reset-At", "1786677299")
	headers.Set("X-Codex-Additional-Spark-Primary-Window-Minutes", "300")
	headers.Set("X-Codex-Additional-Spark-Primary-Reset-At", "1787231961")

	observation, ok := observeCodexUsage(cfg, headers)
	if !ok {
		t.Fatal("expected an observation")
	}
	if got := observation.ResetsAt.Unix(); got != 1786677299 {
		t.Fatalf("reset = %d, want the configured 10080-minute window", got)
	}
}
