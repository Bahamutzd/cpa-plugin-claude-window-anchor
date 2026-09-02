package main

import (
	"context"
	"embed"
	"net/http"
	"time"
)

//go:embed assets/dashboard.html
var dashboardFS embed.FS

// timeRFC3339 is the formatting layout used across the status payloads.
const timeRFC3339 = time.RFC3339

// timeNow is the process clock. Kept as a var for test injection.
var timeNow = func() time.Time { return time.Now() }

// contextBg returns a background context for host calls made outside a
// request scope (manual anchors from management). The host resolves a missing
// host_callback_id to context.Background() anyway, but an explicit non-nil
// context avoids nil-panic on some host callbacks.
func contextBg() context.Context { return context.Background() }

// jsonContentType returns the content-type header for JSON management
// responses.
func jsonContentType() http.Header {
	return http.Header{"Content-Type": []string{"application/json; charset=utf-8"}}
}

// renderDashboardHTML loads the embedded dashboard template. The HTML is
// static; account data is injected by the browser via the status JSON fetch,
// so no server-side interpolation can cause XSS.
func renderDashboardHTML() []byte {
	raw, errRead := dashboardFS.ReadFile("assets/dashboard.html")
	if errRead != nil {
		return []byte("<!doctype html><title>Claude Window Anchor</title><p>dashboard asset missing</p>")
	}
	return raw
}
