# cpa-plugin-claude-window-anchor

A CPA (CLIProxyAPI) native C ABI plugin that **anchors Claude's 5-hour rolling
quota window** to fixed local times.

Claude Pro/Max usage windows are 5-hour rolling windows that start at the
first request of each window. Without anchoring, the boundaries drift with
your usage pattern, so a heavy session can land at the tail of a window.
This plugin pins the boundary to your schedule (e.g. `06:30 / 11:30 / 16:30
Asia/Shanghai`) by sending a minimal request *right after the real window
resets*, making the *next* window begin at your chosen time.

> This is *not* a cron that fires a request at a fixed time. It first
> discovers the real `resets_at`, and only anchors once the window has
> actually closed — otherwise the request would land inside the still-open
> old window and waste quota for nothing.

## How it works

```
anchor slot 16:30 arrives
        |
        v
read real resets_at (from Claude request headers via host usage hook)
        |
        +-- resets_at unknown      -> anchor at 16:30 (best effort)
        +-- resets_at <= 16:30     -> anchor at 16:30 (window already over)
        +-- resets_at >  16:30     -> wait for resets_at + grace
        |
        v
host.model.execute (minimal request, max_tokens=1)
        |
        v
new window starts ≈ anchor time  ->  next resets_at ≈ anchor + 5h
```

Key design points:

- **Passive quota sensing via `usage_plugin` capability.** Every Claude
  request — organic traffic *and* our own anchors — reports raw upstream
  headers with `Anthropic-Ratelimit-Unified-Reset`, attributed per account
  (`AuthID`). This is the only reliable source: `host.model.execute` response
  headers are hidden behind `passthrough-headers` (default off), and a
  plugin's own response interceptor is skipped for its own calls.
- **Subscription quota, not API billing.** Anchors go through
  `host.model.execute`, so CPA's Claude executor applies the full Claude Code
  CLI fingerprint for `sk-ant-oat` credentials — Anthropic counts them as
  Claude Code usage, never API extra usage. Never bypass the executor.
- **Multi-account pinning.** `host.model.execute` has no `auth_id` field, so
  the plugin carries the target in a custom header and its own Scheduler
  capability honours it (double-guarded by the host's internal-source
  metadata marker). Organic traffic always falls through to the built-in
  scheduler.
- **Idempotent ledger.** Each slot has a stable window key; a restart
  (optionally with `state-file`) or a missed slot within `catch-up-window`
  re-fires at most once.
- **Abuse-safe.** At most 3 anchors per account per day, 1 output token each,
  natural jitter, and a full Claude Code fingerprint.

## Requirements

- CPA v7.2.x or newer with plugin support (`plugins.enabled: true`)
- Claude **OAuth** credentials (`sk-ant-oat` tokens). API-key credentials do
  not have subscription quota windows.

## Installation

### Plugin store (recommended)

Add the registry to `config.yaml` (the built-in official registry is always
included, so this only appends):

```yaml
plugins:
  enabled: true            # global switch, default is false — required
  dir: "plugins"
  store-sources:
    - "https://raw.githubusercontent.com/Bahamutzd/cpa-plugin-claude-window-anchor/main/registry.json"
```

Then, in the CPA management center: **Plugin Store → install
`claude-window-anchor`**. The host downloads the platform zip from the latest
GitHub release, verifies the sha256 from `checksums.txt`, extracts it to
`plugins/<goos>/<goarch>/claude-window-anchor-v<version>.<ext>`, sets
`plugins.configs.claude-window-anchor.enabled: true` automatically and hot-loads
it (no restart needed).

> **musl (Alpine) containers:** the plugin store has no glibc/musl distinction —
> there is only one `linux/amd64` artifact, built against glibc 2.31. On Alpine
> you must install the musl build manually (see below) instead of using the store.

### Manual

Place the artifact for your platform in the plugin directory. The file name
is the plugin ID: it must be exactly `claude-window-anchor.so` (or `.dll` on
Windows).

```text
plugins/linux/amd64/claude-window-anchor.so    # glibc
plugins/linux/arm64/claude-window-anchor.so
plugins/linux/amd64/claude-window-anchor-musl.so  # Alpine (musl) — build with ./build.sh musl
plugins/windows/amd64/claude-window-anchor.dll
```

> **glibc vs musl:** Alpine-based images (common on Zeabur) can only load
> musl builds. Ship both and pick the right one.

### Configuration

See `config.example.yaml`. Minimal example:

```yaml
plugins:
  enabled: true
  configs:
    claude-window-anchor:
      enabled: true
      timezone: "Asia/Shanghai"
      anchors: ["06:30", "11:30", "16:30"]
```

## Management dashboard

Once registered, the plugin appears in the CPA management center menu as
**Claude Window Anchor** and is also directly accessible at:

```text
/v0/resource/plugins/claude-window-anchor/dashboard
```

It shows per-account 5h reset, weekly reset, last anchor status and next
scheduled anchor. The dashboard is read-only in a plain browser; triggering an
immediate anchor is available via the management API (requires the management
key):

```text
POST /v0/management/claude-window-anchor/anchor-now
POST /v0/management/claude-window-anchor/anchor-now?account=<id>
```

## Build

cgo `-buildmode=c-shared` needs a target C toolchain. `build.sh` uses
`zig cc` (single download, works from any host):

```bash
# linux amd64 + arm64 + windows amd64 (glibc/mingw)
./build.sh

# also musl (Alpine) variants
./build.sh musl

# zip the built .so/.dll into plugin-store release packages + checksums.txt
./build.sh package
```

`./build.sh package` produces the artifacts `release.yml` uploads: one zip per
platform (dynamic library at the zip root, named exactly
`claude-window-anchor.<ext>`) plus a `checksums.txt` in `sha256sum` format.

For release artifacts, prefer Docker buildx with a native toolchain per
platform for maximum fidelity.

## Verify (without waiting 5 hours)

1. `dry-run: true` + `anchors` set to `now+2min` + `poll-interval: 10s` —
   watch the logs decide without sending anything.
2. Check the dashboard: after one real Claude request, `resets_at` appears.
   This is the passive sensor; it costs nothing.
3. Turn off `dry-run` for one anchor, then compare `resets_at` before and
   after: a successful anchor yields `resets_at ≈ anchor_time + 5h`.

## Caveats

- **Scheduler exclusivity.** The host grants the single Scheduler slot to the
  first plugin that claims it. `scheduler.mode: auto` (default) only claims it
  when more than one account is tracked; set `never` to avoid conflicts with
  other scheduler plugins.
- **Windows DLL replacement** requires stopping CPA (the loaded DLL cannot be
  overwritten while running).
- **OAuth usage probe** (`oauth-usage-probe: true`) is best-effort and may be
  blocked by Cloudflare; the passive sensor is authoritative.
- Keep the CPA dependency pinned (`go mod tidy` against the same CPA version
  you run) and bump `pluginVersion` on release.
