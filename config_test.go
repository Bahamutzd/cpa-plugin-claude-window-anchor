package main

import (
	"testing"
	"time"
)

func TestParseConfig_DefaultsAndDurationParsing(t *testing.T) {
	cfg, err := parseConfig([]byte("timezone: \"Asia/Shanghai\"\nanchors: [\"06:30\"]\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.graceDuration != 90*time.Second {
		t.Fatalf("grace = %v, want 90s", cfg.graceDuration)
	}
	if cfg.maxDeferDuration.String() != "1h0m0s" {
		t.Fatalf("maxDeferral = %v, want 1h", cfg.maxDeferDuration)
	}
	if cfg.pollDuration.String() != "30s" {
		t.Fatalf("poll = %v, want 30s", cfg.pollDuration)
	}
	if cfg.location.String() != "Asia/Shanghai" {
		t.Fatalf("location = %v", cfg.location)
	}
	if cfg.Model != defaultModel {
		t.Fatalf("model = %q, want default", cfg.Model)
	}
}

func TestParseConfig_InvalidDurationFails(t *testing.T) {
	_, err := parseConfig([]byte("timezone: \"UTC\"\nanchors: [\"06:30\"]\ngrace-period: \"not-a-duration\"\n"))
	if err == nil {
		t.Fatal("expected invalid duration error")
	}
}

func TestParseConfig_MissingAnchorsUsesDefaultSlots(t *testing.T) {
	cfg, err := parseConfig([]byte("timezone: \"UTC\"\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Anchors) != 3 || cfg.Anchors[0] != "06:30" || cfg.Anchors[2] != "16:30" {
		t.Fatalf("anchors = %v, want default 06:30/11:30/16:30", cfg.Anchors)
	}
}

func TestParseConfig_UnknownTimezoneFallsBackToUTC(t *testing.T) {
	cfg, err := parseConfig([]byte("timezone: \"Mars/Olympus\"\nanchors: [\"06:30\"]\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.location.String() != "UTC" {
		t.Fatalf("location = %v, want UTC fallback", cfg.location)
	}
}

func TestAppliesTo_IncludeExclude(t *testing.T) {
	cfg := &Config{
		Accounts: accountsConfig{
			Include: []string{"a@example.com"},
			Exclude: []string{"b@example.com"},
		},
	}
	if !cfg.appliesTo("a@example.com") {
		t.Fatal("include account should apply")
	}
	if cfg.appliesTo("other@example.com") {
		t.Fatal("non-included account should not apply")
	}
	if cfg.appliesTo("b@example.com") {
		t.Fatal("excluded account should not apply")
	}
}

func TestAppliesTo_EmptyIncludeAllowsAll(t *testing.T) {
	cfg := &Config{}
	if !cfg.appliesTo("anything@example.com") {
		t.Fatal("empty include should allow all")
	}
}

func TestShouldClaimScheduler_AutoModeSingleAccountDoesNotClaim(t *testing.T) {
	// Auto with an explicit single-account include list: safe to not claim.
	cfg := &Config{Scheduler: schedulerConfig{Mode: "auto"}, Accounts: accountsConfig{Include: []string{"only@example.com"}}}
	if shouldClaimScheduler(cfg) {
		t.Fatal("auto with one account should NOT claim scheduler")
	}
	cfg.Accounts.Include = []string{"a@example.com", "b@example.com"}
	if !shouldClaimScheduler(cfg) {
		t.Fatal("auto with two accounts should claim scheduler")
	}
	// No include list: unknown how many accounts exist; auto conservatively claims.
	cfg.Accounts.Include = nil
	if !shouldClaimScheduler(cfg) {
		t.Fatal("auto with no include should claim (potential multi-account)")
	}
}

func TestShouldClaimScheduler_ExplicitModes(t *testing.T) {
	if !shouldClaimScheduler(&Config{Scheduler: schedulerConfig{Mode: "always"}}) {
		t.Fatal("always should claim")
	}
	if shouldClaimScheduler(&Config{Scheduler: schedulerConfig{Mode: "never"}}) {
		t.Fatal("never should not claim")
	}
}
