# Using Vertex AI with Sympozium

Sympozium is optimized for **Google Cloud Vertex AI** as its primary LLM provider. Vertex AI provides access to cutting-edge foundation models including Gemini, PaLM, and other Google-developed models.

---

## Prerequisites

- A GCP project with Vertex AI enabled
- `gcloud` CLI configured with credentials
- A GKE cluster (Google Kubernetes Engine)
- Sympozium installed (`sympozium install`)

---

## Authentication Methods

Sympozium supports multiple GCP authentication methods:

### 1. Workload Identity (Recommended for GKE)

Workload Identity allows GKE pods to act as a service account without storing keys:

```bash
# Enable Workload Identity on your cluster
gcloud container clusters update CLUSTER_NAME \
  --workload-pool=PROJECT_ID.svc.id.goog

# Create a GCP service account
gcloud iam service-accounts create sympozium-agent

# Grant Vertex AI permissions
gcloud projects add-iam-policy-binding PROJECT_ID \
  --member="serviceAccount:sympozium-agent@PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/aiplatform.user"

# Create Kubernetes service account
kubectl create serviceaccount sympozium-agent -n sympozium-system

# Bind them together
gcloud iam service-accounts add-iam-policy-binding \
  sympozium-agent@PROJECT_ID.iam.gserviceaccount.com \
  --role="roles/iam.workloadIdentityUser" \
  --member="serviceAccount:PROJECT_ID.svc.id.goog[sympozium-system/sympozium-agent]"

# Annotate the Kubernetes service account
kubectl annotate serviceaccount sympozium-agent \
  -n sympozium-system \
  iam.gke.io/gcp-service-account=sympozium-agent@PROJECT_ID.iam.gserviceaccount.com
```

### 2. Service Account JSON Key

For non-GKE environments or simpler setups:

```bash
# Create a service account
gcloud iam service-accounts create sympozium-agent

# Grant Vertex AI permissions
gcloud projects add-iam-policy-binding PROJECT_ID \
  --member="serviceAccount:sympozium-agent@PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/aiplatform.user"

# Create and download a key
gcloud iam service-accounts keys create key.json \
  --iam-account=sympozium-agent@PROJECT_ID.iam.gserviceaccount.com

# Create a Kubernetes Secret
kubectl create secret generic gcp-credentials \
  --from-file=key.json=key.json \
  -n sympozium-system
```

### 3. Application Default Credentials

If running Sympozium on GCP, you can use Application Default Credentials:

```bash
gcloud auth application-default login
```

---

## Configuring a SympoziumInstance

### Using Workload Identity

```yaml
apiVersion: sympozium.ai/v1alpha1
kind: SympoziumInstance
metadata:
  name: vertex-ai-agent
spec:
  agents:
    default:
      model: gemini-2.0-flash
      provider: vertexai
      projectId: "YOUR_PROJECT_ID"
      location: "us-central1"
  authRefs:
    - provider: vertexai
      useWorkloadIdentity: true
  skills:
    - skillPackRef: k8s-ops
  policyRef: default-policy
```

### Using Service Account JSON Key

```yaml
apiVersion: sympozium.ai/v1alpha1
kind: SympoziumInstance
metadata:
  name: vertex-ai-agent
spec:
  agents:
    default:
      model: gemini-2.0-flash
      provider: vertexai
      projectId: "YOUR_PROJECT_ID"
      location: "us-central1"
  authRefs:
    - provider: vertexai
      secret: gcp-credentials
      key: key.json
  skills:
    - skillPackRef: k8s-ops
  policyRef: default-policy
```

---

## Running an AgentRun

```yaml
apiVersion: sympozium.ai/v1alpha1
kind: AgentRun
metadata:
  name: vertex-ai-test
spec:
  instanceRef: vertex-ai-agent
  task: "List all pods across every namespace and summarise their status."
  model:
    provider: vertexai
    name: gemini-2.0-flash
    projectId: "YOUR_PROJECT_ID"
    location: "us-central1"
  skills:
    - k8s-ops
  timeout: "5m"
```

```bash
kubectl apply -f vertex-ai-test.yaml
kubectl get agentrun vertex-ai-test -w
```

The phase transitions: `Pending` → `Running` → `Succeeded` (or `Failed`).

---

## Available Models

Sympozium works with all Vertex AI foundation models:

| Model | Type | Tool Calling | Notes |
|-------|------|--------------|-------|
| `gemini-2.0-flash` | Foundation | Yes | Latest, fastest Gemini model — recommended for most use cases |
| `gemini-1.5-pro` | Foundation | Yes | High-capability model for complex reasoning |
| `gemini-1.5-flash` | Foundation | Yes | Lightweight, cost-effective |
| `text-bison` | Legacy | No | Older model, not recommended for new deployments |

**Tool calling:** Sympozium agents rely on tool calling to execute commands, read files, and interact with the cluster. Use models with tool-calling support for full functionality.

---

## Google Chat Integration

To connect Sympozium to Google Chat for messaging:

1. Create a Google Chat bot with a service account (see [Channels guide](../concepts/channels.md))
2. Configure the channel during onboarding
3. Messages will route through the NATS event bus to your agent

---

## Serving Mode (Web Endpoint)

Expose a Vertex AI-backed agent as an HTTP API using the `web-endpoint` skill:

```yaml
apiVersion: sympozium.ai/v1alpha1
kind: SympoziumInstance
metadata:
  name: vertex-ai-server
spec:
  agents:
    default:
      model: gemini-2.0-flash
      provider: vertexai
      projectId: "YOUR_PROJECT_ID"
      location: "us-central1"
  authRefs:
    - provider: vertexai
      useWorkloadIdentity: true
  skills:
    - skillPackRef: web-endpoint
  policyRef: default-policy
```

The web proxy creates a Service and (optionally) an HTTPRoute with authentication and rate limiting. See [Web Endpoint Skill](../skills/web-endpoint.md) for full details.

---

## IAM Roles Required

For basic Vertex AI usage, the minimum required role is:

```
roles/aiplatform.user
```

For more granular control, use custom roles with these permissions:

```
aiplatform.endpoints.predict
aiplatform.models.getIamPolicy
aiplatform.models.list
```

---

## Quotas and Limits

Verify your Vertex AI quota in the GCP Console:

```bash
gcloud compute project-info describe --project=PROJECT_ID
```

Request quota increases if needed:

```bash
gcloud compute project-info describe --project=PROJECT_ID | grep -i quota
```

---

## Troubleshooting

### Authentication errors

```bash
# Test authentication
gcloud auth list
gcloud projects get-iam-policy PROJECT_ID --flatten="bindings[].members" --format="table(bindings.role)" --filter="bindings.members:*"
```

### Workload Identity not working

```bash
# Verify annotation
kubectl describe sa sympozium-agent -n sympozium-system

# Check pod
kubectl describe pod -n sympozium-system -l app=sympozium-agent
```

### Model access denied

Ensure the service account has the `roles/aiplatform.user` role:

```bash
gcloud projects get-iam-policy PROJECT_ID \
  --flatten="bindings[].members" \
  --filter="bindings.members:sympozium-agent@PROJECT_ID.iam.gserviceaccount.com"
```

### Slow responses

Vertex AI API latency depends on model, location, and load. To optimize:

1. Use `gemini-2.0-flash` for faster response times
2. Set appropriate request `timeout` (default `5m`)
3. Monitor Vertex AI metrics in Cloud Monitoring

---

## Cost Optimization

Vertex AI charges based on input and output tokens. To optimize costs:

- Use `gemini-2.0-flash` for cost-effective operations
- Keep request batches reasonably sized
- Monitor usage in Cloud Billing
- Set appropriate token limits in your agent system prompts

For pricing details, see [Vertex AI Pricing](https://cloud.google.com/vertex-ai/pricing).
