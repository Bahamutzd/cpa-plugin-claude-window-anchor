package main

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

// Plugin identity constants. GitHubRepository must be non-empty for the host
// to accept registration.
const (
	pluginVersion    = "0.1.2"
	pluginAuthor     = "Bahamutzd"
	pluginRepository = "https://github.com/Bahamutzd/cpa-plugin-claude-window-anchor"
)

// Defaults for every configurable duration and knob. Durations are declared
// as strings in YAML (yaml.v3 does not parse "90s" into time.Duration), then
// normalized in normalize().
const (
	defaultTimezone       = "Asia/Shanghai"
	defaultGracePeriod    = "90s"
	defaultMaxDeferral    = "60m"
	defaultCatchUpWindow  = "45m"
	defaultPollInterval   = "30s"
	defaultAccountStagger = "5s"
	defaultModel          = "claude-haiku-4-5-20251001"
	defaultMaxTokens      = 1
	defaultPrompt         = "."
	defaultSchedulerMode  = "auto"
	defaultSchedulerHdr   = "X-Window-Anchor-Auth-Id"
)

// Config is the plugin configuration parsed from
// plugins.configs.claude-window-anchor in config.yaml. Duration fields are
// strings on disk and converted to time.Duration in normalize().
type Config struct {
	Timezone       string   `yaml:"timezone"`
	Anchors        []string `yaml:"anchors"`
	GracePeriod    string   `yaml:"grace-period"`
	MaxDeferral    string   `yaml:"max-deferral"`
	CatchUpWindow  string   `yaml:"catch-up-window"`
	PollInterval   string   `yaml:"poll-interval"`
	AccountStagger string   `yaml:"account-stagger"`

	Model     string `yaml:"model"`
	MaxTokens int    `yaml:"max-tokens"`
	Prompt    string `yaml:"prompt"`

	Scheduler schedulerConfig `yaml:"scheduler"`
	Accounts  accountsConfig  `yaml:"accounts"`

	OAuthUsageProbe bool   `yaml:"oauth-usage-probe"`
	ProbeOnStart    bool   `yaml:"probe-on-start"`
	StateFile       string `yaml:"state-file"`
	DryRun          bool   `yaml:"dry-run"`

	Enabled  bool `yaml:"enabled"`
	Priority int  `yaml:"priority"`

	// normalized durations, populated by normalize()
	graceDuration    time.Duration
	maxDeferDuration time.Duration
	catchUpDuration  time.Duration
	pollDuration     time.Duration
	staggerDuration  time.Duration
	location         *time.Location
	anchorTimes      []anchorTime
}

type schedulerConfig struct {
	Mode   string `yaml:"mode"`
	Header string `yaml:"header"`
}

type accountsConfig struct {
	Include   []string          `yaml:"include"`
	Exclude   []string          `yaml:"exclude"`
	Overrides []accountOverride `yaml:"overrides"`
}

type accountOverride struct {
	ID      string   `yaml:"id"`
	Anchors []string `yaml:"anchors"`
	Enabled *bool    `yaml:"enabled"`
}

// configStore holds the latest parsed configuration. The background loop and
// the scheduler pick handler read through it atomically.
var configStore atomic.Pointer[Config]

// parseConfig decodes the YAML subtree and normalizes defaults.
func parseConfig(raw []byte) (*Config, error) {
	cfg := &Config{}
	if len(raw) > 0 {
		if errUnmarshal := yaml.Unmarshal(raw, cfg); errUnmarshal != nil {
			return nil, fmt.Errorf("decode config yaml: %w", errUnmarshal)
		}
	}
	if errNormalize := cfg.normalize(); errNormalize != nil {
		return nil, errNormalize
	}
	return cfg, nil
}

// normalize fills defaults, converts duration strings, loads the timezone and
// compiles the anchor list. It must be idempotent: called once per parse.
func (c *Config) normalize() error {
	if strings.TrimSpace(c.Timezone) == "" {
		c.Timezone = defaultTimezone
	}
	loc, errLoad := time.LoadLocation(c.Timezone)
	if errLoad != nil {
		// Never silently proceed with wrong times. Degrade to UTC loudly,
		// the operator must fix timezone or the anchors shift.
		logError("timezone load failed; falling back to UTC", map[string]any{
			"timezone": c.Timezone,
			"error":    errLoad.Error(),
		})
		c.Timezone = "UTC"
		loc = time.UTC
	}
	c.location = loc

	var errDur error
	if c.graceDuration, errDur = parseDurationOr(c.GracePeriod, defaultGracePeriod); errDur != nil {
		return fmt.Errorf("grace-period: %w", errDur)
	}
	if c.maxDeferDuration, errDur = parseDurationOr(c.MaxDeferral, defaultMaxDeferral); errDur != nil {
		return fmt.Errorf("max-deferral: %w", errDur)
	}
	if c.catchUpDuration, errDur = parseDurationOr(c.CatchUpWindow, defaultCatchUpWindow); errDur != nil {
		return fmt.Errorf("catch-up-window: %w", errDur)
	}
	if c.pollDuration, errDur = parseDurationOr(c.PollInterval, defaultPollInterval); errDur != nil {
		return fmt.Errorf("poll-interval: %w", errDur)
	}
	if c.staggerDuration, errDur = parseDurationOr(c.AccountStagger, defaultAccountStagger); errDur != nil {
		return fmt.Errorf("account-stagger: %w", errDur)
	}

	if strings.TrimSpace(c.Model) == "" {
		c.Model = defaultModel
	}
	if c.MaxTokens <= 0 {
		c.MaxTokens = defaultMaxTokens
	}
	if c.Prompt == "" {
		c.Prompt = defaultPrompt
	}

	if c.Scheduler.Mode == "" {
		c.Scheduler.Mode = defaultSchedulerMode
	}
	if strings.TrimSpace(c.Scheduler.Header) == "" {
		c.Scheduler.Header = defaultSchedulerHdr
	}

	if len(c.Anchors) == 0 {
		// Store install only writes enabled + store. Without a default the
		// plugin fails register/reconfigure and stays registered:false.
		c.Anchors = []string{"06:30", "11:30", "16:30"}
	}
	times, errParse := parseAnchors(c.Anchors)
	if errParse != nil {
		return fmt.Errorf("anchors: %w", errParse)
	}
	c.anchorTimes = times
	return nil
}

// parseDurationOr parses a duration string, falling back to fallback when the
// field is empty or unset.
func parseDurationOr(value, fallback string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	dur, errParse := time.ParseDuration(value)
	if errParse != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", value, errParse)
	}
	if dur < 0 {
		return 0, fmt.Errorf("duration %q must not be negative", value)
	}
	return dur, nil
}

// anchorTime is one wall-clock anchor hour:minute within the configured zone.
type anchorTime struct {
	Hour   int
	Minute int
}

// parseAnchors parses "HH:MM" strings, rejects duplicates, and sorts them.
func parseAnchors(values []string) ([]anchorTime, error) {
	seen := make(map[string]struct{}, len(values))
	out := make([]anchorTime, 0, len(values))
	for _, value := range values {
		key := strings.TrimSpace(value)
		parts := strings.Split(key, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid anchor %q, want HH:MM", value)
		}
		var hour, minute int
		if _, errScan := fmt.Sscanf(parts[0], "%d", &hour); errScan != nil {
			return nil, fmt.Errorf("invalid anchor hour in %q", value)
		}
		if _, errScan := fmt.Sscanf(parts[1], "%d", &minute); errScan != nil {
			return nil, fmt.Errorf("invalid anchor minute in %q", value)
		}
		if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
			return nil, fmt.Errorf("anchor %q out of range", value)
		}
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("duplicate anchor %q", value)
		}
		seen[key] = struct{}{}
		out = append(out, anchorTime{Hour: hour, Minute: minute})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Hour != out[j].Hour {
			return out[i].Hour < out[j].Hour
		}
		return out[i].Minute < out[j].Minute
	})
	if len(out) == 0 {
		return nil, fmt.Errorf("anchors list is empty")
	}
	return out, nil
}

// appliesTo reports whether an account ID is selected by include/exclude and
// per-account overrides. Empty include means all accounts are eligible.
func (c *Config) appliesTo(accountID string) bool {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return false
	}
	// Include list, when present, is a whitelist.
	if len(c.Accounts.Include) > 0 {
		found := false
		for _, id := range c.Accounts.Include {
			if strings.EqualFold(strings.TrimSpace(id), accountID) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	// Exclude list is a blacklist, applied last.
	for _, id := range c.Accounts.Exclude {
		if strings.EqualFold(strings.TrimSpace(id), accountID) {
			return false
		}
	}
	return true
}

// anchorsForAccount returns the effective anchor list for an account,
// applying per-account overrides when present.
func (c *Config) anchorsForAccount(accountID string) []anchorTime {
	for _, override := range c.Accounts.Overrides {
		if strings.EqualFold(strings.TrimSpace(override.ID), accountID) {
			if override.Enabled != nil && !*override.Enabled {
				return nil
			}
			if len(override.Anchors) > 0 {
				times, errParse := parseAnchors(override.Anchors)
				if errParse == nil {
					return times
				}
				logWarn("account override anchors invalid; using global", map[string]any{
					"account": accountID,
					"anchors": override.Anchors,
					"error":   errParse.Error(),
				})
			}
		}
	}
	return c.anchorTimes
}

// shouldClaimScheduler decides whether this plugin declares the Scheduler
// capability. auto claims the slot only when more than one account is tracked
// (the host grants the single scheduler slot to the first claiming plugin).
func shouldClaimScheduler(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	switch strings.TrimSpace(cfg.Scheduler.Mode) {
	case "always":
		return true
	case "never":
		return false
	default: // auto
		return len(cfg.Accounts.Include) == 0 || len(cfg.Accounts.Include) > 1
	}
}
