package main

import "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

// configFields declares the plugin-owned settings the management console
// renders under "配置字段". The console maps each Name to a top-level key
// under plugins.configs.claude-window-anchor and edits it by type:
// boolean -> switch, enum -> select, array/object -> JSON textarea,
// everything else -> text input. Nested settings therefore have to be
// declared as one object field rather than dotted paths.
func configFields() []pluginapi.ConfigField {
	return []pluginapi.ConfigField{
		{
			Name:        "anchors",
			Type:        pluginapi.ConfigFieldTypeArray,
			Description: `Anchor slots as HH:MM in the timezone below, e.g. ["06:30","11:30","16:30"]. Applies to every provider that has no anchors of its own.`,
		},
		{
			Name:        "timezone",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "IANA timezone for all anchor times, e.g. Asia/Shanghai. The database is embedded, so slim containers work without extra packages.",
		},
		{
			Name:        "providers",
			Type:        pluginapi.ConfigFieldTypeObject,
			Description: `Per-provider overrides, e.g. {"claude":{"enabled":true},"codex":{"enabled":true,"anchors":["07:00","12:00"],"model":"gpt-5.4-mini"}}. Claude is on by default; Codex must be enabled here.`,
		},
		{
			Name:        "dry-run",
			Type:        pluginapi.ConfigFieldTypeBoolean,
			Description: "Log every anchor decision but never send a request. Keep this on until the schedule looks right.",
		},
		{
			Name:        "model",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "Claude anchor request model. Use providers.codex.model for Codex.",
		},
		{
			Name:        "grace-period",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "How long to wait after the observed reset before anchoring, e.g. 90s. Avoids racing the server-side window boundary.",
		},
		{
			Name:        "max-deferral",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "Skip the slot when the real window would end more than this after the anchor, e.g. 60m. Prevents wasting quota.",
		},
		{
			Name:        "catch-up-window",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "Re-fire a slot missed by a restart or suspend within this window, e.g. 45m.",
		},
		{
			Name:        "poll-interval",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "How often the schedule is re-evaluated, e.g. 30s.",
		},
		{
			Name:        "account-stagger",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "Delay between accounts so anchors never fire as one burst, e.g. 5s.",
		},
		{
			Name:        "codex-window-minutes",
			Type:        pluginapi.ConfigFieldTypeInteger,
			Description: "Which Codex rate-limit window to anchor, matched by declared length in minutes (default 300). Codex reports several windows per response and the one named \"primary\" is often the weekly limit, so it is selected by length rather than name.",
		},
		{
			Name:        "probe-on-start",
			Type:        pluginapi.ConfigFieldTypeBoolean,
			Description: "Send one minimal request per account at plugin start to learn the current reset time. Costs a few tokens per account per restart.",
		},
		{
			Name:        "state-file",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "Path for the anchor ledger, persisted across restarts to prevent double-anchoring. Empty keeps it in memory only.",
		},
	}
}
