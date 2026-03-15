# Observability Guide

Sympozium exports metrics, traces, and logs via OpenTelemetry. This guide covers
two paths: **Cloud Monitoring** (fully managed, zero setup on GKE) and
**Prometheus + Grafana** (for teams with an existing stack).

Both paths use the same OTel Collector deployment — you choose which exporter
pipelines to enable.

---

## Architecture

```
Sympozium Pods (agent-runner, controller, apiserver, web-proxy)
        │  OTLP gRPC (:4317)
        ▼
  OTel Collector (otel/opentelemetry-collector-contrib)
        │
   ┌────┴────────────────────────┐
   │                             │
   ▼                             ▼
Prometheus exporter (:8889)    Google Cloud exporter
   │                             │
   ▼                             ▼
GMP PodMonitoring              Cloud Trace + Cloud Monitoring
   │
   ▼
Grafana (PromQL)
```

## Metrics Reference

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `sympozium_agent_runs_total` | Counter | model, status, instance_name | Total agent runs |
| `sympozium_agent_run_duration` | Histogram | model | Agent run execution time (ms) |
| `gen_ai_usage_input_tokens_total` | Counter | model | Input tokens consumed |
| `gen_ai_usage_output_tokens_total` | Counter | model | Output tokens generated |
| `sympozium_tool_invocations_total` | Counter | tool, instance_name, status | Tool/skill invocations |
| `sympozium_skill_duration` | Histogram | — | Skill sidecar execution time (ms) |
| `sympozium_errors_total` | Counter | — | Unhandled errors |
| `sympozium_gateway_ready` | Gauge | — | Web endpoint gateway readiness |
| `sympozium_web_endpoint_serving` | Gauge | — | Active web endpoint deployments |

---

## Path A: Cloud Monitoring (Default)

Cloud Monitoring works out of the box when `observability.enabled: true` in Helm
values. The OTel Collector's `googlecloud` exporter sends metrics to
`custom.googleapis.com/sympozium/*` and traces to Cloud Trace.

```bash
helm install sympozium ./charts/sympozium \
  --set gcp.projectId=YOUR_PROJECT_ID \
  --set observability.enabled=true
```

Metrics appear in Cloud Console under **Monitoring → Metrics Explorer** with the
prefix `custom.googleapis.com/sympozium/`.

No additional configuration is needed on GKE with Workload Identity.

---

## Path B: Prometheus + Grafana

For teams with an existing Prometheus and Grafana deployment, Sympozium supports
two collection methods:

### Option 1: Google Managed Prometheus (GMP) — Recommended on GKE

GMP is the default managed Prometheus on GKE (Autopilot: on by default;
Standard: enable with `--enable-managed-prometheus`). It uses Kubernetes CRDs
(`PodMonitoring`) instead of Prometheus ServiceMonitors.

#### Enable in Helm

```bash
helm install sympozium ./charts/sympozium \
  --set gcp.projectId=YOUR_PROJECT_ID \
  --set observability.enabled=true \
  --set observability.gmp.enabled=true
```

This creates:

- **PodMonitoring** — scrapes the OTel Collector's Prometheus endpoint (`:8889`)
- **Rules** — alerting rules for failure rates, latency, and token budgets

#### Verify collection

```bash
# Check PodMonitoring status
kubectl get podmonitoring -n sympozium-system

# Query metrics in Cloud Console
# Go to Monitoring → PromQL and run:
#   sum(rate(sympozium_agent_runs_total[5m])) by (status)
```

#### Connect Grafana to GMP

GMP exposes a Prometheus-compatible query endpoint. To use it from Grafana:

1. In Grafana, add a **Prometheus** data source
2. Set the URL to your GMP frontend:
   ```
   https://monitoring.googleapis.com/v1/projects/YOUR_PROJECT_ID/location/global/prometheus
   ```
3. Under **Authentication**, select **Google JWT** or use a service account key
   with `roles/monitoring.viewer`
4. Import the Sympozium dashboard: **Dashboards → Import → Upload JSON**,
   then select `config/observability/grafana-dashboard.json`

### Option 2: Standard Prometheus Operator (ServiceMonitor)

If you run your own Prometheus via prometheus-operator:

```bash
helm install sympozium ./charts/sympozium \
  --set observability.enabled=true \
  --set serviceMonitor.enabled=true \
  --set serviceMonitor.interval=30s
```

Then import the Grafana dashboard JSON from
`config/observability/grafana-dashboard.json`.

---

## Alerting

### Built-in Alerts

When GMP alerting is enabled (`observability.gmp.alerting.enabled: true`), these
alerts are automatically created:

| Alert | Condition | Severity |
|-------|-----------|----------|
| AgentRunFailureRateHigh | >25% failure rate over 5m | warning |
| AgentRunFailureRateCritical | >50% failure rate over 5m | critical |
| TokenBudgetCritical | >2M output tokens/hr | critical |
| AgentRunLatencyHigh | P95 >2min over 10m | warning |

### Customize Thresholds

```yaml
observability:
  gmp:
    enabled: true
    alerting:
      failureRateWarning: 0.15       # Lower warning threshold
      failureRateCritical: 0.40
      tokenBudgetPerHour: 1000000    # Tighter token budget
      p95LatencyThresholdMs: 60000   # 1 minute P95
```

### Standalone Alert Rules

For non-Helm deployments or additional custom alerts, apply the rules directly:

```bash
kubectl apply -f config/observability/prometheus-rules.yaml
```

The standalone rules file includes additional alerts not in the Helm template
(tool error rates, collector health, gateway readiness).

---

## Grafana Dashboard

The pre-built dashboard (`config/observability/grafana-dashboard.json`) provides:

- **Overview row** — total runs, failure rate, token usage, gateway health
- **Agent Runs** — runs by status, model, and instance; P50/P95/P99 duration
- **Token Usage & Cost** — input/output tokens by model, 24h usage bar gauge
- **Tools & Skills** — invocations by tool, skill duration, tool error rates
- **Errors & Infrastructure** — error rate, web endpoint status

### Import

1. Open Grafana → **Dashboards → Import**
2. Upload `config/observability/grafana-dashboard.json`
3. Select your Prometheus data source (GMP, self-hosted, or Grafana Cloud)
4. Click **Import**

---

## Disabling Observability

To run with zero overhead:

```yaml
observability:
  enabled: false
```

All components initialize noop OTel providers — no telemetry data is generated or
exported, and there is no performance impact.
