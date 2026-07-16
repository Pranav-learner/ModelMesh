# ModelMesh — Observability Stack

Prometheus + Grafana wiring for the ModelMesh gateway. The gateway exposes the
metrics catalog on `/metrics` (`metrics.Manager.Handler()`); Prometheus scrapes
it and Grafana renders the dashboards.

## Layout

```
deploy/
├── docker-compose.yml                  # Prometheus + Grafana
├── prometheus/prometheus.yml           # scrape config (targets modelmesh:2112)
└── grafana/
    ├── generate_dashboards.py          # regenerates the dashboard JSON
    ├── dashboards/*.json               # 7 dashboards (one per subsystem)
    └── provisioning/
        ├── datasources/prometheus.yml  # Prometheus datasource (uid: prometheus)
        └── dashboards/modelmesh.yml     # loads dashboards/ on boot
```

## Run

```bash
# 1. Expose gateway metrics (offline demo fires 100 requests then serves /metrics):
go run ./cmd/observabilitydemo -serve         # → :2112/metrics

# 2. Start the stack:
docker compose -f deploy/docker-compose.yml up -d
#   Grafana    → http://localhost:3000   (admin / admin)
#   Prometheus → http://localhost:9090
```

Grafana auto-provisions the datasource and all dashboards under the **ModelMesh**
folder. The Prometheus target `modelmesh:2112` resolves to the host via
`host-gateway`, so a gateway running on the host is scraped without extra config.

## Dashboards

`gateway`, `router`, `cache`, `providers`, `circuit`, `health`, `cost` — see
[docs/05-implementation/Observability-Metrics.md](../docs/05-implementation/Observability-Metrics.md) §5.

The JSON is **generated** for consistency:

```bash
python3 deploy/grafana/generate_dashboards.py
```

Every panel expression is validated against the live metrics registry by
`TestDashboards_ReferencedMetricsExist` in `internal/observability`, so a
dashboard can never reference a metric the code doesn't emit.
