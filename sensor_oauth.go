package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

// oauthUsageURL is Anthropic's OAuth control-plane usage endpoint. It is an
// undocumented private API: the response shape may change without notice, and
// the host's plain Go HTTP client (no uTLS Firefox fingerprint) may be blocked
// by Cloudflare. This probe is therefore opt-in and strictly best-effort; the
// passive usage sensor is the primary mechanism.
const oauthUsageURL = "https://api.anthropic.com/api/oauth/usage"

// hostHTTPRequest mirrors the host.http.do request shape. Headers and body
// use snake_case payload keys.
type hostHTTPRequest struct {
	Method  string      `json:"method,omitempty"`
	URL     string      `json:"url,omitempty"`
	Headers http.Header `json:"headers,omitempty"`
	Body    []byte      `json:"body,omitempty"`
}

// httpResponse mirrors pluginapi.HTTPResponse.
type httpResponse struct {
	StatusCode int         `json:"status_code"`
	Headers    http.Header `json:"headers"`
	Body       []byte      `json:"body"`
}

// oauthUsageResponse is the parsed /api/oauth/usage response body.
type oauthUsageResponse struct {
	FiveHour oauthUsageWindow `json:"five_hour"`
	SevenDay oauthUsageWindow `json:"seven_day"`
}

type oauthUsageWindow struct {
	Utilization int   `json:"utilization"`
	ResetsAt    int64 `json:"resets_at"`
}

// probeOAuthUsage performs one active usage query for an account via
// host.http.do, using the access token fetched by host.auth.get. Failures
// degrade silently: the passive Tier 1 sensor remains authoritative.
func probeOAuthUsage(account anchoredAccount) {
	// The endpoint is Anthropic-specific; Codex credentials must never be sent
	// to it. Codex quota arrives through the passive usage sensor instead.
	if account.Provider != providerClaude {
		return
	}
	if account.AuthIndex == "" || account.AccessToken == "" {
		return
	}
	var authRes hostAuthGetResponse
	if errCall := callHost(pluginabi.MethodHostAuthGet, hostAuthGetRequest{AuthIndex: account.AuthIndex}, &authRes); errCall != nil {
		setLastUsageError(account.ID, errCall.Error())
		return
	}

	req := hostHTTPRequest{
		Method: http.MethodGet,
		URL:    oauthUsageURL,
		Headers: http.Header{
			"Accept":        []string{"application/json, text/plain, */*"},
			"Authorization": []string{"Bearer " + account.AccessToken},
			"User-Agent":    []string{"claude-cli/2.1.220 (external, cli)"},
		},
	}
	var resp httpResponse
	if errCall := callHost(pluginabi.MethodHostHTTPDo, req, &resp); errCall != nil {
		setLastUsageError(account.ID, errCall.Error())
		return
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		setLastUsageError(account.ID, "oauth usage probe status "+strconv.Itoa(resp.StatusCode))
		return
	}
	var usage oauthUsageResponse
	if errUnmarshal := json.Unmarshal(resp.Body, &usage); errUnmarshal != nil {
		setLastUsageError(account.ID, "unmarshal usage: "+errUnmarshal.Error())
		return
	}
	if usage.FiveHour.ResetsAt > 0 {
		observeReset(account.ID, time.Unix(usage.FiveHour.ResetsAt, 0),
			usageStatusFromUtilization(usage.FiveHour.Utilization))
	}
	if usage.SevenDay.ResetsAt > 0 {
		observeSevenDayReset(account.ID, time.Unix(usage.SevenDay.ResetsAt, 0),
			usageStatusFromUtilization(usage.SevenDay.Utilization))
	}
	setLastUsageError(account.ID, "")
}

// usageStatusFromUtilization maps a utilization percentage to a coarse status
// for display. The authoritative status comes from the unified headers.
func usageStatusFromUtilization(utilization int) string {
	switch {
	case utilization >= 90:
		return "allowed_warning"
	case utilization > 0:
		return "allowed"
	default:
		return ""
	}
}

// isClaudeOAuthEndpoint is kept for future reference; the probe URL is fixed.
func isClaudeOAuthEndpoint(url string) bool {
	return strings.Contains(url, "api/anthropic.com/api/oauth/")
}
