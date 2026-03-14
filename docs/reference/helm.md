# Helm Chart

For production and GitOps workflows, deploy the control plane using Helm.

## Prerequisites

[cert-manager](https://cert-manager.io/) is required for webhook TLS:

```bash
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.17.1/cert-manager.yaml
```

## Install

```bash
helm install sympozium oci://us-docker.pkg.dev/sympozium/sympozium/charts/sympozium
```

See the values.yaml for all configuration options (replicas, resources, external NATS, network policies, GCP-specific settings, etc.).

## Observability

The Helm chart can export observability data to GCP Cloud Trace and Cloud Monitoring:

```yaml
observability:
  enabled: true
  cloudTrace:
    enabled: true
    projectId: "YOUR_PROJECT_ID"
  cloudMonitoring:
    enabled: true
    projectId: "YOUR_PROJECT_ID"
```

Disable cloud exporters if you use a local OpenTelemetry collector:

```yaml
observability:
  enabled: true
  cloudTrace:
    enabled: false
  cloudMonitoring:
    enabled: false
```

## Web UI

```yaml
apiserver:
  webUI:
    enabled: true       # Serve the embedded web dashboard (default: true)
    token: ""           # Explicit token; leave blank to auto-generate a Secret
```

If `token` is left empty, Helm creates a `<release>-ui-token` Secret with a random 32-character token.

## Network Policies

```yaml
networkPolicies:
  enabled: true
  extraEgressPorts: []    # add non-standard API server ports here (e.g. [6444] for k3s)
```
