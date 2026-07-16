#!/usr/bin/env python3
"""Generate ModelMesh Grafana dashboards (one per subsystem) as provisioned JSON.

Kept as a generator so the seven dashboards stay consistent (same datasource,
grid math, styling) and so metric names are defined once. Output is committed to
deploy/grafana/dashboards/ and validated by the observability dashboard test.
"""
import json
import os

NS = "modelmesh"
DS = {"type": "prometheus", "uid": "prometheus"}
OUT = os.path.join(os.path.dirname(__file__), "dashboards")
os.makedirs(OUT, exist_ok=True)

_panel_id = 0


def pid():
    global _panel_id
    _panel_id += 1
    return _panel_id


def target(expr, legend="", ref="A"):
    return {"expr": expr, "legendFormat": legend, "refId": ref, "datasource": DS}


def timeseries(title, targets, unit="short", x=0, y=0, w=12, h=8):
    return {
        "id": pid(), "title": title, "type": "timeseries", "datasource": DS,
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "fieldConfig": {"defaults": {"unit": unit, "custom": {"drawStyle": "line", "fillOpacity": 10}}, "overrides": []},
        "options": {"legend": {"displayMode": "table", "placement": "bottom", "calcs": ["last", "max"]}, "tooltip": {"mode": "multi"}},
        "targets": targets,
    }


def stat(title, targets, unit="short", x=0, y=0, w=6, h=6, color="green"):
    return {
        "id": pid(), "title": title, "type": "stat", "datasource": DS,
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "fieldConfig": {"defaults": {"unit": unit, "color": {"mode": "fixed", "fixedColor": color}}, "overrides": []},
        "options": {"reduceOptions": {"calcs": ["lastNotNull"]}, "colorMode": "value", "graphMode": "area"},
        "targets": targets,
    }


def statedash(title, targets, x=0, y=0, w=12, h=8):
    # State timeline for circuit states (0 closed / 1 open / 2 half-open).
    return {
        "id": pid(), "title": title, "type": "state-timeline", "datasource": DS,
        "gridPos": {"h": h, "w": w, "x": x, "y": y},
        "fieldConfig": {"defaults": {"mappings": [
            {"type": "value", "options": {"0": {"text": "closed", "color": "green"}}},
            {"type": "value", "options": {"1": {"text": "open", "color": "red"}}},
            {"type": "value", "options": {"2": {"text": "half-open", "color": "yellow"}}},
        ], "custom": {"fillOpacity": 80}}, "overrides": []},
        "options": {"showValue": "auto"},
        "targets": targets,
    }


def quantiles(metric, by=""):
    grp = f",{by}" if by else ""
    label = "{{le}}" if not by else "{{%s}}" % by
    return [
        target(f'histogram_quantile(0.50, sum(rate({metric}_bucket[5m])) by (le{grp}))', "p50 " + (label if by else ""), "A"),
        target(f'histogram_quantile(0.95, sum(rate({metric}_bucket[5m])) by (le{grp}))', "p95 " + (label if by else ""), "B"),
        target(f'histogram_quantile(0.99, sum(rate({metric}_bucket[5m])) by (le{grp}))', "p99 " + (label if by else ""), "C"),
    ]


def dashboard(uid, title, panels, tags):
    global _panel_id
    _panel_id = 0
    return {
        "uid": uid, "title": title, "tags": ["modelmesh"] + tags, "schemaVersion": 39,
        "version": 1, "timezone": "browser", "refresh": "10s",
        "time": {"from": "now-1h", "to": "now"},
        "templating": {"list": []},
        "annotations": {"list": []},
        "panels": [p for p in panels],
    }


dashboards = {}

# --- Gateway ---
dashboards["gateway"] = dashboard("modelmesh-gateway", "ModelMesh — Gateway", [
    stat("Request Rate (req/s)", [target(f'sum(rate({NS}_gateway_requests_total[5m]))', "req/s")], "reqps", 0, 0, 8, 6, "blue"),
    stat("Error Rate", [target(f'sum(rate({NS}_gateway_requests_total{{outcome="error"}}[5m])) / clamp_min(sum(rate({NS}_gateway_requests_total[5m])), 1e-9)', "errors")], "percentunit", 8, 0, 8, 6, "red"),
    stat("Success Rate", [target(f'sum(rate({NS}_gateway_requests_total{{outcome="success"}}[5m])) / clamp_min(sum(rate({NS}_gateway_requests_total[5m])), 1e-9)', "success")], "percentunit", 16, 0, 8, 6, "green"),
    timeseries("Gateway Latency P50/P95/P99", quantiles(f"{NS}_gateway_request_duration_seconds"), "s", 0, 6, 12, 8),
    timeseries("Requests by Outcome", [target(f'sum(rate({NS}_gateway_requests_total[5m])) by (outcome)', "{{outcome}}")], "reqps", 12, 6, 12, 8),
], ["gateway"])

# --- Router ---
dashboards["router"] = dashboard("modelmesh-router", "ModelMesh — Router", [
    timeseries("Routing Decisions by Provider", [target(f'sum(rate({NS}_routing_decisions_total[5m])) by (provider)', "{{provider}}")], "reqps", 0, 0, 12, 8),
    timeseries("Provider Usage Share", [target(f'sum(rate({NS}_routing_decisions_total[5m])) by (provider) / ignoring(provider) group_left sum(rate({NS}_routing_decisions_total[5m]))', "{{provider}}")], "percentunit", 12, 0, 12, 8),
    timeseries("Routing Decision Latency P50/P95/P99", quantiles(f"{NS}_routing_decision_duration_seconds"), "s", 0, 8, 12, 8),
], ["router"])

# --- Cache ---
dashboards["cache"] = dashboard("modelmesh-cache", "ModelMesh — Cache", [
    stat("Cache Hit Rate", [target(f'sum(rate({NS}_cache_hits_total[5m])) / clamp_min(sum(rate({NS}_cache_hits_total[5m])) + sum(rate({NS}_cache_misses_total[5m])), 1e-9)', "hit rate")], "percentunit", 0, 0, 8, 6, "green"),
    stat("Tokens Saved", [target(f'{NS}_cache_tokens_saved_total', "tokens")], "short", 8, 0, 8, 6, "blue"),
    stat("Hits/s", [target(f'sum(rate({NS}_cache_hits_total[5m]))', "hits/s")], "reqps", 16, 0, 8, 6, "purple"),
    timeseries("Cache Hits by Level", [target(f'sum(rate({NS}_cache_hits_total[5m])) by (level)', "{{level}}")], "reqps", 0, 6, 12, 8),
    timeseries("Hits vs Misses", [
        target(f'sum(rate({NS}_cache_hits_total[5m]))', "hits", "A"),
        target(f'sum(rate({NS}_cache_misses_total[5m]))', "misses", "B"),
    ], "reqps", 12, 6, 12, 8),
], ["cache"])

# --- Providers ---
dashboards["providers"] = dashboard("modelmesh-providers", "ModelMesh — Providers", [
    timeseries("Provider Usage (req/s)", [target(f'sum(rate({NS}_provider_requests_total[5m])) by (provider)', "{{provider}}")], "reqps", 0, 0, 12, 8),
    timeseries("Provider Error Rate", [target(f'sum(rate({NS}_provider_errors_total[5m])) by (provider) / clamp_min(sum(rate({NS}_provider_requests_total[5m])) by (provider), 1e-9)', "{{provider}}")], "percentunit", 12, 0, 12, 8),
    timeseries("Provider Latency P50", [target(f'histogram_quantile(0.50, sum(rate({NS}_provider_request_duration_seconds_bucket[5m])) by (le,provider))', "p50 {{provider}}", "A")], "s", 0, 8, 8, 8),
    timeseries("Provider Latency P95", [target(f'histogram_quantile(0.95, sum(rate({NS}_provider_request_duration_seconds_bucket[5m])) by (le,provider))', "p95 {{provider}}", "A")], "s", 8, 8, 8, 8),
    timeseries("Provider Latency P99", [target(f'histogram_quantile(0.99, sum(rate({NS}_provider_request_duration_seconds_bucket[5m])) by (le,provider))', "p99 {{provider}}", "A")], "s", 16, 8, 8, 8),
], ["providers"])

# --- Circuit Breaker ---
dashboards["circuit"] = dashboard("modelmesh-circuit", "ModelMesh — Circuit Breaker", [
    stat("Open Circuits", [target(f'{NS}_circuit_open_circuits', "open")], "short", 0, 0, 8, 6, "red"),
    stat("Failovers", [target(f'{NS}_failovers_total', "failovers")], "short", 8, 0, 8, 6, "orange"),
    stat("Failover Rate", [target(f'sum(rate({NS}_failovers_total[5m]))', "failovers/s")], "reqps", 16, 0, 8, 6, "yellow"),
    statedash("Circuit States (0 closed / 1 open / 2 half-open)", [target(f'{NS}_circuit_state', "{{provider}}")], 0, 6, 24, 8),
    timeseries("Circuit State Changes", [target(f'sum(rate({NS}_circuit_state_changes_total[5m])) by (provider, to)', "{{provider}} -> {{to}}")], "short", 0, 14, 24, 8),
], ["circuit-breaker"])

# --- Health ---
dashboards["health"] = dashboard("modelmesh-health", "ModelMesh — Health", [
    stat("Healthy Providers", [target(f'{NS}_providers_healthy', "healthy")], "short", 0, 0, 12, 6, "green"),
    stat("Unhealthy Providers", [target(f'{NS}_providers_unhealthy', "unhealthy")], "short", 12, 0, 12, 6, "red"),
    timeseries("Provider Health Over Time", [
        target(f'{NS}_providers_healthy', "healthy", "A"),
        target(f'{NS}_providers_unhealthy', "unhealthy", "B"),
    ], "short", 0, 6, 24, 8),
], ["health"])

# --- Cost ---
dashboards["cost"] = dashboard("modelmesh-cost", "ModelMesh — Cost", [
    stat("Cost Saved (USD)", [target(f'{NS}_cache_cost_saved_usd_total', "USD saved")], "currencyUSD", 0, 0, 12, 6, "green"),
    stat("Tokens Saved", [target(f'{NS}_cache_tokens_saved_total', "tokens")], "short", 12, 0, 12, 6, "blue"),
    timeseries("Cumulative Cost Saved (USD)", [target(f'{NS}_cache_cost_saved_usd_total', "USD saved")], "currencyUSD", 0, 6, 12, 8),
    timeseries("Cost Saved Rate (USD/s)", [target(f'sum(rate({NS}_cache_cost_saved_usd_total[5m]))', "USD/s")], "currencyUSD", 12, 6, 12, 8),
], ["cost"])

for name, d in dashboards.items():
    path = os.path.join(OUT, f"{name}.json")
    with open(path, "w") as f:
        json.dump(d, f, indent=2)
    print("wrote", path)
print("done", len(dashboards), "dashboards")
