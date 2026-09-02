package main

import (
	"encoding/json"
	"strings"
)

// schedulerPickRequest mirrors pluginapi.SchedulerPickRequest on the wire.
// Note the nested Options/Candidates fields: SchedulerOptions and
// SchedulerAuthCandidate have no json tags, so the keys are PascalCase.
// This is the host verifying our chosen AuthID exists in Candidates.
type schedulerPickRequest struct {
	Plugin     metadataSummary          `json:"Plugin"`
	Provider   string                   `json:"Provider"`
	Providers  []string                 `json:"Providers"`
	Model      string                   `json:"Model"`
	Stream     bool                     `json:"Stream"`
	Options    schedulerOptions         `json:"Options"`
	Candidates []schedulerAuthCandidate `json:"Candidates"`
}

type metadataSummary struct {
	Name string `json:"Name"`
}

// schedulerOptions mirrors pluginapi.SchedulerOptions (no tags, PascalCase).
type schedulerOptions struct {
	Headers  map[string][]string `json:"Headers"`
	Metadata map[string]any      `json:"Metadata"`
}

// schedulerAuthCandidate mirrors pluginapi.SchedulerAuthCandidate (no tags,
// PascalCase). Metadata is always nil from the host — do not rely on it.
type schedulerAuthCandidate struct {
	ID         string            `json:"ID"`
	Provider   string            `json:"Provider"`
	Priority   int               `json:"Priority"`
	Status     string            `json:"Status"`
	Attributes map[string]string `json:"Attributes"`
}

// schedulerPickResponse mirrors pluginapi.SchedulerPickResponse (no tags,
// PascalCase).
type schedulerPickResponse struct {
	AuthID          string `json:"AuthID"`
	DelegateBuiltin string `json:"DelegateBuiltin"`
	Handled         bool   `json:"Handled"`
}

// internalSourceMetadataKey is the metadata key the host sets on internal
// plugin-host model executions.
const internalSourceMetadataKey = "source"

// internalSourceValue is the value the host uses to mark internal calls.
const internalSourceValue = "plugin_host_model_callback"

// handleSchedulerPick routes anchor requests to their pinned auth. It is the
// only Scheduler role this plugin plays: organic client traffic always falls
// through to the built-in scheduler (Handled=false).
//
// Two guards prevent an external client from hijacking credential selection:
//  1. The pin header must match the configured scheduler header.
//  2. The request must carry the host's internal-source metadata marker.
func handleSchedulerPick(raw []byte) ([]byte, error) {
	var req schedulerPickRequest
	if len(raw) > 0 {
		if errUnmarshal := json.Unmarshal(raw, &req); errUnmarshal != nil {
			return okResult(schedulerPickResponse{Handled: false})
		}
	}
	cfg := configStore.Load()
	if cfg == nil {
		return okResult(schedulerPickResponse{Handled: false})
	}
	if req.Options.Metadata[internalSourceMetadataKey] != internalSourceValue {
		logDebug("scheduler pick: rejecting non-internal request", nil)
		return okResult(schedulerPickResponse{Handled: false})
	}

	want := ""
	for key, values := range req.Options.Headers {
		if strings.EqualFold(strings.TrimSpace(key), strings.TrimSpace(cfg.Scheduler.Header)) {
			if len(values) > 0 {
				want = strings.TrimSpace(values[0])
			}
			break
		}
	}
	if want == "" {
		return okResult(schedulerPickResponse{Handled: false})
	}
	for _, candidate := range req.Candidates {
		if strings.TrimSpace(candidate.ID) == want {
			return okResult(schedulerPickResponse{AuthID: want, Handled: true})
		}
	}
	logWarn("scheduler pick: pin not in candidates", map[string]any{
		"pin":   want,
		"model": req.Model,
	})
	return okResult(schedulerPickResponse{Handled: false})
}
