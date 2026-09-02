package main

// host log helpers. All logs go through host.log so they appear in the CPA
// log stream with request correlation, never on the plugin's own stdout.

import "github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"

// rpcHostLogRequest mirrors the host's rpcHostLogRequest wire shape.
type rpcHostLogRequest struct {
	HostCallbackID string         `json:"host_callback_id,omitempty"`
	Level          string         `json:"level,omitempty"`
	Message        string         `json:"message,omitempty"`
	Fields         map[string]any `json:"fields,omitempty"`
}

// logInfo logs at info level through the host.
func logInfo(message string, fields map[string]any) {
	logHost("info", message, fields)
}

// logWarn logs at warn level through the host.
func logWarn(message string, fields map[string]any) {
	logHost("warn", message, fields)
}

// logError logs at error level through the host.
func logError(message string, fields map[string]any) {
	logHost("error", message, fields)
}

// logDebug logs at debug level through the host.
func logDebug(message string, fields map[string]any) {
	logHost("debug", message, fields)
}

func logHost(level, message string, fields map[string]any) {
	_ = callHost(pluginabi.MethodHostLog, rpcHostLogRequest{
		Level:   level,
		Message: message,
		Fields:  fields,
	}, nil)
}
