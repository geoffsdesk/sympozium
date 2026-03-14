#!/bin/bash
# setup-workload-identity.sh — Configure GKE Workload Identity for Sympozium
#
# This script sets up Workload Identity Federation so Sympozium pods can
# authenticate to GCP services (Vertex AI, Pub/Sub, Secret Manager) without
# managing service account keys.
#
# Prerequisites:
#   - gcloud CLI authenticated
#   - GKE cluster with Workload Identity enabled
#   - kubectl configured to target cluster
#
# Usage:
#   ./setup-workload-identity.sh <project-id> <namespace> [gcp-sa-name]

set -euo pipefail

PROJECT_ID="${1:?Usage: $0 <project-id> <namespace> [gcp-sa-name]}"
NAMESPACE="${2:?Usage: $0 <project-id> <namespace> [gcp-sa-name]}"
GCP_SA_NAME="${3:-sympozium-sa}"
K8S_SA_NAME="sympozium-controller"

echo "=== Sympozium Workload Identity Setup ==="
echo "Project:       $PROJECT_ID"
echo "Namespace:     $NAMESPACE"
echo "GCP SA:        $GCP_SA_NAME@$PROJECT_ID.iam.gserviceaccount.com"
echo "K8s SA:        $K8S_SA_NAME"
echo ""

# 1. Create GCP service account
echo "Creating GCP service account..."
gcloud iam service-accounts create "$GCP_SA_NAME" \
  --project="$PROJECT_ID" \
  --display-name="Sympozium Agent Platform" \
  --description="Service account for Sympozium AI agent orchestration" \
  2>/dev/null || echo "  (already exists)"

GCP_SA_EMAIL="$GCP_SA_NAME@$PROJECT_ID.iam.gserviceaccount.com"

# 2. Grant required IAM roles
echo "Granting IAM roles..."

declare -a ROLES=(
  "roles/aiplatform.user"              # Vertex AI (Gemini) inference
  "roles/pubsub.editor"                # Cloud Pub/Sub publish/subscribe
  "roles/secretmanager.secretAccessor"  # Secret Manager read
  "roles/logging.logWriter"            # Cloud Logging
  "roles/monitoring.metricWriter"      # Cloud Monitoring
  "roles/cloudtrace.agent"             # Cloud Trace
)

for role in "${ROLES[@]}"; do
  echo "  Granting $role..."
  gcloud projects add-iam-policy-binding "$PROJECT_ID" \
    --member="serviceAccount:$GCP_SA_EMAIL" \
    --role="$role" \
    --condition=None \
    --quiet 2>/dev/null
done

# 3. Create Kubernetes namespace if needed
echo "Ensuring namespace $NAMESPACE exists..."
kubectl create namespace "$NAMESPACE" 2>/dev/null || true

# 4. Create Kubernetes service account with annotation
echo "Creating Kubernetes service account..."
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: $K8S_SA_NAME
  namespace: $NAMESPACE
  annotations:
    iam.gke.io/gcp-service-account: $GCP_SA_EMAIL
  labels:
    app.kubernetes.io/name: sympozium
    app.kubernetes.io/component: controller
EOF

# Also create SA for agent runners
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: sympozium-agent-runner
  namespace: $NAMESPACE
  annotations:
    iam.gke.io/gcp-service-account: $GCP_SA_EMAIL
  labels:
    app.kubernetes.io/name: sympozium
    app.kubernetes.io/component: agent-runner
EOF

# Also create SA for channel pods
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: sympozium-channel
  namespace: $NAMESPACE
  annotations:
    iam.gke.io/gcp-service-account: $GCP_SA_EMAIL
  labels:
    app.kubernetes.io/name: sympozium
    app.kubernetes.io/component: channel
EOF

# 5. Bind K8s SA to GCP SA (Workload Identity binding)
echo "Creating Workload Identity binding..."
gcloud iam service-accounts add-iam-policy-binding "$GCP_SA_EMAIL" \
  --project="$PROJECT_ID" \
  --role="roles/iam.workloadIdentityUser" \
  --member="serviceAccount:$PROJECT_ID.svc.id.goog[$NAMESPACE/$K8S_SA_NAME]" \
  --quiet

gcloud iam service-accounts add-iam-policy-binding "$GCP_SA_EMAIL" \
  --project="$PROJECT_ID" \
  --role="roles/iam.workloadIdentityUser" \
  --member="serviceAccount:$PROJECT_ID.svc.id.goog[$NAMESPACE/sympozium-agent-runner]" \
  --quiet

gcloud iam service-accounts add-iam-policy-binding "$GCP_SA_EMAIL" \
  --project="$PROJECT_ID" \
  --role="roles/iam.workloadIdentityUser" \
  --member="serviceAccount:$PROJECT_ID.svc.id.goog[$NAMESPACE/sympozium-channel]" \
  --quiet

# 6. Enable required APIs
echo "Enabling required GCP APIs..."
gcloud services enable \
  aiplatform.googleapis.com \
  pubsub.googleapis.com \
  secretmanager.googleapis.com \
  cloudtrace.googleapis.com \
  monitoring.googleapis.com \
  logging.googleapis.com \
  artifactregistry.googleapis.com \
  container.googleapis.com \
  --project="$PROJECT_ID" \
  --quiet

echo ""
echo "=== Setup Complete ==="
echo ""
echo "Workload Identity is configured. Deploy Sympozium with:"
echo ""
echo "  helm install sympozium ./charts/sympozium \\"
echo "    --namespace $NAMESPACE \\"
echo "    --set gcp.projectId=$PROJECT_ID \\"
echo "    --set gcp.workloadIdentity.enabled=true \\"
echo "    --set gcp.workloadIdentity.gcpServiceAccount=$GCP_SA_EMAIL"
echo ""
echo "No API keys or service account key files needed!"
