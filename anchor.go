package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Retry backoff parameters for a single anchor slot.
const (
	maxAnchorRetries = 5
	anchorRetryBase  = 30 * time.Second
)

// anchorRequestBody builds the minimal Claude messages body. max_tokens 1
// and a one-character prompt keep the request to a few tokens while still
// registering as the first request of the new window.
func anchorRequestBody(cfg *Config) []byte {
	body, _ := json.Marshal(map[string]any{
		"model":      cfg.Model,
		"max_tokens": cfg.MaxTokens,
		"messages": []map[string]any{
			{"role": "user", "content": cfg.Prompt},
		},
	})
	return body
}

// hostModelExecutionRequest mirrors pluginapi.HostModelExecutionRequest plus
// the optional host_callback_id. The request has json tags; headers carry the
// scheduler pin. Note: there is no auth_id field — the pin travels via the
// scheduler header instead.
type hostModelExecutionRequest struct {
	pluginapi.HostModelExecutionRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// hostModelExecutionResponse mirrors pluginapi.HostModelExecutionResponse.
type hostModelExecutionResponse struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers"`
	Body       []byte      `json:"body"`
}

// anchorAccount performs one anchor request for one account at the given slot.
// The request is routed by the Scheduler capability (when claimed) to the
// account's auth via X-Window-Anchor-Auth-Id; in single-account mode the
// built-in scheduler picks its own credential.
func anchorAccount(ctx context.Context, cfg *Config, account claudeAccount, slotKey windowKey, anchorTime time.Time) {
	// fireAt is the moment this slot becomes actionable. The loop passes only
	// due slots, so fireAt is at or before now; if it is still in the future
	// (a slot deferred to reset+grace), wait until then.
	fireAt := anchorTime
	if fireAt.After(time.Now()) {
		sleepCtx(ctx, fireAt.Sub(time.Now()))
	}
	if ctx.Err() != nil {
		return
	}

	if cfg.DryRun {
		logInfo("dry-run anchor", map[string]any{
			"auth_id": account.ID,
			"slot":    string(slotKey),
			"time":    fireAt.Format(time.RFC3339),
		})
		markAnchored(account.ID, string(slotKey), fireAt)
		return
	}

	body := anchorRequestBody(cfg)
	headers := http.Header{}
	if cfg.Scheduler.Header != "" && account.ID != "" {
		headers.Set(cfg.Scheduler.Header, account.ID)
	}

	req := hostModelExecutionRequest{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{
			EntryProtocol: "claude",
			ExitProtocol:  "claude",
			Model:         cfg.Model,
			Stream:        false, // host rejects stream=true
			Body:          body,
			Headers:       headers,
		},
	}

	var resp hostModelExecutionResponse
	if errCall := callHost(pluginabi.MethodHostModelExecute, req, &resp); errCall != nil {
		handleAnchorFailure(account.ID, string(slotKey), fmt.Errorf("host.model.execute: %w", errCall))
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		handleAnchorFailure(account.ID, string(slotKey), fmt.Errorf("upstream status %d", resp.StatusCode))
		return
	}

	markAnchored(account.ID, string(slotKey), fireAt)
	logInfo("anchor succeeded", map[string]any{
		"auth_id":     account.ID,
		"slot":        string(slotKey),
		"status_code": resp.StatusCode,
		// The new resets_at arrives asynchronously via usage.handle; the
		// dashboard shows it once observed.
	})
}

// handleAnchorFailure records a failure and schedules the exponential backoff.
// A hard failure (401/403) abandons the slot; transient errors retry with
// jitter through the loop's normal tick.
func handleAnchorFailure(authID, slot string, failErr error) {
	msg := failErr.Error()
	if strings.Contains(msg, "status 401") || strings.Contains(msg, "status 403") {
		// Terminal: never retry within the same slot.
		setAnchorTerminalFailure(authID, msg)
		logError("anchor rejected; abandoning slot", map[string]any{
			"auth_id": authID,
			"slot":    slot,
			"error":   msg,
		})
		return
	}
	retry := recordAnchorFailure(authID, slot, failErr)
	logWarn("anchor failed", map[string]any{
		"auth_id": authID,
		"slot":    slot,
		"error":   msg,
		"retry":   retry,
	})
}

// sleepCtx sleeps for the duration or until ctx is cancelled.
func sleepCtx(ctx context.Context, duration time.Duration) {
	if duration <= 0 {
		return
	}
	select {
	case <-ctx.Done():
	case <-time.After(duration):
	}
}
