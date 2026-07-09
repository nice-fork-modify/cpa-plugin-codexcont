package main

import (
	"path"
	"strings"
	"sync/atomic"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

const (
	pluginIdentifier = "codexcont"
	pluginVersion    = "0.1.1"
	pluginAuthor     = "OpenAI Codex"
	pluginRepo       = "https://github.com/neteroster/CodexCont"
	defaultStep      = 518
)

type pluginConfig struct {
	HostEnabled           *bool    `yaml:"enabled"`
	HostPriority          int      `yaml:"priority"`
	SourceFormats         []string `yaml:"source_formats"`
	ExitProtocol          string   `yaml:"exit_protocol"`
	ModelPatterns         []string `yaml:"model_patterns"`
	TruncationStep        int      `yaml:"truncation_step"`
	MaxContinue           int      `yaml:"max_continue"`
	MinN                  int      `yaml:"min_n"`
	MaxN                  int      `yaml:"max_n"`
	MarkerText            string   `yaml:"marker_text"`
	ForwardMarker         bool     `yaml:"forward_marker"`
	ForceIncludeEncrypted bool     `yaml:"force_include_encrypted"`
	RechunkFinalAnswer    bool     `yaml:"rechunk_final_answer"`
	RechunkSize           int      `yaml:"rechunk_size"`
	MaxTotalOutputTokens  int      `yaml:"max_total_output_tokens"`
}

var currentConfig atomic.Value

func configurePlugin(raw []byte) error {
	var req lifecycleRequest
	if len(raw) > 0 {
		if err := jsonUnmarshal(raw, &req); err != nil {
			return err
		}
	}
	cfg := defaultPluginConfig()
	if len(req.ConfigYAML) > 0 {
		decoded, err := decodeConfig(req.ConfigYAML)
		if err != nil {
			return err
		}
		cfg = decoded
	}
	currentConfig.Store(cfg)
	return nil
}

func defaultPluginConfig() pluginConfig {
	return pluginConfig{
		SourceFormats:         []string{"responses", "codex"},
		ExitProtocol:          "responses",
		ModelPatterns:         []string{"gpt-5.5"},
		TruncationStep:        defaultStep,
		MaxContinue:           3,
		MinN:                  1,
		MaxN:                  6,
		MarkerText:            "Continue thinking. Do not repeat prior final answer; continue from the hidden reasoning state.",
		ForwardMarker:         false,
		ForceIncludeEncrypted: true,
		RechunkFinalAnswer:    true,
		RechunkSize:           8,
		MaxTotalOutputTokens:  0,
	}
}

func decodeConfig(raw []byte) (pluginConfig, error) {
	cfg := defaultPluginConfig()
	if len(strings.TrimSpace(string(raw))) == 0 {
		return cfg, nil
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return pluginConfig{}, err
	}
	cfg.ExitProtocol = normalizeExitProtocol(cfg.ExitProtocol)
	cfg.SourceFormats = normalizeSourceFormats(cfg.SourceFormats)
	cfg.ModelPatterns = normalizePatterns(cfg.ModelPatterns)
	cfg.MarkerText = strings.TrimSpace(cfg.MarkerText)
	if cfg.TruncationStep <= 0 {
		cfg.TruncationStep = defaultStep
	}
	if cfg.MaxContinue <= 0 {
		cfg.MaxContinue = 1
	}
	if cfg.MinN <= 0 {
		cfg.MinN = 1
	}
	if cfg.RechunkSize <= 0 {
		cfg.RechunkSize = 8
	}
	if len(cfg.SourceFormats) == 0 {
		cfg.SourceFormats = defaultPluginConfig().SourceFormats
	}
	if len(cfg.ModelPatterns) == 0 {
		cfg.ModelPatterns = defaultPluginConfig().ModelPatterns
	}
	if cfg.ExitProtocol == "" {
		cfg.ExitProtocol = defaultPluginConfig().ExitProtocol
	}
	if cfg.MarkerText == "" {
		cfg.MarkerText = defaultPluginConfig().MarkerText
	}
	return cfg, nil
}

func loadedConfig() pluginConfig {
	raw := currentConfig.Load()
	if cfg, ok := raw.(pluginConfig); ok {
		return cfg
	}
	return defaultPluginConfig()
}

func shouldRoute(req pluginapi.ModelRouteRequest) bool {
	cfg := loadedConfig()
	if !req.Stream {
		return false
	}
	if !containsFormat(cfg.SourceFormats, req.SourceFormat) {
		return false
	}
	if !matchesAnyPattern(strings.TrimSpace(req.RequestedModel), cfg.ModelPatterns) {
		return false
	}
	body, ok := parseJSONObject(req.Body)
	if !ok {
		return false
	}
	if !reasoningEnabled(body) {
		return false
	}
	return true
}

func normalizeFormat(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "none":
		return ""
	case "responses", "openai-response", "openai-responses", "openai_responses", "response":
		return "responses"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func normalizeExitProtocol(raw string) string {
	switch normalizeFormat(raw) {
	case "", "responses":
		return "responses"
	default:
		return defaultPluginConfig().ExitProtocol
	}
}

func normalizeSourceFormats(items []string) []string {
	out := make([]string, 0, len(items))
	seen := map[string]struct{}{}
	for _, item := range items {
		format := normalizeFormat(item)
		if format == "" {
			continue
		}
		if _, ok := seen[format]; ok {
			continue
		}
		seen[format] = struct{}{}
		out = append(out, format)
	}
	return out
}

func containsFormat(items []string, candidate string) bool {
	candidate = normalizeFormat(candidate)
	for _, item := range items {
		if normalizeFormat(item) == candidate {
			return true
		}
	}
	return false
}

func normalizePatterns(items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func matchesAnyPattern(value string, patterns []string) bool {
	if value == "" {
		return false
	}
	for _, pattern := range patterns {
		ok, err := path.Match(pattern, value)
		if err == nil && ok {
			return true
		}
	}
	return false
}
