package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	resourceStatusPath  = "/v0/resource/plugins/codexcont/status"
	resourceStatsPath   = "/v0/resource/plugins/codexcont/stats.json"
	managementStatsPath = "/v0/management/plugins/codexcont/stats"
)

type rpcManagementRequest struct {
	pluginapi.ManagementRequest
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type managementRouteDefinition struct {
	Method      string `json:"method,omitempty"`
	Path        string `json:"path"`
	Menu        string `json:"menu,omitempty"`
	Description string `json:"description,omitempty"`
}

type managementRegistrationResponse struct {
	Routes    []managementRouteDefinition `json:"routes,omitempty"`
	Resources []managementRouteDefinition `json:"resources,omitempty"`
}

type publicConfigSnapshot struct {
	ModelPatterns        []string `json:"model_patterns"`
	SelectedModels       []string `json:"selected_models"`
	RecommendedModels    []string `json:"recommended_models"`
	SourceFormats        []string `json:"source_formats"`
	ExitProtocol         string   `json:"exit_protocol"`
	TruncationStep       int      `json:"truncation_step"`
	MaxContinue          int      `json:"max_continue"`
	MinN                 int      `json:"min_n"`
	MaxN                 int      `json:"max_n"`
	MaxTotalOutputTokens int      `json:"max_total_output_tokens"`
}

type publicStatsResponse struct {
	Plugin  publicPluginSnapshot `json:"plugin"`
	Config  publicConfigSnapshot `json:"config"`
	Runtime runtimeStatsSnapshot `json:"runtime"`
}

type publicPluginSnapshot struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

func managementRegistration() managementRegistrationResponse {
	return managementRegistrationResponse{
		Routes: []managementRouteDefinition{
			{Method: http.MethodGet, Path: "/plugins/codexcont/stats"},
		},
		Resources: []managementRouteDefinition{
			{
				Path:        "/status",
				Menu:        "CodexCont status",
				Description: "Read-only process statistics and effective model selection.",
			},
			{Path: "/stats.json"},
		},
	}
}

func handleManagement(raw []byte) ([]byte, error) {
	var req rpcManagementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	switch strings.TrimRight(req.Path, "/") {
	case resourceStatusPath:
		return okEnvelope(htmlManagementResponse(statusPageHTML))
	case resourceStatsPath, managementStatsPath:
		body, err := json.Marshal(publicStatsSnapshot(time.Now()))
		if err != nil {
			return nil, err
		}
		return okEnvelope(jsonManagementResponse(body))
	default:
		return okEnvelope(pluginapi.ManagementResponse{
			StatusCode: http.StatusNotFound,
			Headers:    http.Header{"Content-Type": []string{"application/json; charset=utf-8"}},
			Body:       []byte(`{"error":"not found"}`),
		})
	}
}

func publicStatsSnapshot(now time.Time) publicStatsResponse {
	cfg := loadedConfig()
	patterns := append([]string(nil), cfg.ModelPatterns...)
	recommended := append([]string(nil), recommendedModels...)
	selected := make([]string, 0, len(recommended))
	for _, model := range recommended {
		if matchesAnyPattern(model, patterns) {
			selected = append(selected, model)
		}
	}
	return publicStatsResponse{
		Plugin: publicPluginSnapshot{ID: pluginIdentifier, Version: pluginVersion},
		Config: publicConfigSnapshot{
			ModelPatterns:        patterns,
			SelectedModels:       selected,
			RecommendedModels:    recommended,
			SourceFormats:        append([]string(nil), cfg.SourceFormats...),
			ExitProtocol:         cfg.ExitProtocol,
			TruncationStep:       cfg.TruncationStep,
			MaxContinue:          cfg.MaxContinue,
			MinN:                 cfg.MinN,
			MaxN:                 cfg.MaxN,
			MaxTotalOutputTokens: cfg.MaxTotalOutputTokens,
		},
		Runtime: processStats.snapshot(now),
	}
}

func htmlManagementResponse(body string) pluginapi.ManagementResponse {
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers: http.Header{
			"Cache-Control":           []string{"no-store"},
			"Content-Security-Policy": []string{"default-src 'none'; connect-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'self'"},
			"Content-Type":            []string{"text/html; charset=utf-8"},
			"Referrer-Policy":         []string{"no-referrer"},
			"X-Content-Type-Options":  []string{"nosniff"},
		},
		Body: []byte(body),
	}
}

func jsonManagementResponse(body []byte) pluginapi.ManagementResponse {
	return pluginapi.ManagementResponse{
		StatusCode: http.StatusOK,
		Headers: http.Header{
			"Cache-Control":          []string{"no-store"},
			"Content-Type":           []string{"application/json; charset=utf-8"},
			"X-Content-Type-Options": []string{"nosniff"},
		},
		Body: body,
	}
}

const statusPageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>CodexCont status</title>
<style>
:root{color-scheme:light dark;--bg:#f6f7f9;--surface:#fff;--ink:#17202a;--muted:#68717d;--line:#d8dde5;--accent:#13795b;--danger:#b42318;--shadow:0 1px 2px rgba(16,24,40,.06)}
@media(prefers-color-scheme:dark){:root{--bg:#111418;--surface:#1a1f25;--ink:#f2f4f7;--muted:#aab2bd;--line:#343b44;--accent:#62d2a7;--danger:#ff8a80;--shadow:none}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:14px/1.5 ui-sans-serif,system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;letter-spacing:0}main{width:min(1120px,calc(100% - 32px));margin:0 auto;padding:28px 0 40px}header{display:flex;align-items:flex-start;justify-content:space-between;gap:16px;margin-bottom:24px}h1{font-size:24px;line-height:1.2;margin:0 0 4px}h2{font-size:16px;margin:0 0 12px}p{margin:0;color:var(--muted)}.badge{display:inline-flex;align-items:center;gap:7px;padding:5px 9px;border:1px solid var(--line);border-radius:6px;background:var(--surface);white-space:nowrap}.dot{width:8px;height:8px;border-radius:50%;background:var(--accent)}.kpis{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:12px;margin-bottom:28px}.kpi{min-width:0;padding:14px;border:1px solid var(--line);border-radius:6px;background:var(--surface);box-shadow:var(--shadow)}.kpi span{display:block;color:var(--muted);font-size:12px}.kpi strong{display:block;margin-top:4px;font-size:24px;line-height:1.2;font-variant-numeric:tabular-nums;overflow-wrap:anywhere}section{padding:20px 0;border-top:1px solid var(--line)}.grid{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:28px}.checks{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px 16px}.check{display:flex;align-items:center;gap:8px;min-width:0}.check input{width:16px;height:16px;accent-color:var(--accent)}.check span{overflow-wrap:anywhere}.patterns{margin:12px 0 0;padding:0;list-style:none;display:flex;flex-wrap:wrap;gap:6px}.patterns code{display:block;padding:3px 7px;border:1px solid var(--line);border-radius:5px;background:var(--surface);font-size:12px}.facts{display:grid;grid-template-columns:1fr auto;gap:8px 16px;margin:0}.facts dt{color:var(--muted)}.facts dd{margin:0;text-align:right;font-variant-numeric:tabular-nums}.table-wrap{overflow-x:auto;border:1px solid var(--line);border-radius:6px;background:var(--surface)}table{width:100%;border-collapse:collapse;min-width:420px}th,td{padding:9px 12px;text-align:right;border-bottom:1px solid var(--line);font-variant-numeric:tabular-nums}th{color:var(--muted);font-size:12px;font-weight:600}th:first-child,td:first-child{text-align:left}tr:last-child td{border-bottom:0}.empty{text-align:center!important;color:var(--muted)}.error{color:var(--danger)}footer{padding-top:18px;color:var(--muted);font-size:12px}
@media(max-width:760px){main{width:min(100% - 24px,1120px);padding-top:20px}header{display:block}.badge{margin-top:12px}.kpis{grid-template-columns:repeat(2,minmax(0,1fr))}.grid{grid-template-columns:1fr}.checks{grid-template-columns:1fr}}
</style>
</head>
<body>
<main>
<header><div><h1>CodexCont</h1><p>Process-local folded reasoning activity</p></div><div class="badge"><span class="dot"></span><span id="uptime">Loading</span></div></header>
<div class="kpis" aria-label="Runtime totals">
<div class="kpi"><span>Active</span><strong id="active">0</strong></div>
<div class="kpi"><span>Handled</span><strong id="handled">0</strong></div>
<div class="kpi"><span>Continued</span><strong id="continued">0</strong></div>
<div class="kpi"><span>Failed</span><strong id="failed">0</strong></div>
</div>
<section class="grid">
<div><h2>Model selection</h2><div id="models" class="checks"></div><ul id="patterns" class="patterns"></ul><p style="margin-top:10px">Read-only here. Edit <code>model_patterns</code> in the authenticated plugin configuration form.</p></div>
<div><h2>Effective policy</h2><dl id="policy" class="facts"></dl></div>
</section>
<section><h2>Token totals</h2><div class="kpis"><div class="kpi"><span>Reasoning</span><strong id="reasoningTokens">0</strong></div><div class="kpi"><span>Output</span><strong id="outputTokens">0</strong></div><div class="kpi"><span>Billed total</span><strong id="billedTokens">0</strong></div><div class="kpi"><span>Continuation rounds</span><strong id="rounds">0</strong></div></div></section>
<section class="grid"><div><h2>By model</h2><div class="table-wrap"><table><thead><tr><th>Model</th><th>Handled</th><th>Continued</th><th>Completed</th><th>Failed</th></tr></thead><tbody id="byModel"></tbody></table></div></div><div><h2>Stop reasons</h2><div class="table-wrap"><table><thead><tr><th>Reason</th><th>Requests</th></tr></thead><tbody id="stopReasons"></tbody></table></div></div></section>
<footer id="footer">This unauthenticated resource displays only non-sensitive aggregate values. Statistics reset when the plugin process restarts.</footer>
</main>
<script>
const numberFormat=new Intl.NumberFormat();
const setText=(id,value)=>{document.getElementById(id).textContent=String(value)};
const formatDuration=seconds=>{seconds=Math.max(0,Number(seconds)||0);const days=Math.floor(seconds/86400),hours=Math.floor(seconds%86400/3600),minutes=Math.floor(seconds%3600/60);return(days?days+'d ':'')+(hours?hours+'h ':'')+minutes+'m uptime'};
const addRow=(tbody,cells,empty=false)=>{const tr=document.createElement('tr');cells.forEach(value=>{const td=document.createElement('td');td.textContent=String(value);if(empty)td.className='empty';tr.appendChild(td)});tbody.appendChild(tr)};
function render(data){const runtime=data.runtime,config=data.config;setText('uptime',formatDuration(runtime.uptime_seconds));setText('active',numberFormat.format(runtime.active_requests));setText('handled',numberFormat.format(runtime.handled_total));setText('continued',numberFormat.format(runtime.continued_requests));setText('failed',numberFormat.format(runtime.failed_total));setText('reasoningTokens',numberFormat.format(runtime.tokens.reasoning_tokens));setText('outputTokens',numberFormat.format(runtime.tokens.output_tokens));setText('billedTokens',numberFormat.format(runtime.tokens.billed_tokens));setText('rounds',numberFormat.format(runtime.continuation_rounds));
const models=document.getElementById('models');models.replaceChildren();const selected=new Set(config.selected_models);config.recommended_models.forEach(model=>{const label=document.createElement('label');label.className='check';const input=document.createElement('input');input.type='checkbox';input.disabled=true;input.checked=selected.has(model);const span=document.createElement('span');span.textContent=model;label.append(input,span);models.appendChild(label)});
const patterns=document.getElementById('patterns');patterns.replaceChildren();config.model_patterns.forEach(pattern=>{const li=document.createElement('li'),code=document.createElement('code');code.textContent=pattern;li.appendChild(code);patterns.appendChild(li)});
const policy=document.getElementById('policy');policy.replaceChildren();[['Source formats',config.source_formats.join(', ')],['Exit protocol',config.exit_protocol],['Truncation step',config.truncation_step],['Continuation limit',config.max_continue],['Tier window',config.min_n+' to '+(config.max_n||'unbounded')],['Billed output cap',config.max_total_output_tokens||'disabled']].forEach(([name,value])=>{const dt=document.createElement('dt'),dd=document.createElement('dd');dt.textContent=name;dd.textContent=String(value);policy.append(dt,dd)});
const byModel=document.getElementById('byModel');byModel.replaceChildren();if(!runtime.by_model.length)addRow(byModel,['No handled requests'],true);else runtime.by_model.forEach(row=>addRow(byModel,[row.model,numberFormat.format(row.handled),numberFormat.format(row.continued),numberFormat.format(row.completed),numberFormat.format(row.failed)]));
const reasons=document.getElementById('stopReasons');reasons.replaceChildren();const entries=Object.entries(runtime.stop_reasons).sort((a,b)=>b[1]-a[1]||a[0].localeCompare(b[0]));if(!entries.length)addRow(reasons,['No completed requests'],true);else entries.forEach(([reason,count])=>addRow(reasons,[reason.replaceAll('_',' '),numberFormat.format(count)]));}
async function refresh(){try{const response=await fetch('stats.json',{cache:'no-store'});if(!response.ok)throw new Error('status '+response.status);render(await response.json())}catch(error){const footer=document.getElementById('footer');footer.className='error';footer.textContent='Statistics are temporarily unavailable.'}}
refresh();setInterval(refresh,5000);
</script>
</body>
</html>`
