package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
)

// backgroundOnce guards the single background loop instance across
// register/reconfigure cycles. The loop owns its own context and is cancelled
// from cliproxyPluginShutdown.
var (
	backgroundOnce     sync.Once
	backgroundCancel   context.CancelFunc
	backgroundWG       sync.WaitGroup
	shutdownBackground sync.Once
)

// dispatch routes a plugin RPC method to its handler. Every exported handler
// installs a recover guard: a panic inside a plugin fuses the plugin with the
// host, so we must never let one escape.
func dispatch(method string, request []byte) (raw []byte, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			raw = nil
			err = fmt.Errorf("panic in %s: %v", method, recovered)
		}
	}()

	switch method {
	case pluginabi.MethodPluginRegister:
		return handleRegister(request)
	case pluginabi.MethodPluginReconfigure:
		return handleReconfigure(request)
	case pluginabi.MethodPluginQuiesce:
		return handleQuiesce(request)

	case pluginabi.MethodUsageHandle:
		return handleUsage(request)

	case pluginabi.MethodSchedulerPick:
		return handleSchedulerPick(request)

	case pluginabi.MethodManagementRegister:
		return handleManagementRegister(request)
	case pluginabi.MethodManagementHandle:
		return handleManagementHandle(request)

	default:
		// plugin.quiesce and other optional methods may be unknown to us; the
		// host tolerates unknown_method for quiesce, so return the envelope.
		return errorEnvelope("unknown_method", "unknown method: "+method)
	}
}

// ensureBackgroundLoop starts the background anchor loop exactly once. It is a
// no-op if already running (register and reconfigure can both reach here).
func ensureBackgroundLoop() {
	backgroundOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		backgroundCancel = cancel
		backgroundWG.Add(1)
		go func() {
			defer backgroundWG.Done()
			backgroundLoop(ctx)
		}()
	})
}

// shutdownBackgroundOnce cancels the background loop and waits for it to exit.
// The host drains in-flight plugin calls before dlclose, so this wait is safe.
func shutdownBackgroundOnce() {
	shutdownBackground.Do(func() {
		if backgroundCancel != nil {
			backgroundCancel()
		}
		backgroundWG.Wait()
		persistStateBestEffort()
	})
}
