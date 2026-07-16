package main

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type tokenTotalsSnapshot struct {
	ReasoningTokens uint64 `json:"reasoning_tokens"`
	OutputTokens    uint64 `json:"output_tokens"`
	BilledTokens    uint64 `json:"billed_tokens"`
}

type modelStatsSnapshot struct {
	Model     string `json:"model"`
	Handled   uint64 `json:"handled"`
	Continued uint64 `json:"continued"`
	Failed    uint64 `json:"failed"`
	Completed uint64 `json:"completed"`
}

type runtimeStatsSnapshot struct {
	StartedAt          string               `json:"started_at"`
	UptimeSeconds      int64                `json:"uptime_seconds"`
	ActiveRequests     uint64               `json:"active_requests"`
	HandledTotal       uint64               `json:"handled_total"`
	ContinuedRequests  uint64               `json:"continued_requests"`
	ContinuationRounds uint64               `json:"continuation_rounds"`
	CompletedTotal     uint64               `json:"completed_total"`
	FailedTotal        uint64               `json:"failed_total"`
	StopReasons        map[string]uint64    `json:"stop_reasons"`
	ByModel            []modelStatsSnapshot `json:"by_model"`
	Tokens             tokenTotalsSnapshot  `json:"tokens"`
}

type runtimeStats struct {
	mu                 sync.Mutex
	startedAt          time.Time
	activeRequests     uint64
	handledTotal       uint64
	continuedRequests  uint64
	continuationRounds uint64
	completedTotal     uint64
	failedTotal        uint64
	stopReasons        map[string]uint64
	byModel            map[string]*modelStatsSnapshot
	tokens             tokenTotalsSnapshot
}

type requestStatsTracker struct {
	stats      *runtimeStats
	model      string
	continued  bool
	finishOnce sync.Once
}

var processStats = newRuntimeStats(time.Now())

func newRuntimeStats(startedAt time.Time) *runtimeStats {
	return &runtimeStats{
		startedAt:   startedAt.UTC(),
		stopReasons: make(map[string]uint64),
		byModel:     make(map[string]*modelStatsSnapshot),
	}
}

func (s *runtimeStats) begin(model string) *requestStatsTracker {
	model = strings.TrimSpace(model)
	if model == "" {
		model = "(unspecified)"
	}
	s.mu.Lock()
	s.activeRequests++
	s.handledTotal++
	entry := s.modelEntryLocked(model)
	entry.Handled++
	s.mu.Unlock()
	return &requestStatsTracker{stats: s, model: model}
}

func (t *requestStatsTracker) recordContinuation() {
	if t == nil || t.stats == nil {
		return
	}
	t.stats.mu.Lock()
	if !t.continued {
		t.continued = true
		t.stats.continuedRequests++
		t.stats.modelEntryLocked(t.model).Continued++
	}
	t.stats.continuationRounds++
	t.stats.mu.Unlock()
}

func (t *requestStatsTracker) finish(usage usageTotals, completed bool, reason string, executionErr error) {
	if t == nil || t.stats == nil {
		return
	}
	t.finishOnce.Do(func() {
		reason = strings.TrimSpace(reason)
		failed := executionErr != nil || !completed
		if executionErr != nil && completed {
			reason = "execution_error"
		}
		if failed && reason == "" {
			reason = "execution_error"
		}
		if !failed && reason == "" {
			reason = "completed"
		}

		t.stats.mu.Lock()
		if t.stats.activeRequests > 0 {
			t.stats.activeRequests--
		}
		entry := t.stats.modelEntryLocked(t.model)
		if failed {
			t.stats.failedTotal++
			entry.Failed++
		} else {
			t.stats.completedTotal++
			entry.Completed++
		}
		t.stats.stopReasons[reason]++
		t.stats.tokens.ReasoningTokens += nonNegativeUint64(usage.ReasoningTokens)
		t.stats.tokens.OutputTokens += nonNegativeUint64(usage.OutputTokens)
		t.stats.tokens.BilledTokens += nonNegativeUint64(usage.TotalTokens)
		t.stats.mu.Unlock()
	})
}

func (s *runtimeStats) snapshot(now time.Time) runtimeStatsSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	stopReasons := make(map[string]uint64, len(s.stopReasons))
	for reason, count := range s.stopReasons {
		stopReasons[reason] = count
	}
	byModel := make([]modelStatsSnapshot, 0, len(s.byModel))
	for _, entry := range s.byModel {
		byModel = append(byModel, *entry)
	}
	sort.Slice(byModel, func(i, j int) bool {
		return byModel[i].Model < byModel[j].Model
	})
	uptime := now.Sub(s.startedAt)
	if uptime < 0 {
		uptime = 0
	}
	return runtimeStatsSnapshot{
		StartedAt:          s.startedAt.Format(time.RFC3339),
		UptimeSeconds:      int64(uptime / time.Second),
		ActiveRequests:     s.activeRequests,
		HandledTotal:       s.handledTotal,
		ContinuedRequests:  s.continuedRequests,
		ContinuationRounds: s.continuationRounds,
		CompletedTotal:     s.completedTotal,
		FailedTotal:        s.failedTotal,
		StopReasons:        stopReasons,
		ByModel:            byModel,
		Tokens:             s.tokens,
	}
}

func (s *runtimeStats) modelEntryLocked(model string) *modelStatsSnapshot {
	entry := s.byModel[model]
	if entry == nil {
		entry = &modelStatsSnapshot{Model: model}
		s.byModel[model] = entry
	}
	return entry
}

func nonNegativeUint64(value int) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}
