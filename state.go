package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// accountState holds everything the plugin knows about one Claude account.
type accountState struct {
	// AuthID and AuthIndex are the host identifiers. Scheduler picks by ID;
	// host.auth.get addresses by index.
	AuthID    string
	AuthIndex string
	Label     string
	// Provider is the host provider identifier this account belongs to.
	Provider string `json:"provider,omitempty"`

	// ResetsAt is the latest observed five-hour window reset time, zero if
	// never observed.
	ResetsAt time.Time `json:"resets_at,omitempty"`
	// ResetsAtObservedAt is when ResetsAt was learned.
	ResetsAtObservedAt time.Time `json:"resets_at_observed_at,omitempty"`
	// Status is the latest anthropic-ratelimit-unified-status value.
	Status string `json:"status,omitempty"`
	// SevenDayReset is the weekly window reset, tracked separately (never used
	// for scheduling; only displayed).
	SevenDayReset time.Time `json:"seven_day_reset,omitempty"`
	// SevenDayStatus mirrors the 7d limit status.
	SevenDayStatus string `json:"seven_day_status,omitempty"`

	// LastAnchoredWindowKey is the slot key last successfully anchored.
	LastAnchoredWindowKey string `json:"last_anchored_window_key,omitempty"`
	// LastAnchorTime is when the last anchor request succeeded.
	LastAnchorTime time.Time `json:"last_anchor_time,omitempty"`
	// LastAnchorError carries the last anchor failure message.
	LastAnchorError string `json:"last_anchor_error,omitempty"`

	// RetryCount and NextRetryAt do the exponential backoff for one slot.
	RetryCount  int       `json:"retry_count,omitempty"`
	NextRetryAt time.Time `json:"next_retry_at,omitempty"`
	// RetrySlot is the slot key currently in backoff (empty when not in
	// backoff). It scopes NextRetryAt to a single failed slot so a new slot
	// is never accidentally suppressed.
	RetrySlot string `json:"retry_slot,omitempty"`

	// Disabled reports an explicitly disabled account override.
	Disabled bool `json:"disabled,omitempty"`

	// LastUsageError records the last oauth usage probe failure for the UI.
	LastUsageError string `json:"last_usage_error,omitempty"`
}

// stateLedger is the process-global, mutex-guarded account state store.
type stateLedger struct {
	mu    sync.RWMutex
	byID  map[string]*accountState
	byIdx map[string]*accountState
}

var ledger = &stateLedger{
	byID:  make(map[string]*accountState),
	byIdx: make(map[string]*accountState),
}

// ensureAccount returns the state entry for an account, creating it if needed.
func ensureAccount(authID, authIndex, label, provider string) *accountState {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entry, ok := ledger.byID[authID]
	if !ok {
		entry = &accountState{}
		ledger.byID[authID] = entry
	}
	if authIndex != "" {
		entry.AuthIndex = authIndex
		ledger.byIdx[authIndex] = entry
	}
	if label != "" && entry.Label == "" {
		entry.Label = label
	}
	if provider != "" {
		entry.Provider = provider
	}
	entry.AuthID = authID
	return entry
}

// accountByID returns the state for an account, or nil.
func accountByID(authID string) *accountState {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	return ledger.byID[authID]
}

// allAccountsSnapshots returns a copy of every tracked account's state.
func allAccountsSnapshots() []accountState {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	out := make([]accountState, 0, len(ledger.byID))
	for _, entry := range ledger.byID {
		out = append(out, *entry)
	}
	return out
}

// observeReset records a five-hour window reset observation.
func observeReset(authID string, resetsAt time.Time, status string) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entry, ok := ledger.byID[authID]
	if !ok {
		entry = &accountState{AuthID: authID}
		ledger.byID[authID] = entry
	}
	entry.ResetsAt = resetsAt
	entry.ResetsAtObservedAt = time.Now()
	entry.Status = status
	if entry.RetryCount != 0 {
		entry.RetryCount = 0
		entry.NextRetryAt = time.Time{}
		entry.RetrySlot = ""
	}
}

// observeSevenDayReset records the weekly window reset for display only.
func observeSevenDayReset(authID string, resetsAt time.Time, status string) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entry, ok := ledger.byID[authID]
	if !ok {
		entry = &accountState{AuthID: authID}
		ledger.byID[authID] = entry
	}
	entry.SevenDayReset = resetsAt
	entry.SevenDayStatus = status
}

// markAnchored records a successful anchor for the given slot key.
func markAnchored(authID, windowKey string, anchorTime time.Time) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entry, ok := ledger.byID[authID]
	if !ok {
		entry = &accountState{AuthID: authID}
		ledger.byID[authID] = entry
	}
	entry.LastAnchoredWindowKey = windowKey
	entry.LastAnchorTime = anchorTime
	entry.LastAnchorError = ""
	entry.RetryCount = 0
	entry.NextRetryAt = time.Time{}
	entry.RetrySlot = ""
}

// recordAnchorFailure applies exponential backoff after a failed anchor.
// It returns true when the slot should be retried later, false when the slot
// must be abandoned.
func recordAnchorFailure(authID, windowKey string, failErr error) bool {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entry, ok := ledger.byID[authID]
	if !ok {
		entry = &accountState{AuthID: authID}
		ledger.byID[authID] = entry
	}
	entry.LastAnchorError = failErr.Error()
	entry.RetryCount++
	if entry.RetryCount >= maxAnchorRetries {
		entry.RetryCount = 0
		entry.RetrySlot = ""
		// Abandon the slot; the next slot will start fresh.
		return false
	}
	backoff := anchorRetryBase
	for i := 1; i < entry.RetryCount; i++ {
		backoff *= 2
	}
	entry.RetrySlot = windowKey
	entry.NextRetryAt = time.Now().Add(backoff)
	return true
}

// lastAnchorFor returns the last anchored window key for an account.
func lastAnchorFor(authID string) (string, time.Time) {
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	entry := ledger.byID[authID]
	if entry == nil {
		return "", time.Time{}
	}
	return entry.LastAnchoredWindowKey, entry.LastAnchorTime
}

// setAccountDisabled records an override that disables anchoring for an account.
func setAccountDisabled(authID string, disabled bool) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entry, ok := ledger.byID[authID]
	if !ok {
		entry = &accountState{AuthID: authID}
		ledger.byID[authID] = entry
	}
	entry.Disabled = disabled
}

// setAnchorTerminalFailure records a terminal (401/403) anchor failure and
// clears the retry schedule so the loop abandons the slot.
func setAnchorTerminalFailure(authID, message string) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entry, ok := ledger.byID[authID]
	if !ok {
		entry = &accountState{AuthID: authID}
		ledger.byID[authID] = entry
	}
	entry.LastAnchorError = message
	entry.RetryCount = 0
	entry.NextRetryAt = time.Time{}
	entry.RetrySlot = ""
}

// setLastUsageError records the last oauth usage probe failure for the dashboard.
func setLastUsageError(authID, message string) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	entry, ok := ledger.byID[authID]
	if !ok {
		entry = &accountState{AuthID: authID}
		ledger.byID[authID] = entry
	}
	entry.LastUsageError = message
}

// persistStateBestEffort writes the ledger to state-file when configured.
func persistStateBestEffort() {
	cfg := configStore.Load()
	if cfg == nil || cfg.StateFile == "" {
		return
	}
	ledger.mu.RLock()
	defer ledger.mu.RUnlock()
	data, errMarshal := json.Marshal(ledger.byID)
	if errMarshal != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(cfg.StateFile), 0o700)
	if errWrite := os.WriteFile(cfg.StateFile, data, 0o600); errWrite != nil {
		logDebug("state persist failed", map[string]any{"error": errWrite.Error()})
	}
}

// loadStateBestEffort reads the ledger back from state-file if configured.
func loadStateBestEffort() {
	cfg := configStore.Load()
	if cfg == nil || cfg.StateFile == "" {
		return
	}
	data, errRead := os.ReadFile(cfg.StateFile)
	if errRead != nil {
		return
	}
	var loaded map[string]*accountState
	if errUnmarshal := json.Unmarshal(data, &loaded); errUnmarshal != nil {
		return
	}
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	for id, entry := range loaded {
		if entry == nil || id == "" {
			continue
		}
		entry.AuthID = id
		ledger.byID[id] = entry
	}
}
