package main

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Codex quota headers are namespaced per rate-limit family, not fixed like
// Anthropic's. The host (internal/runtime/executor/helps/codex_quota.go)
// normalizes both the websocket event and the HTTP response into three
// shapes:
//
//	X-Codex-Primary-*                        base limit
//	X-Codex-Secondary-*                      base limit, second window
//	X-Codex-Additional-<Limit>-Primary-*     per-model limit (e.g. Spark)
//	X-Codex-<short>-Primary-*                per-model limit, HTTP spelling
//	X-Codex-Code-Review-Primary-*            code review limit
//
// Critically, "primary" does NOT mean the five-hour window. Observed payloads
// carry window_minutes=10080 (7 days) on the base primary window while the
// 300-minute window lives under an additional limit. Selecting by name would
// anchor against the weekly window; windows must be matched by
// window_minutes instead.
const (
	codexHeaderPrefix    = "X-Codex-"
	codexSuffixWindowMin = "-Window-Minutes"
	codexSuffixResetAt   = "-Reset-At"
	codexSuffixResetIn   = "-Reset-After-Seconds"
	codexSuffixUsedPct   = "-Used-Percent"
	codexSuffixReached   = "-Limit-Reached"
)

// codexWindow is one parsed rate-limit window from the X-Codex-* headers.
type codexWindow struct {
	// Namespace is the header prefix identifying this window, e.g.
	// "X-Codex-Primary-" or "X-Codex-Additional-GPT-5.3-Codex-Spark-Primary-".
	Namespace string
	// WindowMinutes is the declared window length.
	WindowMinutes int
	// ResetsAt is the absolute window end. Derived from Reset-At when present,
	// otherwise from Reset-After-Seconds relative to the observation time.
	ResetsAt time.Time
	// UsedPercent is the consumed fraction, -1 when absent.
	UsedPercent float64
	// LimitReached reports an exhausted window.
	LimitReached bool
}

// observeCodexUsage picks the window whose declared length is closest to the
// configured target (5 hours by default) and reports it as the rolling window
// to anchor. The longest window seen is reported as the secondary (weekly)
// figure for display.
func observeCodexUsage(cfg *Config, headers http.Header) (windowObservation, bool) {
	windows := parseCodexWindows(headers, timeNow())
	if len(windows) == 0 {
		return windowObservation{}, false
	}

	target := codexTargetWindowMinutes
	tolerance := codexWindowToleranceMinutes
	if cfg != nil {
		if cfg.CodexWindowMinutes > 0 {
			target = cfg.CodexWindowMinutes
		}
		if cfg.CodexWindowTolerance > 0 {
			tolerance = cfg.CodexWindowTolerance
		}
	}

	var (
		best       *codexWindow
		bestDelta  int
		longest    *codexWindow
		longestMin int
	)
	for i := range windows {
		window := &windows[i]
		if window.WindowMinutes > longestMin {
			longestMin = window.WindowMinutes
			longest = window
		}
		delta := window.WindowMinutes - target
		if delta < 0 {
			delta = -delta
		}
		if delta > tolerance {
			continue
		}
		// Prefer the closest match; break ties toward the earliest reset so a
		// per-model limit that resets sooner still anchors on time.
		if best == nil || delta < bestDelta ||
			(delta == bestDelta && window.ResetsAt.Before(best.ResetsAt)) {
			best = window
			bestDelta = delta
		}
	}

	out := windowObservation{}
	if longest != nil && longest != best && !longest.ResetsAt.IsZero() {
		out.SecondaryReset = longest.ResetsAt
		out.SecondaryStatus = codexStatus(longest)
	}
	if best == nil {
		// No window matches the target length. Report the weekly figure so the
		// dashboard still shows something, but never anchor off it: returning a
		// zero ResetsAt keeps the scheduler in "no signal" mode.
		return out, !out.SecondaryReset.IsZero()
	}
	out.ResetsAt = best.ResetsAt
	out.Status = codexStatus(best)
	return out, !out.ResetsAt.IsZero() || !out.SecondaryReset.IsZero()
}

// Default target window: Codex's short rolling limit is 300 minutes. The
// tolerance absorbs upstream rounding without ever matching the 10080-minute
// weekly window.
const (
	codexTargetWindowMinutes    = 300
	codexWindowToleranceMinutes = 60
)

// parseCodexWindows groups the flat X-Codex-* header set into windows by
// their shared namespace prefix. observedAt anchors Reset-After-Seconds.
func parseCodexWindows(headers http.Header, observedAt time.Time) []codexWindow {
	if len(headers) == 0 {
		return nil
	}
	byNamespace := make(map[string]*codexWindow)
	namespaceOf := func(name, suffix string) (string, bool) {
		if !strings.HasPrefix(name, codexHeaderPrefix) || !strings.HasSuffix(name, suffix) {
			return "", false
		}
		return strings.TrimSuffix(name, suffix), true
	}
	ensure := func(namespace string) *codexWindow {
		window, ok := byNamespace[namespace]
		if !ok {
			window = &codexWindow{Namespace: namespace, UsedPercent: -1}
			byNamespace[namespace] = window
		}
		return window
	}

	for key, values := range headers {
		if len(values) == 0 {
			continue
		}
		name := http.CanonicalHeaderKey(strings.TrimSpace(key))
		value := strings.TrimSpace(values[len(values)-1])
		if value == "" {
			continue
		}
		switch {
		case matchCodexSuffix(name, codexSuffixWindowMin):
			namespace, _ := namespaceOf(name, codexSuffixWindowMin)
			minutes, errParse := strconv.Atoi(value)
			if errParse != nil || minutes <= 0 {
				continue
			}
			ensure(namespace).WindowMinutes = minutes
		case matchCodexSuffix(name, codexSuffixResetAt):
			namespace, _ := namespaceOf(name, codexSuffixResetAt)
			epoch, errParse := strconv.ParseInt(value, 10, 64)
			if errParse != nil || epoch <= 0 {
				continue
			}
			// Reset-At is absolute and outranks the relative form.
			ensure(namespace).ResetsAt = unixTime(epoch)
		case matchCodexSuffix(name, codexSuffixResetIn):
			namespace, _ := namespaceOf(name, codexSuffixResetIn)
			seconds, errParse := strconv.ParseInt(value, 10, 64)
			if errParse != nil || seconds < 0 {
				continue
			}
			window := ensure(namespace)
			if window.ResetsAt.IsZero() {
				window.ResetsAt = observedAt.Add(time.Duration(seconds) * time.Second)
			}
		case matchCodexSuffix(name, codexSuffixUsedPct):
			namespace, _ := namespaceOf(name, codexSuffixUsedPct)
			used, errParse := strconv.ParseFloat(value, 64)
			if errParse != nil {
				continue
			}
			ensure(namespace).UsedPercent = used
		case matchCodexSuffix(name, codexSuffixReached):
			namespace, _ := namespaceOf(name, codexSuffixReached)
			ensure(namespace).LimitReached = strings.EqualFold(value, "true")
		}
	}

	out := make([]codexWindow, 0, len(byNamespace))
	for _, window := range byNamespace {
		// A window without a length cannot be matched against the target, and
		// one without a reset time cannot drive scheduling.
		if window.WindowMinutes <= 0 || window.ResetsAt.IsZero() {
			continue
		}
		out = append(out, *window)
	}
	return out
}

// matchCodexSuffix reports whether a canonical header name belongs to the
// X-Codex namespace and carries the given field suffix. Reset-At must not
// also match Reset-After-Seconds, so the check is on the full suffix.
func matchCodexSuffix(name, suffix string) bool {
	return strings.HasPrefix(name, codexHeaderPrefix) &&
		strings.HasSuffix(name, suffix) &&
		len(name) > len(codexHeaderPrefix)+len(suffix)
}

// codexStatus maps a window's utilization onto the same coarse status
// vocabulary the Claude headers use, so the dashboard renders both providers
// with one badge implementation.
func codexStatus(window *codexWindow) string {
	if window == nil {
		return ""
	}
	if window.LimitReached {
		return "rejected"
	}
	switch {
	case window.UsedPercent >= 90:
		return "allowed_warning"
	case window.UsedPercent >= 0:
		return "allowed"
	default:
		return ""
	}
}
