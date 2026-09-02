package main

import (
	"encoding/json"
	"fmt"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// lifecycleRequest carries the plugin configuration sent in plugin.register
// and plugin.reconfigure. config_yaml arrives as base64 of YAML bytes.
type lifecycleRequest struct {
	ConfigYAML    []byte `json:"config_yaml"`
	SchemaVersion uint32 `json:"schema_version"`
}

// registration is the plugin response shape the host validates. Note the JSON
// keys are PascalCase because pluginapi.Metadata has no struct tags.
type registration struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Metadata      pluginapi.Metadata       `json:"metadata"`
	Capabilities  registrationCapabilities `json:"capabilities"`
}

type registrationCapabilities struct {
	UsagePlugin   bool `json:"usage_plugin"`
	Scheduler     bool `json:"scheduler"`
	ManagementAPI bool `json:"management_api"`
}

// handleRegister processes the first plugin.register call: parses config,
// decides capabilities, and starts the background loop once.
func handleRegister(raw []byte) ([]byte, error) {
	return handleLifecycle(raw, true)
}

// handleReconfigure processes plugin.reconfigure after config changes. The
// host routes already-registered plugins here instead of plugin.register.
func handleReconfigure(raw []byte) ([]byte, error) {
	// Do not start a second background loop; ensureBackgroundLoop is once-guarded.
	return handleLifecycle(raw, false)
}

func handleLifecycle(raw []byte, isRegister bool) ([]byte, error) {
	var req lifecycleRequest
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			return nil, fmt.Errorf("decode lifecycle request: %w", errUnmarshal)
		}
	}
	cfg, errParse := parseConfig(req.ConfigYAML)
	if errParse != nil {
		return nil, fmt.Errorf("parse plugin config: %w", errParse)
	}
	configStore.Store(cfg)

	schema := req.SchemaVersion
	if schema == 0 || schema > pluginabi.SchemaVersion {
		schema = pluginabi.SchemaVersion
	}

	if isRegister {
		ensureBackgroundLoop()
	}

	return okResult(registration{
		SchemaVersion: schema,
		Metadata: pluginapi.Metadata{
			Name:             "claude-window-anchor",
			Version:          pluginVersion,
			Author:           pluginAuthor,
			GitHubRepository: pluginRepository,
			ConfigFields:     []pluginapi.ConfigField{},
		},
		Capabilities: registrationCapabilities{
			UsagePlugin:   true,
			Scheduler:     shouldClaimScheduler(cfg),
			ManagementAPI: true,
		},
	})
}

// handleQuiesce acknowledges a quiesce request. The host tolerates
// unknown_method for this optional RPC, so an empty success is fine.
func handleQuiesce(raw []byte) ([]byte, error) {
	return okResult(struct{}{})
}
