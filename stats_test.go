package main

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

func TestDecodeConfigNormalizesMultiplePrefixGlobs(t *testing.T) {
	cfg, err := decodeConfig([]byte(`
model_patterns:
  - gpt-5.3
  - gpt-5.6-?
  - custom-*
`))
	if err != nil {
		t.Fatalf("decodeConfig() error = %v", err)
	}
	want := []string{"gpt-5.3*", "gpt-5.6-?*", "custom-*"}
	if len(cfg.ModelPatterns) != len(want) {
		t.Fatalf("model_patterns = %#v, want %#v", cfg.ModelPatterns, want)
	}
	for i := range want {
		if cfg.ModelPatterns[i] != want[i] {
			t.Fatalf("model_patterns = %#v, want %#v", cfg.ModelPatterns, want)
		}
	}
	for _, model := range []string{"gpt-5.3", "gpt-5.3-mini", "gpt-5.6-sol", "custom-model"} {
		if !matchesAnyPattern(model, cfg.ModelPatterns) {
			t.Fatalf("matchesAnyPattern(%q, %#v) = false, want true", model, cfg.ModelPatterns)
		}
	}
	if matchesAnyPattern("gpt-5.4", cfg.ModelPatterns) {
		t.Fatal("matchesAnyPattern(gpt-5.4) = true, want false")
	}
}

func TestDecodeConfigAcceptsCommaSeparatedModelPatterns(t *testing.T) {
	cfg, err := decodeConfig([]byte(`model_patterns: "gpt-5.3, gpt-5.5, gpt-5.6-sol"`))
	if err != nil {
		t.Fatalf("decodeConfig() error = %v", err)
	}
	want := []string{"gpt-5.3*", "gpt-5.5*", "gpt-5.6-sol*"}
	if len(cfg.ModelPatterns) != len(want) {
		t.Fatalf("model_patterns = %#v, want %#v", cfg.ModelPatterns, want)
	}
	for i := range want {
		if cfg.ModelPatterns[i] != want[i] {
			t.Fatalf("model_patterns = %#v, want %#v", cfg.ModelPatterns, want)
		}
	}
}

func TestRuntimeStatsConcurrentLifecycle(t *testing.T) {
	startedAt := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	stats := newRuntimeStats(startedAt)
	const requests = 200

	trackers := make([]*requestStatsTracker, requests)
	for i := range trackers {
		model := "gpt-5.5"
		if i%2 == 1 {
			model = "gpt-5.6-sol"
		}
		trackers[i] = stats.begin(model)
	}
	if got := stats.snapshot(startedAt).ActiveRequests; got != requests {
		t.Fatalf("active_requests = %d, want %d", got, requests)
	}

	var wg sync.WaitGroup
	for i, tracker := range trackers {
		wg.Add(1)
		go func(i int, tracker *requestStatsTracker) {
			defer wg.Done()
			if i%2 == 0 {
				tracker.recordContinuation()
				tracker.recordContinuation()
			}
			if i%3 == 0 {
				tracker.finish(usageTotals{ReasoningTokens: 1, OutputTokens: 2, TotalTokens: 5}, false, "upstream_error", errors.New("not exposed"))
				return
			}
			tracker.finish(usageTotals{ReasoningTokens: 1, OutputTokens: 2, TotalTokens: 5}, true, "", nil)
		}(i, tracker)
	}
	wg.Wait()

	snapshot := stats.snapshot(startedAt.Add(90 * time.Second))
	if snapshot.ActiveRequests != 0 || snapshot.HandledTotal != requests {
		t.Fatalf("lifecycle totals = active:%d handled:%d", snapshot.ActiveRequests, snapshot.HandledTotal)
	}
	if snapshot.ContinuedRequests != 100 || snapshot.ContinuationRounds != 200 {
		t.Fatalf("continuations = requests:%d rounds:%d", snapshot.ContinuedRequests, snapshot.ContinuationRounds)
	}
	if snapshot.CompletedTotal != 133 || snapshot.FailedTotal != 67 {
		t.Fatalf("outcomes = completed:%d failed:%d", snapshot.CompletedTotal, snapshot.FailedTotal)
	}
	if snapshot.StopReasons["completed"] != 133 || snapshot.StopReasons["upstream_error"] != 67 {
		t.Fatalf("stop_reasons = %#v", snapshot.StopReasons)
	}
	if snapshot.Tokens.ReasoningTokens != 200 || snapshot.Tokens.OutputTokens != 400 || snapshot.Tokens.BilledTokens != 1000 {
		t.Fatalf("tokens = %#v", snapshot.Tokens)
	}
	if snapshot.UptimeSeconds != 90 || len(snapshot.ByModel) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestRouteDecisionDoesNotCountAsHandledExecution(t *testing.T) {
	oldConfig := loadedConfig()
	oldStats := processStats
	t.Cleanup(func() {
		currentConfig.Store(oldConfig)
		processStats = oldStats
	})
	currentConfig.Store(defaultPluginConfig())
	processStats = newRuntimeStats(time.Now())

	raw, err := json.Marshal(rpcModelRouteRequest{ModelRouteRequest: pluginapi.ModelRouteRequest{
		SourceFormat:   "responses",
		RequestedModel: "gpt-5.5-preview",
		Stream:         true,
		Body:           []byte(`{"input":[],"reasoning":{"effort":"high"}}`),
	}})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if _, err := routeModel(raw); err != nil {
		t.Fatalf("routeModel() error = %v", err)
	}
	if snapshot := processStats.snapshot(time.Now()); snapshot.HandledTotal != 0 || snapshot.ActiveRequests != 0 {
		t.Fatalf("route decision changed execution stats: %#v", snapshot)
	}
}

func TestReconfigureDoesNotResetProcessStats(t *testing.T) {
	oldConfig := loadedConfig()
	oldStats := processStats
	t.Cleanup(func() {
		currentConfig.Store(oldConfig)
		processStats = oldStats
	})

	processStats = newRuntimeStats(time.Now())
	tracker := processStats.begin("gpt-5.5")
	tracker.finish(usageTotals{}, true, "", nil)

	raw, err := json.Marshal(lifecycleRequest{ConfigYAML: []byte("model_patterns:\n  - gpt-5.6-sol\n")})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if err := configurePlugin(raw); err != nil {
		t.Fatalf("configurePlugin() error = %v", err)
	}
	if snapshot := processStats.snapshot(time.Now()); snapshot.HandledTotal != 1 || snapshot.CompletedTotal != 1 {
		t.Fatalf("stats after reconfigure = %#v", snapshot)
	}
}
