package main

import (
	"testing"
	"time"
)

func TestDueSlot_NoKnownReset_FiresAtAnchor(t *testing.T) {
	cfg := &Config{location: mustLoc(t, "Asia/Shanghai"), catchUpDuration: 45 * time.Minute}
	account := anchoredAccount{ID: "loop-noknownreset_firesatanchor@example.com", Provider: providerClaude}
	anchors := []anchorTime{{Hour: 16, Minute: 30}}
	loc := mustLoc(t, "Asia/Shanghai")
	// Just before the anchor: not yet actionable (the slot fires at 16:30).
	early := time.Date(2026, 9, 2, 16, 29, 30, 0, loc)
	if _, due := dueSlot(cfg, account, anchors, early); due {
		t.Fatal("must not fire before the anchor wall-clock time")
	}
	// Exactly at the anchor: fire.
	atAnchor := time.Date(2026, 9, 2, 16, 30, 10, 0, loc)
	slotResult, due := dueSlot(cfg, account, anchors, atAnchor)
	if !due {
		t.Fatal("expected due at anchor time")
	}
	if slotResult.anchorTime.Hour() != 16 {
		t.Fatalf("anchor time = %v", slotResult.anchorTime)
	}
}

func TestDueSlot_AlreadyAnchoredSameKey_NoRepeat(t *testing.T) {
	cfg := &Config{location: mustLoc(t, "Asia/Shanghai"), catchUpDuration: 45 * time.Minute}
	account := anchoredAccount{ID: "loop-alreadyanchoredsamekey_norepeat@example.com", Provider: providerClaude}
	anchors := []anchorTime{{Hour: 16, Minute: 30}}
	now := time.Date(2026, 9, 2, 16, 30, 30, 0, mustLoc(t, "Asia/Shanghai"))
	key := time.Date(2026, 9, 2, 16, 30, 0, 0, mustLoc(t, "Asia/Shanghai")).Format(windowKeyFormat)

	markAnchored(account.ID, key, now)
	if _, due := dueSlot(cfg, account, anchors, now); due {
		t.Fatal("same slot must not re-anchor")
	}
}

func TestDueSlot_DeferredToResetPlusGrace_Waits(t *testing.T) {
	cfg := &Config{location: mustLoc(t, "Asia/Shanghai"), catchUpDuration: 45 * time.Minute,
		graceDuration: 90 * time.Second, maxDeferDuration: time.Hour}
	account := anchoredAccount{ID: "loop-deferredtoresetplusgrace_waits@example.com", Provider: providerClaude}
	anchors := []anchorTime{{Hour: 16, Minute: 30}}
	loc := mustLoc(t, "Asia/Shanghai")
	now := time.Date(2026, 9, 2, 16, 30, 0, 0, loc)
	// Window ends at 16:45, so fire time is 16:46:30. At 16:30 it is not due.
	resetsAt := time.Date(2026, 9, 2, 16, 45, 0, 0, loc)
	observeReset(account.ID, resetsAt, "allowed")

	if _, due := dueSlot(cfg, account, anchors, now); due {
		t.Fatal("should not fire before reset+grace arrives")
	}
	// At 16:47 it should be due.
	later := time.Date(2026, 9, 2, 16, 47, 0, 0, loc)
	if _, due := dueSlot(cfg, account, anchors, later); !due {
		t.Fatal("should fire after reset+grace")
	}
}

func TestDueSlot_CatchUpAfterRestart(t *testing.T) {
	cfg := &Config{location: mustLoc(t, "Asia/Shanghai"), catchUpDuration: 45 * time.Minute}
	account := anchoredAccount{ID: "loop-catchupafterrestart@example.com", Provider: providerClaude}
	anchors := []anchorTime{{Hour: 16, Minute: 30}}
	loc := mustLoc(t, "Asia/Shanghai")
	// Restart at 16:40 — 16:30 slot missed by 10 minutes, within catch-up.
	now := time.Date(2026, 9, 2, 16, 40, 0, 0, loc)

	slotResult, due := dueSlot(cfg, account, anchors, now)
	if !due {
		t.Fatal("expected catch-up due")
	}
	if slotResult.anchorTime.Hour() != 16 {
		t.Fatalf("catch-up anchor time = %v", slotResult.anchorTime)
	}
}

func TestDueSlot_CatchUpTooLate(t *testing.T) {
	cfg := &Config{location: mustLoc(t, "Asia/Shanghai"), catchUpDuration: 45 * time.Minute}
	account := anchoredAccount{ID: "loop-catchuptoolate@example.com", Provider: providerClaude}
	anchors := []anchorTime{{Hour: 16, Minute: 30}}
	loc := mustLoc(t, "Asia/Shanghai")
	// 3 hours after the missed slot: too late, no catch-up.
	now := time.Date(2026, 9, 2, 19, 30, 0, 0, loc)

	if _, due := dueSlot(cfg, account, anchors, now); due {
		t.Fatal("should not catch up after 3 hours")
	}
}

func TestDueSlot_BackoffScopedToFailedSlot(t *testing.T) {
	cfg := &Config{location: mustLoc(t, "Asia/Shanghai"), catchUpDuration: 45 * time.Minute,
		graceDuration: 90 * time.Second, maxDeferDuration: time.Hour}
	account := anchoredAccount{ID: "loop-backoffscopedtofailedslot@example.com", Provider: providerClaude}
	anchors := []anchorTime{{Hour: 16, Minute: 30}}
	loc := mustLoc(t, "Asia/Shanghai")
	now := time.Date(2026, 9, 2, 16, 30, 0, 0, loc)
	failedKey := now.Format(windowKeyFormat)
	// Simulate a failed attempt: the reward is in backoff with a controllable
	// retry window set relative to the simulated now (recordAnchorFailure uses
	// the real clock, which is not the test clock).
	ledger.mu.Lock()
	ledger.byID[account.ID] = &accountState{
		AuthID: account.ID, RetrySlot: failedKey, NextRetryAt: now.Add(2 * time.Minute),
	}
	ledger.mu.Unlock()

	if _, due := dueSlot(cfg, account, anchors, now); due {
		t.Fatal("backoff should suppress retry for the failed slot")
	}
	// Different anchor time = different slot key -> backoff must NOT apply.
	// The new slot fires at its own anchor (17:30); by 17:31 it is actionable.
	anchors2 := []anchorTime{{Hour: 17, Minute: 30}}
	later := time.Date(2026, 9, 2, 17, 31, 0, 0, loc)
	if _, due := dueSlot(cfg, account, anchors2, later); !due {
		t.Fatal("a new slot must not be suppressed by old-slot backoff")
	}
}
