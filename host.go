package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type hostModelExecutionRequest struct {
	pluginapi.HostModelExecutionRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type rpcStreamEmitRequest struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
	Error    string `json:"error,omitempty"`
}

type rpcStreamCloseRequest struct {
	StreamID string `json:"stream_id"`
	Error    string `json:"error,omitempty"`
}

type rpcHostLogRequest struct {
	HostCallbackID string         `json:"host_callback_id,omitempty"`
	Level          string         `json:"level,omitempty"`
	Message        string         `json:"message,omitempty"`
	Fields         map[string]any `json:"fields,omitempty"`
}

func callHost(method string, request any) ([]byte, error) {
	if method == "" {
		return nil, fmt.Errorf("host method is required")
	}
	rawReq, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	return callHostRPC(method, rawReq)
}

func emitPluginStreamChunk(streamID string, payload []byte) error {
	_, err := callHost(pluginabi.MethodHostStreamEmit, rpcStreamEmitRequest{
		StreamID: streamID,
		Payload:  payload,
	})
	return err
}

func closePluginStream(streamID, errMsg string) {
	if strings.TrimSpace(streamID) == "" {
		return
	}
	_, _ = callHost(pluginabi.MethodHostStreamClose, rpcStreamCloseRequest{
		StreamID: streamID,
		Error:    strings.TrimSpace(errMsg),
	})
}

func hostLog(level, hostCallbackID, message string, fields map[string]any) {
	if strings.TrimSpace(message) == "" {
		return
	}
	_, _ = callHost(pluginabi.MethodHostLog, rpcHostLogRequest{
		HostCallbackID: hostCallbackID,
		Level:          strings.TrimSpace(level),
		Message:        message,
		Fields:         fields,
	})
}

func hostDebugLog(hostCallbackID, message string, fields map[string]any) {
	hostLog("debug", hostCallbackID, message, fields)
}

func openHostModelStream(req pluginapi.ExecutorRequest, hostCallbackID string, body []byte, stream bool) (pluginapi.HostModelStreamResponse, error) {
	entryProtocol, exitProtocol := hostProtocols(req)
	raw, err := callHost(pluginabi.MethodHostModelExecuteStream, hostModelExecutionRequest{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{
			EntryProtocol: entryProtocol,
			ExitProtocol:  exitProtocol,
			Model:         strings.TrimSpace(req.Model),
			Stream:        stream,
			Body:          body,
			Headers:       cloneHeaders(req.Headers),
			Query:         cloneValues(req.Query),
			Alt:           req.Alt,
		},
		HostCallbackID: hostCallbackID,
	})
	if err != nil {
		return pluginapi.HostModelStreamResponse{}, err
	}
	var resp pluginapi.HostModelStreamResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return pluginapi.HostModelStreamResponse{}, err
	}
	return resp, nil
}

func executeHostModel(req pluginapi.ExecutorRequest, hostCallbackID string, body []byte) (pluginapi.HostModelExecutionResponse, error) {
	entryProtocol, exitProtocol := hostProtocols(req)
	raw, err := callHost(pluginabi.MethodHostModelExecute, hostModelExecutionRequest{
		HostModelExecutionRequest: pluginapi.HostModelExecutionRequest{
			EntryProtocol: entryProtocol,
			ExitProtocol:  exitProtocol,
			Model:         strings.TrimSpace(req.Model),
			Stream:        false,
			Body:          body,
			Headers:       cloneHeaders(req.Headers),
			Query:         cloneValues(req.Query),
			Alt:           req.Alt,
		},
		HostCallbackID: hostCallbackID,
	})
	if err != nil {
		return pluginapi.HostModelExecutionResponse{}, err
	}
	var resp pluginapi.HostModelExecutionResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return pluginapi.HostModelExecutionResponse{}, err
	}
	return resp, nil
}

func closeHostModelStream(streamID string) error {
	if strings.TrimSpace(streamID) == "" {
		return nil
	}
	_, err := callHost(pluginabi.MethodHostModelStreamClose, pluginapi.HostModelStreamCloseRequest{StreamID: streamID})
	return err
}

func readHostModelStream(streamID string) (pluginapi.HostModelStreamReadResponse, error) {
	raw, err := callHost(pluginabi.MethodHostModelStreamRead, pluginapi.HostModelStreamReadRequest{StreamID: streamID})
	if err != nil {
		return pluginapi.HostModelStreamReadResponse{}, err
	}
	var resp pluginapi.HostModelStreamReadResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return pluginapi.HostModelStreamReadResponse{}, err
	}
	if resp.Error != "" {
		return pluginapi.HostModelStreamReadResponse{}, fmt.Errorf("%s", resp.Error)
	}
	return resp, nil
}

func delegateOnce(ctx context.Context, req pluginapi.ExecutorRequest, hostCallbackID string) ([]byte, http.Header, error) {
	_ = ctx
	body, err := executorPayloadBody(req)
	if err != nil {
		return nil, nil, err
	}
	resp, err := executeHostModel(req, hostCallbackID, body)
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, resp.Headers, fmt.Errorf("host model status %d", resp.StatusCode)
	}
	return resp.Body, resp.Headers, nil
}

func forwardHostStream(ctx context.Context, req pluginapi.ExecutorRequest, hostCallbackID, pluginStreamID string) error {
	_ = ctx
	body, err := executorPayloadBody(req)
	if err != nil {
		return err
	}
	resp, err := openHostModelStream(req, hostCallbackID, body, true)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		_ = closeHostModelStream(resp.StreamID)
		return fmt.Errorf("host model status %d", resp.StatusCode)
	}
	if strings.TrimSpace(resp.StreamID) == "" {
		return fmt.Errorf("host model stream: empty stream_id")
	}
	defer func() { _ = closeHostModelStream(resp.StreamID) }()

	for {
		chunk, err := readHostModelStream(resp.StreamID)
		if err != nil {
			return err
		}
		if len(chunk.Payload) > 0 {
			if err := emitPluginStreamChunk(pluginStreamID, cloneSlice(chunk.Payload)); err != nil {
				return err
			}
		}
		if chunk.Done {
			return nil
		}
	}
}

func executorPayloadBody(req pluginapi.ExecutorRequest) ([]byte, error) {
	if len(req.Payload) == 0 {
		return nil, fmt.Errorf("executor payload is empty")
	}
	return cloneSlice(req.Payload), nil
}

func hostProtocols(req pluginapi.ExecutorRequest) (string, string) {
	cfg := loadedConfig()
	entryProtocol := hostProtocolName(req.Format)
	if entryProtocol == "" {
		entryProtocol = hostProtocolName(req.SourceFormat)
	}
	if entryProtocol == "" {
		entryProtocol = "openai-response"
	}
	exitProtocol := hostProtocolName(cfg.ExitProtocol)
	if exitProtocol == "" {
		exitProtocol = "openai-response"
	}
	return entryProtocol, exitProtocol
}

func hostProtocolName(raw string) string {
	switch normalizeFormat(raw) {
	case "responses":
		return "openai-response"
	default:
		return normalizeFormat(raw)
	}
}

func cloneHeaders(src http.Header) http.Header {
	if len(src) == 0 {
		return nil
	}
	dst := make(http.Header, len(src))
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
	return dst
}

func cloneValues(src map[string][]string) map[string][]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string][]string, len(src))
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
	return dst
}
