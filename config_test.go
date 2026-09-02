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

func TestParseConfig_ProviderDefaults(t *testing.T) {
	cfg, err := parseConfig([]byte("timezone: \"UTC\"\nanchors: [\"06:30\"]\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// Claude keeps working for installs that predate per-provider config.
	if !cfg.providerEnabled(providerClaude) {
		t.Fatal("claude should be enabled by default")
	}
	// Codex spends quota on a second provider, so it must be opt-in.
	if cfg.providerEnabled(providerCodex) {
		t.Fatal("codex must be opt-in")
	}
	if got := cfg.modelFor(providerCodex); got != defaultCodexModel {
		t.Fatalf("codex model = %q, want %q", got, defaultCodexModel)
	}
	if cfg.CodexWindowMinutes != codexTargetWindowMinutes {
		t.Fatalf("codex window = %d, want %d", cfg.CodexWindowMinutes, codexTargetWindowMinutes)
	}
}

func TestParseConfig_ProviderAnchorsOverrideGlobal(t *testing.T) {
	cfg, err := parseConfig([]byte(`
timezone: "Asia/Shanghai"
anchors: ["06:30", "11:30", "16:30"]
providers:
  codex:
    enabled: true
    anchors: ["07:00", "12:00"]
    model: "gpt-5.4"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !cfg.providerEnabled(providerCodex) {
		t.Fatal("codex should be enabled")
	}
	codexAnchors := cfg.anchorsForAccount("codex-a@example.com", providerCodex)
	if len(codexAnchors) != 2 || codexAnchors[0].Hour != 7 || codexAnchors[1].Hour != 12 {
		t.Fatalf("codex anchors = %v, want 07:00/12:00", codexAnchors)
	}
	// Claude has no override and must keep the global list.
	claudeAnchors := cfg.anchorsForAccount("claude-a@example.com", providerClaude)
	if len(claudeAnchors) != 3 {
		t.Fatalf("claude anchors = %v, want the global three", claudeAnchors)
	}
	if got := cfg.modelFor(providerCodex); got != "gpt-5.4" {
		t.Fatalf("codex model = %q, want gpt-5.4", got)
	}
}

// An unparseable provider anchor list must fail loudly: silently falling back
// to the global anchors would anchor at times nobody configured.
func TestParseConfig_InvalidProviderAnchorsFails(t *testing.T) {
	_, err := parseConfig([]byte(`
timezone: "UTC"
anchors: ["06:30"]
providers:
  codex:
    enabled: true
    anchors: ["25:99"]
`))
	if err == nil {
		t.Fatal("expected invalid provider anchor error")
	}
}

// The legacy top-level model has always meant the Claude model.
func TestModelFor_TopLevelModelAppliesToClaudeOnly(t *testing.T) {
	cfg, err := parseConfig([]byte("timezone: \"UTC\"\nanchors: [\"06:30\"]\nmodel: \"claude-custom\"\n"))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := cfg.modelFor(providerClaude); got != "claude-custom" {
		t.Fatalf("claude model = %q, want claude-custom", got)
	}
	if got := cfg.modelFor(providerCodex); got != defaultCodexModel {
		t.Fatalf("codex model = %q, must not inherit the claude model", got)
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
