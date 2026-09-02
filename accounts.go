package main

import (
	"encoding/json"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// hostAuthListRequest is an empty request for host.auth.list.
type hostAuthListRequest struct{}

// hostAuthListResponse mirrors the host's list response wire shape. The
// entries use snake_case json tags from pluginapi.HostAuthFileEntry.
type hostAuthListResponse struct {
	Files []pluginapi.HostAuthFileEntry `json:"files"`
}

// hostAuthGetRequest addresses a credential by its runtime index (not the
// scheduler ID — the two are distinct).
type hostAuthGetRequest struct {
	AuthIndex string `json:"auth_index"`
}

// hostAuthGetResponse mirrors the host's rpcHostAuthGetResponse. JSON carries
// the raw credential file bytes, which for Claude OAuth include access_token.
type hostAuthGetResponse struct {
	AuthIndex string          `json:"auth_index"`
	Name      string          `json:"name,omitempty"`
	Path      string          `json:"path,omitempty"`
	JSON      json.RawMessage `json:"json"`
}

// claudeAccount is a Claude credential with enough metadata to anchor.
type claudeAccount struct {
	ID          string
	AuthIndex   string
	Label       string
	Email       string
	Account     string
	AccountType string
	Status      string
	Disabled    bool
	Unavailable bool
	// AccessToken is present only when host.auth.get succeeded and the file
	// carries an OAuth token. Used by the optional usage probe.
	AccessToken string
}

// listClaudeAccounts enumerates Claude credentials via host.auth.list and
// augments them with host.auth.get (for label/email/token). Failures on
// runtime-only credentials degrade gracefully: the account stays visible for
// passive observation but is skipped for active probes.
func listClaudeAccounts() []claudeAccount {
	var listResp hostAuthListResponse
	if errCall := callHost(pluginabi.MethodHostAuthList, hostAuthListRequest{}, &listResp); errCall != nil {
		logWarn("host.auth.list failed", map[string]any{"error": errCall.Error()})
		return nil
	}
	accounts := make([]claudeAccount, 0, len(listResp.Files))
	for _, file := range listResp.Files {
		if !strings.EqualFold(file.Provider, "claude") {
			continue
		}
		entry := claudeAccount{
			ID:          file.ID,
			AuthIndex:   file.AuthIndex,
			Label:       file.Label,
			Email:       file.Email,
			Account:     file.Account,
			AccountType: file.AccountType,
			Status:      file.Status,
			Disabled:    file.Disabled,
			Unavailable: file.Unavailable,
		}
		if entry.Label == "" {
			if entry.Email != "" {
				entry.Label = entry.Email
			} else if entry.ID != "" {
				entry.Label = entry.ID
			}
		}
		// Optional token fetch for the oauth usage probe. Failures are silent;
		// the account is still usable for passive observation and anchoring.
		if entry.AuthIndex != "" {
			var getResp hostAuthGetResponse
			if errCall := callHost(pluginabi.MethodHostAuthGet, hostAuthGetRequest{AuthIndex: entry.AuthIndex}, &getResp); errCall == nil {
				var raw map[string]any
				if errUnmarshal := json.Unmarshal(getResp.JSON, &raw); errUnmarshal == nil {
					if token, ok := raw["access_token"].(string); ok {
						entry.AccessToken = token
					}
					if rawEmail, ok := raw["email"].(string); ok && entry.Email == "" {
						entry.Email = rawEmail
						entry.Label = rawEmail
					}
				}
			}
		}
		accounts = append(accounts, entry)
	}
	return accounts
}

// isOAuthToken reports whether a credential token is a Claude OAuth token
// (sk-ant-oat), as opposed to an API key. Only OAuth tokens carry subscription
// quota windows that anchoring applies to.
func isOAuthToken(token string) bool {
	return strings.Contains(token, "sk-ant-oat")
}
