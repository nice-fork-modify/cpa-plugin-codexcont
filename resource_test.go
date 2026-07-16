package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestManagementRegistrationDeclaresExactRoutes(t *testing.T) {
	reg := pluginRegistration()
	if !reg.Capabilities.ManagementAPI {
		t.Fatal("management_api = false, want true")
	}

	raw, err := handleMethod(pluginabi.MethodManagementRegister, nil)
	if err != nil {
		t.Fatalf("handleMethod(management.register) error = %v", err)
	}
	result, err := decodeRPCResult(raw)
	if err != nil {
		t.Fatalf("decodeRPCResult() error = %v", err)
	}
	var routes managementRegistrationResponse
	if err := json.Unmarshal(result, &routes); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(routes.Routes) != 1 || routes.Routes[0].Method != "GET" || routes.Routes[0].Path != "/plugins/codexcont/stats" {
		t.Fatalf("management routes = %#v", routes.Routes)
	}
	if len(routes.Resources) != 2 || routes.Resources[0].Path != "/status" || routes.Resources[0].Menu == "" || routes.Resources[1].Path != "/stats.json" {
		t.Fatalf("resource routes = %#v", routes.Resources)
	}
}

func TestResourceStatusPageIsReadOnlyAndFetchesSiblingStats(t *testing.T) {
	resp := managementResponseForPath(t, resourceStatusPath)
	if resp.StatusCode != 200 || !strings.Contains(resp.Headers.Get("Content-Type"), "text/html") {
		t.Fatalf("status response = code:%d headers:%v", resp.StatusCode, resp.Headers)
	}
	body := string(resp.Body)
	for _, required := range []string{"CodexCont", "input.type='checkbox'", "input.disabled=true", "fetch('stats.json'", "Read-only here"} {
		if !strings.Contains(body, required) {
			t.Fatalf("status page missing %q", required)
		}
	}
	for _, forbidden := range []string{"marker_text", "encrypted_content", "request_id", "authorization"} {
		if strings.Contains(strings.ToLower(body), forbidden) {
			t.Fatalf("status page contains forbidden field %q", forbidden)
		}
	}
}

func TestResourceAndManagementStatsExposeOnlyPublicAggregateSnapshot(t *testing.T) {
	oldConfig := loadedConfig()
	oldStats := processStats
	t.Cleanup(func() {
		currentConfig.Store(oldConfig)
		processStats = oldStats
	})

	cfg, err := decodeConfig([]byte(`
model_patterns:
  - gpt-5.4
  - gpt-5.6-sol
marker_text: secret-prompt-must-not-leak
`))
	if err != nil {
		t.Fatalf("decodeConfig() error = %v", err)
	}
	currentConfig.Store(cfg)
	startedAt := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	processStats = newRuntimeStats(startedAt)
	tracker := processStats.begin("gpt-5.6-sol-preview")
	tracker.recordContinuation()
	tracker.finish(usageTotals{ReasoningTokens: 516, OutputTokens: 600, TotalTokens: 700}, true, "max_continue", nil)

	for _, path := range []string{resourceStatsPath, managementStatsPath} {
		resp := managementResponseForPath(t, path)
		if resp.StatusCode != 200 || !strings.Contains(resp.Headers.Get("Content-Type"), "application/json") {
			t.Fatalf("stats response for %s = code:%d headers:%v", path, resp.StatusCode, resp.Headers)
		}
		if strings.Contains(string(resp.Body), "secret-prompt-must-not-leak") {
			t.Fatalf("stats response for %s leaked marker_text", path)
		}
		var snapshot publicStatsResponse
		if err := json.Unmarshal(resp.Body, &snapshot); err != nil {
			t.Fatalf("json.Unmarshal(%s) error = %v", path, err)
		}
		if got := snapshot.Config.ModelPatterns; len(got) != 2 || got[0] != "gpt-5.4*" || got[1] != "gpt-5.6-sol*" {
			t.Fatalf("model_patterns for %s = %#v", path, got)
		}
		if got := snapshot.Config.SelectedModels; len(got) != 2 || got[0] != "gpt-5.4" || got[1] != "gpt-5.6-sol" {
			t.Fatalf("selected_models for %s = %#v", path, got)
		}
		if snapshot.Runtime.HandledTotal != 1 || snapshot.Runtime.ContinuedRequests != 1 || snapshot.Runtime.CompletedTotal != 1 || snapshot.Runtime.FailedTotal != 0 {
			t.Fatalf("runtime for %s = %#v", path, snapshot.Runtime)
		}
		if snapshot.Runtime.Tokens.ReasoningTokens != 516 || snapshot.Runtime.Tokens.OutputTokens != 600 || snapshot.Runtime.Tokens.BilledTokens != 700 {
			t.Fatalf("tokens for %s = %#v", path, snapshot.Runtime.Tokens)
		}
	}
}

func TestUnknownManagementPathReturnsNotFound(t *testing.T) {
	resp := managementResponseForPath(t, "/v0/resource/plugins/codexcont/missing")
	if resp.StatusCode != 404 {
		t.Fatalf("status_code = %d, want 404", resp.StatusCode)
	}
}

func managementResponseForPath(t *testing.T, path string) pluginapi.ManagementResponse {
	t.Helper()
	rawRequest, err := json.Marshal(rpcManagementRequest{
		ManagementRequest: pluginapi.ManagementRequest{Method: "GET", Path: path},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	rawResponse, err := handleMethod(pluginabi.MethodManagementHandle, rawRequest)
	if err != nil {
		t.Fatalf("handleMethod(management.handle) error = %v", err)
	}
	result, err := decodeRPCResult(rawResponse)
	if err != nil {
		t.Fatalf("decodeRPCResult() error = %v", err)
	}
	var resp pluginapi.ManagementResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return resp
}
