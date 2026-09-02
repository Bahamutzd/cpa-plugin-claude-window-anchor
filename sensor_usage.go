package main

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Exact header names for Anthropic's unified rate-limit headers. The 5h
// rolling window is "anthropic-ratelimit-unified-reset"; the weekly limit is
// "anthropic-ratelimit-unified-7d-reset". Never match by prefix — the 7d
// value would corrupt the scheduling decision.
const (
	headerUnifiedReset    = "Anthropic-Ratelimit-Unified-Reset"
	headerUnifiedStatus   = "Anthropic-Ratelimit-Unified-Status"
	headerUnified7DReset  = "Anthropic-Ratelimit-Unified-7d-Reset"
	headerUnified7DStatus = "Anthropic-Ratelimit-Unified-7d-Status"
)

// usageRecord is the pluginapi.UsageRecord wire shape. pluginapi.UsageRecord
// has no json tags; the host marshals it with field names as keys
// (e.g. "AuthID", "ResponseHeaders"), so this local struct must match those
// exact names. The usage.handle RPC delivers the record bare, not wrapped.
type usageRecord struct {
	Provider        string              `json:"Provider"`
	AuthID          string              `json:"AuthID"`
	AuthIndex       string              `json:"AuthIndex"`
	Model           string              `json:"Model"`
	ResponseHeaders map[string][]string `json:"ResponseHeaders"`
}

// handleUsage is the Tier 1 sensor: every Claude request (organic user
// traffic and our own anchor requests) lands here with the unfiltered
// upstream response headers and the AuthID that served it.
//
// host.model.execute responses hide headers behind the passthrough-headers
// config (default false), and the host skips a plugin's own response
// interceptor for its own callbacks — that is why usage.handle is the only
// reliable way to obtain the reset timestamp. Never migrate away from it.
func handleUsage(raw []byte) ([]byte, error) {
	// Usage handling must never fail or block the host: return an empty ok
	// envelope on any error.
	var record usageRecord
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &record); errUnmarshal != nil {
			return okResult(struct{}{})
		}
	}
	if !strings.EqualFold(record.Provider, "claude") || record.AuthID == "" {
		return okResult(struct{}{})
	}

	headers := http.Header{}
	for key, values := range record.ResponseHeaders {
		for _, value := range values {
			headers.Add(key, value)
		}
	}

	// Weekly (7d) limit is tracked for display only — observe it first so a
	// 7d-only response still records the weekly window, never let it influence
	// five-hour scheduling.
	if reset7dRaw := strings.TrimSpace(headers.Get(headerUnified7DReset)); reset7dRaw != "" {
		epoch7d, err7d := strconv.ParseInt(reset7dRaw, 10, 64)
		if err7d == nil && epoch7d > 0 {
			observeSevenDayReset(record.AuthID, unixTime(epoch7d),
				strings.TrimSpace(headers.Get(headerUnified7DStatus)))
		}
	}

	// Exact key match on the 5h reset.
	resetRaw := strings.TrimSpace(headers.Get(headerUnifiedReset))
	if resetRaw == "" {
		return okResult(struct{}{})
	}
	epoch, errParse := strconv.ParseInt(resetRaw, 10, 64)
	if errParse != nil || epoch <= 0 {
		logDebug("invalid unified reset value", map[string]any{
			"auth_id": record.AuthID,
			"value":   resetRaw,
		})
		return okResult(struct{}{})
	}

	status := strings.TrimSpace(headers.Get(headerUnifiedStatus))
	observeReset(record.AuthID, unixTime(epoch), status)

	return okResult(struct{}{})
}

// unixTime converts a unix seconds epoch to a time.Time. Kept as a variable
// for unit-test injection.
var unixTime = func(epoch int64) time.Time {
	return time.Unix(epoch, 0).UTC()
}
