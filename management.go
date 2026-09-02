package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// managementRegistrationResponse is the pluginapi.ManagementRegistrationResponse
// wire shape (no json tags on ManagementRoute/ResourceRoute, so PascalCase).
type managementRegistrationResponse struct {
	Routes    []managementRoute `json:"routes,omitempty"`
	Resources []resourceRoute   `json:"resources,omitempty"`
}

type managementRoute struct {
	Path        string `json:"Path"`
	Method      string `json:"Method"`
	Description string `json:"Description,omitempty"`
}

type resourceRoute struct {
	Path        string `json:"Path"`
	Menu        string `json:"Menu,omitempty"`
	Description string `json:"Description,omitempty"`
}

// managementRequest mirrors pluginapi.ManagementRequest. Unlike the response
// types, the request struct uses the plugin's own json tags.
type managementRequest struct {
	Method         string              `json:"method,omitempty"`
	Path           string              `json:"path,omitempty"`
	Headers        http.Header         `json:"headers,omitempty"`
	Query          map[string][]string `json:"query,omitempty"`
	Body           []byte              `json:"body,omitempty"`
	HostCallbackID string              `json:"host_callback_id,omitempty"`
}

// managementResponse mirrors pluginapi.ManagementResponse (PascalCase keys).
type managementResponse struct {
	StatusCode int         `json:"StatusCode"`
	Headers    http.Header `json:"Headers"`
	Body       []byte      `json:"Body"`
}

// handleManagementRegister reports the plugin's management API surface.
// The JSON dashboard resource is browser-accessible without a key; the
// status JSON requires the management key.
func handleManagementRegister(raw []byte) ([]byte, error) {
	return okResult(managementRegistrationResponse{
		Resources: []resourceRoute{
			{
				Path:        "dashboard",
				Menu:        "额度窗口锚定",
				Description: "Claude 5 小时额度窗口锚定状态",
			},
			{
				// Browser-accessible JSON endpoint backing the dashboard.
				// It carries the same payload as the key-protected status
				// route but is readable without a management key.
				Path:        "status-data",
				Description: "Window anchor status JSON (dashboard data source)",
			},
		},
		Routes: []managementRoute{{
			Path:        "claude-window-anchor/status",
			Method:      "GET",
			Description: "Window anchor status JSON",
		}, {
			Path:        "claude-window-anchor/anchor-now",
			Method:      "POST",
			Description: "Trigger an immediate anchor for one or all accounts",
		}},
	})
}

// handleManagementHandle dispatches management.handle RPC calls by path.
// Panics are caught by dispatch's deferred recover.
func handleManagementHandle(raw []byte) ([]byte, error) {
	var req managementRequest
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			return okResult(managementResponse{
				StatusCode: http.StatusBadRequest,
				Body:       []byte(`{"error":"invalid management request"}`),
			})
		}
	}
	path := strings.TrimSpace(req.Path)
	switch {
	case strings.HasSuffix(path, "/dashboard"): // resource route
		return okResult(managementResponse{
			StatusCode: http.StatusOK,
			Headers:    htmlContentType(),
			Body:       renderDashboardHTML(),
		})
	case strings.HasSuffix(path, "/status-data"): // resource route, dashboard fetch target
		return okResult(managementResponse{
			StatusCode: http.StatusOK,
			Headers:    jsonContentType(),
			Body:       anchorStatusJSON(),
		})
	case strings.HasSuffix(path, "/status"):
		return okResult(managementResponse{
			StatusCode: http.StatusOK,
			Headers:    jsonContentType(),
			Body:       anchorStatusJSON(),
		})
	case strings.HasSuffix(path, "/anchor-now"):
		return handleAnchorNow(req)
	default:
		return okResult(managementResponse{
			StatusCode: http.StatusNotFound,
			Headers:    jsonContentType(),
			Body:       []byte(`{"error":"unknown plugin management path"}`),
		})
	}
}

// handleAnchorNow triggers an immediate anchor for one account (?account=xxx)
// or all eligible accounts when no filter is given.
func handleAnchorNow(req managementRequest) ([]byte, error) {
	st := map[string]any{
		"triggered": true,
		"mode":      "anchor-now",
		"time":      timeNow().Format(time.RFC3339),
	}

	filter := ""
	providerFilter := ""
	for key, values := range req.Query {
		if len(values) == 0 {
			continue
		}
		switch {
		case strings.EqualFold(key, "account"):
			filter = strings.TrimSpace(values[0])
		case strings.EqualFold(key, "provider"):
			providerFilter = normalizeProvider(values[0])
		}
	}

	cfg := configStore.Load()
	var accounts []anchoredAccount
	if cfg != nil {
		accounts = listAnchorAccounts()
	} else {
		return okResult(managementResponse{
			StatusCode: http.StatusServiceUnavailable,
			Headers:    jsonContentType(),
			Body:       []byte(`{"error":"plugin not configured"}`),
		})
	}

	anchored := []string{}
	for _, account := range accounts {
		// A manual anchor still honours the provider switch: firing at a
		// provider the operator disabled would spend quota they opted out of.
		if !cfg.providerEnabled(account.Provider) {
			continue
		}
		if !cfg.appliesTo(account.ID) {
			continue
		}
		if filter != "" && account.ID != filter {
			continue
		}
		if providerFilter != "" && account.Provider != providerFilter {
			continue
		}
		// Force: anchor now regardless of schedule state.
		slotKey := windowKey("manual-" + account.ID)
		anchorAccount(contextBg(), cfg, account, slotKey, timeNow())
		anchored = append(anchored, account.ID)
	}
	st["anchored"] = anchored

	body, _ := json.Marshal(st)
	return okResult(managementResponse{
		StatusCode: http.StatusOK,
		Headers:    jsonContentType(),
		Body:       body,
	})
}

// anchorStatusJSON renders the per-account status snapshot the dashboard data
// endpoint uses. HTML-sensitive values are never interpolated here; the
// dashboard injects them via JS.
func anchorStatusJSON() []byte {
	cfg := configStore.Load()
	snapshot := allAccountsSnapshots()
	type accountView struct {
		ID               string `json:"id"`
		Provider         string `json:"provider,omitempty"`
		AuthIndex        string `json:"auth_index"`
		Label            string `json:"label"`
		Status           string `json:"status"`
		Disabled         bool   `json:"disabled"`
		ResetsAt         string `json:"resets_at,omitempty"`
		ResetsAtObserved string `json:"resets_at_observed_at,omitempty"`
		SevenDayReset    string `json:"seven_day_reset,omitempty"`
		LastAnchoredSlot string `json:"last_anchored_slot,omitempty"`
		LastAnchorTime   string `json:"last_anchor_time,omitempty"`
		LastAnchorError  string `json:"last_anchor_error,omitempty"`
		NextRetryAt      string `json:"next_retry_at,omitempty"`
		LastUsageError   string `json:"last_usage_error,omitempty"`
	}
	views := make([]accountView, 0, len(snapshot))
	for _, entry := range snapshot {
		view := accountView{
			ID:        entry.AuthID,
			Provider:  entry.Provider,
			AuthIndex: entry.AuthIndex,
			Label:     entry.Label,
			Status:    entry.Status,
			Disabled:  entry.Disabled,
		}
		if !entry.ResetsAt.IsZero() {
			view.ResetsAt = entry.ResetsAt.Format(timeRFC3339)
			view.ResetsAtObserved = entry.ResetsAtObservedAt.Format(timeRFC3339)
		}
		if !entry.SevenDayReset.IsZero() {
			view.SevenDayReset = entry.SevenDayReset.Format(timeRFC3339)
		}
		if entry.LastAnchoredWindowKey != "" {
			view.LastAnchoredSlot = entry.LastAnchoredWindowKey
			view.LastAnchorTime = entry.LastAnchorTime.Format(timeRFC3339)
		}
		view.LastAnchorError = entry.LastAnchorError
		if !entry.NextRetryAt.IsZero() {
			view.NextRetryAt = entry.NextRetryAt.Format(timeRFC3339)
		}
		view.LastUsageError = entry.LastUsageError
		views = append(views, view)
	}

	var configView map[string]any
	if cfg != nil {
		providersView := make(map[string]any, len(providerSpecs))
		for id := range providerSpecs {
			anchors := cfg.Anchors
			if _, overridden := cfg.providerAnchors[id]; overridden {
				switch id {
				case providerClaude:
					anchors = cfg.Providers.Claude.Anchors
				case providerCodex:
					anchors = cfg.Providers.Codex.Anchors
				}
			}
			providersView[id] = map[string]any{
				"enabled": cfg.providerEnabled(id),
				"anchors": anchors,
				"model":   cfg.modelFor(id),
			}
		}
		configView = map[string]any{
			"timezone":             cfg.Timezone,
			"anchors":              cfg.Anchors,
			"grace_period":         cfg.GracePeriod,
			"max_deferral":         cfg.MaxDeferral,
			"scheduler_mode":       cfg.Scheduler.Mode,
			"model":                cfg.Model,
			"dry_run":              cfg.DryRun,
			"enabled":              cfg.Enabled,
			"oauth_usage_probe":    cfg.OAuthUsageProbe,
			"providers":            providersView,
			"codex_window_minutes": cfg.CodexWindowMinutes,
		}
	}

	payload := map[string]any{
		"now":      timeNow().Format(timeRFC3339),
		"config":   configView,
		"accounts": views,
	}
	body, _ := json.Marshal(payload)
	return body
}
