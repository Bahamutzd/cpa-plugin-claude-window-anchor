package main

import (
	"encoding/json"
	"fmt"
)

// pluginABIVersion mirrors sdk/pluginabi.ABIVersion on the host side. The
// host checks this for exact equality, so bump it only in lockstep with the
// CPA ABI contract.
const pluginABIVersion = 1

// envelope is the RPC wire envelope exchanged with the CPA plugin host. The
// host wraps every method call in {ok, result|error}; plugins must respond in
// the same shape.
type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *envelopeError  `json:"error,omitempty"`
}

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// okResult marshals a success envelope around the given result value.
func okResult(result any) ([]byte, error) {
	raw, errMarshal := json.Marshal(result)
	if errMarshal != nil {
		return nil, fmt.Errorf("marshal ok result: %w", errMarshal)
	}
	return json.Marshal(envelope{OK: true, Result: raw})
}

// okEnvelope marshals a success envelope with a pre-encoded result payload.
func okEnvelope(result json.RawMessage) ([]byte, error) {
	if len(result) == 0 {
		result = json.RawMessage("{}")
	}
	return json.Marshal(envelope{OK: true, Result: result})
}

// errResult marshals an error envelope with the given code and message.
func errResult(code, message string) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

// errorEnvelope returns an error envelope for callback-style handlers.
func errorEnvelope(code, message string) ([]byte, error) {
	return errResult(code, message), nil
}
