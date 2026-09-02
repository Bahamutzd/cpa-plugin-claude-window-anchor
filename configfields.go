package main

import "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"

// configFields declares the plugin-owned settings the management console
// renders under "配置字段". The console maps each Name to a top-level key
// under plugins.configs.claude-window-anchor and edits it by type:
// boolean -> switch, enum -> select, array/object -> JSON textarea,
// everything else -> text input. Nested settings therefore have to be
// declared as one object field rather than dotted paths.
//
// Descriptions are user-visible in the console and stay in Chinese to match
// the plugin's own dashboard.
func configFields() []pluginapi.ConfigField {
	return []pluginapi.ConfigField{
		{
			Name:        "anchors",
			Type:        pluginapi.ConfigFieldTypeArray,
			Description: `锚点时间，格式 HH:MM，使用下方时区，例如 ["06:30","11:30","16:30"]。未单独设置锚点的服务使用这组时间。`,
		},
		{
			Name:        "timezone",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "所有锚点时间使用的 IANA 时区，例如 Asia/Shanghai。时区数据已内嵌，精简镜像无需额外安装。",
		},
		{
			Name:        "providers",
			Type:        pluginapi.ConfigFieldTypeObject,
			Description: `各服务的单独设置，例如 {"claude":{"enabled":true},"codex":{"enabled":true,"anchors":["07:00","12:00"],"model":"gpt-5.4-mini"}}。Claude 默认启用；Codex 需在此显式开启。`,
		},
		{
			Name:        "dry-run",
			Type:        pluginapi.ConfigFieldTypeBoolean,
			Description: "演练模式：记录每次锚定决策但不真正发送请求。确认排程无误前建议保持开启。",
		},
		{
			Name:        "model",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "Claude 锚定请求使用的模型。Codex 请用 providers.codex.model 设置。",
		},
		{
			Name:        "grace-period",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "观测到窗口重置后等待多久再锚定，例如 90s。避免与服务端的窗口边界抢跑。",
		},
		{
			Name:        "max-deferral",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "若真实窗口结束时间比锚点晚出这么多则跳过本次，例如 60m。防止浪费额度。",
		},
		{
			Name:        "catch-up-window",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "重启或休眠导致错过锚点时，在此时长内补发一次，例如 45m。",
		},
		{
			Name:        "poll-interval",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "重新评估排程的间隔，例如 30s。",
		},
		{
			Name:        "account-stagger",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "多个账号之间的错峰间隔，避免同时发出，例如 5s。",
		},
		{
			Name:        "codex-window-minutes",
			Type:        pluginapi.ConfigFieldTypeInteger,
			Description: "按声明时长（分钟）选择要锚定的 Codex 额度窗口，默认 300。Codex 每次响应会给出多个窗口，名为 primary 的往往是周窗口，因此按时长而非名称匹配。",
		},
		{
			Name:        "probe-on-start",
			Type:        pluginapi.ConfigFieldTypeBoolean,
			Description: "插件启动时为每个账号发送一次极小请求以获取当前重置时间。每次重启会消耗每账号几个 token。",
		},
		{
			Name:        "state-file",
			Type:        pluginapi.ConfigFieldTypeString,
			Description: "锚定记录的持久化路径，跨重启避免重复锚定。留空则仅保存在内存中。",
		},
	}
}
