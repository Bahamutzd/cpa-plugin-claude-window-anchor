package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Provider identifiers as reported by the host in auth entries and usage
// records. These match the executor Identifier() values: ClaudeExecutor
// returns "claude", CodexExecutor and CodexAutoExecutor both return "codex".
const (
	providerClaude = "claude"
	providerCodex  = "codex"
)

// providerSpec describes everything provider-specific about anchoring one
// upstream. The scheduling core (loop, schedule, state) is provider-agnostic
// and drives every account through this interface.
type providerSpec struct {
	// ID is the host provider identifier used to match auth entries and usage
	// records.
	ID string
	// EntryProtocol and ExitProtocol are the host.model.execute protocol pair.
	EntryProtocol string
	ExitProtocol  string
	// DefaultModel is the model used for anchor requests when the operator did
	// not override it.
	DefaultModel string
	// buildBody renders the minimal anchor request payload.
	buildBody func(cfg *Config, model string) []byte
	// observeUsage extracts the rolling window reset from usage response
	// headers. It returns false when the headers carry no usable signal.
	observeUsage func(cfg *Config, headers http.Header) (windowObservation, bool)
}

// windowObservation is one parsed reset signal for an account.
type windowObservation struct {
	// ResetsAt is the end of the current rolling window.
	ResetsAt time.Time
	// Status is a coarse quota state for display ("allowed", "rejected", ...).
	Status string
	// SecondaryReset is the longer (weekly) window, tracked for display only.
	SecondaryReset time.Time
	// SecondaryStatus mirrors SecondaryReset.
	SecondaryStatus string
}

// providerSpecs holds every supported upstream keyed by provider ID.
var providerSpecs = map[string]providerSpec{
	providerClaude: {
		ID:            providerClaude,
		EntryProtocol: "claude",
		ExitProtocol:  "claude",
		DefaultModel:  defaultModel,
		buildBody:     buildClaudeAnchorBody,
		observeUsage:  observeClaudeUsage,
	},
	providerCodex: {
		ID:            providerCodex,
		EntryProtocol: "openai-response",
		ExitProtocol:  "openai-response",
		DefaultModel:  defaultCodexModel,
		buildBody:     buildCodexAnchorBody,
		observeUsage:  observeCodexUsage,
	},
}

// specFor returns the provider descriptor for a host provider identifier.
func specFor(provider string) (providerSpec, bool) {
	spec, ok := providerSpecs[strings.ToLower(strings.TrimSpace(provider))]
	return spec, ok
}

// buildClaudeAnchorBody renders the minimal Claude messages payload.
// max_tokens 1 and a one-character prompt keep the request to a few tokens
// while still registering as the first request of the new window.
func buildClaudeAnchorBody(cfg *Config, model string) []byte {
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": cfg.MaxTokens,
		"messages": []map[string]any{
			{"role": "user", "content": cfg.Prompt},
		},
	})
	return body
}

// buildCodexAnchorBody renders the minimal Responses API payload. Codex takes
// OpenAI Responses input rather than Claude messages, so max_output_tokens
// replaces max_tokens and the prompt travels as an input item.
func buildCodexAnchorBody(cfg *Config, model string) []byte {
	body, _ := json.Marshal(map[string]any{
		"model": model,
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": cfg.Prompt},
				},
			},
		},
		"max_output_tokens": codexMinOutputTokens,
		"store":             false,
	})
	return body
}

// codexMinOutputTokens is the smallest output budget the Responses API
// accepts. Unlike Claude's max_tokens, values below 16 are rejected upstream.
const codexMinOutputTokens = 16

// Anthropic's unified rate-limit headers. The 5h rolling window is
// "anthropic-ratelimit-unified-reset"; the weekly limit is
// "anthropic-ratelimit-unified-7d-reset". Never match by prefix — the 7d
// value would corrupt the scheduling decision.
const (
	headerUnifiedReset    = "Anthropic-Ratelimit-Unified-Reset"
	headerUnifiedStatus   = "Anthropic-Ratelimit-Unified-Status"
	headerUnified7DReset  = "Anthropic-Ratelimit-Unified-7d-Reset"
	headerUnified7DStatus = "Anthropic-Ratelimit-Unified-7d-Status"
)

// observeClaudeUsage reads the unified Anthropic rate-limit headers.
func observeClaudeUsage(_ *Config, headers http.Header) (windowObservation, bool) {
	out := windowObservation{}

	// Weekly (7d) limit is tracked for display only — parse it first so a
	// 7d-only response still records the weekly window, but never let it
	// influence five-hour scheduling.
	if raw := strings.TrimSpace(headers.Get(headerUnified7DReset)); raw != "" {
		if epoch, errParse := strconv.ParseInt(raw, 10, 64); errParse == nil && epoch > 0 {
			out.SecondaryReset = unixTime(epoch)
			out.SecondaryStatus = strings.TrimSpace(headers.Get(headerUnified7DStatus))
		}
	}

	// Exact key match on the 5h reset.
	raw := strings.TrimSpace(headers.Get(headerUnifiedReset))
	if raw == "" {
		return out, !out.SecondaryReset.IsZero()
	}
	epoch, errParse := strconv.ParseInt(raw, 10, 64)
	if errParse != nil || epoch <= 0 {
		return out, !out.SecondaryReset.IsZero()
	}
	out.ResetsAt = unixTime(epoch)
	out.Status = strings.TrimSpace(headers.Get(headerUnifiedStatus))
	return out, true
}
