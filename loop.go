package main

import (
	"context"
	"time"
)

// windowKey is the stable identifier for one anchor slot, e.g.
// "2026-09-02T16:30+08:00". It is derived from the wall-clock anchor time in
// the configured timezone and doubles as the idempotency ledger key.
type windowKey string

// backgroundLoop is the single long-running goroutine that drives all
// anchoring. It ticks, re-evaluates due slots, and sleeps until the next
// anchor is due. It never uses one long time.After: timers are unreliable
// across suspend/resume, so every tick recomputes the wall clock against the
// anchor schedule.
func backgroundLoop(ctx context.Context) {
	loadStateBestEffort()
	cfg := configStore.Load()
	if cfg == nil {
		logError("background loop started without config", nil)
		return
	}
	if cfg.ProbeOnStart {
		probeAllAccounts(ctx)
	}

	last := time.Now()
	poll := cfg.pollDuration
	ticker := time.NewTicker(poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			// Detect suspend/resume: wall-clock advance much larger than the
			// tick interval forces a full re-evaluation.
			if now.Sub(last) > 3*poll {
				logInfo("clock jump detected; re-evaluating schedule", map[string]any{
					"elapsed": now.Sub(last).String(),
				})
			}
			last = now
			evaluate(ctx, now)
		}
	}
}

// evaluate runs one full scheduling pass: list accounts, find due slots,
// and anchor each due account sequentially (staggered to avoid burst).
func evaluate(ctx context.Context, now time.Time) {
	cfg := configStore.Load()
	if cfg == nil || !cfg.Enabled {
		return
	}
	accounts := listAnchorAccounts()
	if len(accounts) == 0 {
		return
	}
	for i, account := range accounts {
		// Register every supported account so the dashboard lists it before
		// any traffic has produced a quota observation.
		ensureAccount(account.ID, account.AuthIndex, account.Label, account.Provider)
		if !cfg.providerEnabled(account.Provider) {
			continue
		}
		if !cfg.appliesTo(account.ID) {
			continue
		}
		st := accountByID(account.ID)
		if st != nil && st.Disabled {
			continue
		}
		// Account-level anchor overrides may disable or re-schedule.
		anchors := cfg.anchorsForAccount(account.ID, account.Provider)
		if len(anchors) == 0 {
			continue
		}
		slot, ok := dueSlot(cfg, account, anchors, now)
		if !ok {
			continue
		}
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(cfg.staggerDuration):
			}
		}
		anchorAccount(ctx, cfg, account, slot.windowKey, slot.anchorTime)
	}
}

// slot is the outcome of one account's due-check.
type slot struct {
	windowKey  windowKey
	anchorTime time.Time
}

// dueSlot decides, for one account right now, whether an anchor slot is due
// (immediately or after sleeping until the real reset boundary). It returns
// false when nothing is due for this account at this moment.
func dueSlot(cfg *Config, account anchoredAccount, anchors []anchorTime, now time.Time) (slot, bool) {
	st := accountByID(account.ID)
	var knownReset time.Time
	if st != nil {
		knownReset = st.ResetsAt
	}

	// Previous anchor slot for catch-up after restarts / suspend / resume.
	if prevAnchor, _, ok := previousAnchorOnOrBefore(now, anchors, cfg.location); ok {
		prevKey := windowKeyFor(prevAnchor)
		// A slot still in backoff must never be re-fired, not even by catch-up.
		if slotInBackoff(st, prevKey, now) {
			return slot{}, false
		}
		if prevKey != "" && st != nil && st.LastAnchoredWindowKey == prevKey {
			// already anchored this slot
		} else if catchUpDue(now, prevAnchor, cfg.catchUpDuration, knownReset) {
			return slot{windowKey: windowKey(prevKey), anchorTime: now}, true
		}
	}

	// Next anchor on or after now.
	nextAnchor, _ := nextAnchorOnOrAfter(now, anchors, cfg.location)
	if nextAnchor.IsZero() {
		return slot{}, false
	}
	nextKey := windowKeyFor(nextAnchor)
	if st != nil && st.LastAnchoredWindowKey == nextKey {
		return slot{}, false
	}
	// Retry gate: if a previous attempt for THIS slot is in backoff, respect
	// it. Backoff is scoped to the retry slot key, so a new slot is never
	// accidentally suppressed.
	if slotInBackoff(st, nextKey, now) {
		return slot{}, false
	}

	result := evaluateSlot(now, nextAnchor, knownReset, cfg.graceDuration, cfg.maxDeferDuration)
	switch result.Decision {
	case decisionAnchorNow:
		// Only fire when the computed fire time has actually arrived. The
		// loop ticks every poll interval; a deferred slot (reset+grace) will
		// become actionable on a later tick without sleeping the loop.
		if result.AnchorTime.After(now) {
			return slot{}, false
		}
		return slot{windowKey: windowKey(result.WindowKey), anchorTime: result.AnchorTime}, true
	case decisionSkipped:
		logInfo("slot skipped", map[string]any{
			"auth_id": account.ID,
			"slot":    result.WindowKey,
			"reason":  result.Reason,
		})
		return slot{}, false
	default:
		return slot{}, false
	}
}

// slotInBackoff reports whether the given slot key is currently in a backoff
// window for this account's state. Backoff is scoped to the failed slot, so a
// new slot is never accidentally suppressed.
func slotInBackoff(st *accountState, slotKey string, now time.Time) bool {
	if st == nil || slotKey == "" {
		return false
	}
	return st.RetrySlot == slotKey && st.NextRetryAt.After(now)
}

// probeAllAccounts sends one minimal request per account at startup when
// probe-on-start is enabled, purely to harvest the unified reset headers. It
// is a no-op when dry-run is on.
func probeAllAccounts(ctx context.Context) {
	cfg := configStore.Load()
	if cfg == nil || cfg.DryRun {
		return
	}
	accounts := listAnchorAccounts()
	for _, account := range accounts {
		if !cfg.providerEnabled(account.Provider) {
			continue
		}
		if !cfg.appliesTo(account.ID) {
			continue
		}
		logInfo("probe-on-start anchor", map[string]any{
			"auth_id":  account.ID,
			"provider": account.Provider,
		})
		anchorAccount(ctx, cfg, account, windowKey("probe-start"), time.Now())
		select {
		case <-ctx.Done():
			return
		case <-time.After(cfg.staggerDuration):
		}
	}
}
