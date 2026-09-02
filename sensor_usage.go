package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
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

// handleUsage is the Tier 1 sensor: every request for a supported provider
// (organic user traffic and our own anchor requests) lands here with the
// unfiltered upstream response headers and the AuthID that served it.
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
	if record.AuthID == "" {
		return okResult(struct{}{})
	}
	spec, okSpec := specFor(record.Provider)
	if !okSpec {
		return okResult(struct{}{})
	}

	headers := http.Header{}
	for key, values := range record.ResponseHeaders {
		for _, value := range values {
			headers.Add(key, value)
		}
	}

	cfg := configStore.Load()
	observation, okObserve := spec.observeUsage(cfg, headers)
	if !okObserve {
		return okResult(struct{}{})
	}

	// The longer window is recorded first so a response that carries only the
	// weekly figure still updates the display, but it never influences
	// five-hour scheduling.
	if !observation.SecondaryReset.IsZero() {
		observeSevenDayReset(record.AuthID, observation.SecondaryReset, observation.SecondaryStatus)
	}
	if !observation.ResetsAt.IsZero() {
		observeReset(record.AuthID, observation.ResetsAt, observation.Status)
	}

	return okResult(struct{}{})
}

// unixTime converts a unix seconds epoch to a time.Time. Kept as a variable
// for unit-test injection.
var unixTime = func(epoch int64) time.Time {
	return time.Unix(epoch, 0).UTC()
}

// normalizeProvider lowercases and trims a host provider identifier.
func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}
