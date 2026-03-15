#!/usr/bin/env bash
set -euo pipefail

# ============================================================================
# Phase 1: Set up Workload Identity + IAM for Sympozium
# ============================================================================
#
# Creates:
#   - GCP service account for Sympozium
#   - IAM role bindings for Vertex AI, Pub/Sub, Secret Manager, Firestore, Monitoring
#   - Kubernetes service accounts
#   - Workload Identity binding (KSA → GSA)
#   - Static IP for the Ingress
#
# Run from WSL:
#   ./01-setup-iam.sh
# ============================================================================

export PROJECT_ID="first-cascade-490202-e3"
export REGION="us-central1"
export GSA_NAME="sympozium-sa"
export KSA_NAME="sympozium-sa"
export NAMESPACE="sympozium-system"
export SHOWCASE_NS="sympozium-showcase"
export DOMAIN="geoffsdesk.com"

echo "================================================================"
echo "  Setting up IAM & Workload Identity"
echo "  Project: ${PROJECT_ID}"
echo "================================================================"
echo ""

# -------------------------------------------------------------------
# Step 1: Create GCP service account
# -------------------------------------------------------------------
echo "→ Step 1: Creating GCP service account..."
gcloud iam service-accounts create "${GSA_NAME}" \
  --display-name="Sympozium Platform" \
  --project="${PROJECT_ID}" 2>/dev/null || echo "  (already exists)"

GSA_EMAIL="${GSA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"

# -------------------------------------------------------------------
# Step 2: Grant IAM roles
# -------------------------------------------------------------------
echo "→ Step 2: Granting IAM roles to ${GSA_EMAIL}..."

ROLES=(
  "roles/aiplatform.user"              # Vertex AI Gemini
  "roles/pubsub.editor"                # Cloud Pub/Sub
  "roles/secretmanager.secretAccessor"  # Secret Manager
  "roles/datastore.user"               # Firestore
  "roles/monitoring.metricWriter"       # Cloud Monitoring (metrics)
  "roles/cloudtrace.agent"             # Cloud Trace
  "roles/logging.logWriter"            # Cloud Logging
  "roles/monitoring.viewer"             # Grafana reads metrics
  "roles/artifactregistry.reader"       # Pull images
)

for ROLE in "${ROLES[@]}"; do
  echo "  Granting ${ROLE}..."
  gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
    --member="serviceAccount:${GSA_EMAIL}" \
    --role="${ROLE}" \
    --condition=None \
    --quiet 2>/dev/null
done

# -------------------------------------------------------------------
# Step 3: Create Kubernetes namespaces and service accounts
# -------------------------------------------------------------------
echo "→ Step 3: Creating Kubernetes namespaces and service accounts..."
kubectl create namespace "${NAMESPACE}" 2>/dev/null || true
kubectl create namespace "${SHOWCASE_NS}" 2>/dev/null || true

kubectl create serviceaccount "${KSA_NAME}" \
  --namespace="${NAMESPACE}" 2>/dev/null || true

kubectl annotate serviceaccount "${KSA_NAME}" \
  --namespace="${NAMESPACE}" \
  "iam.gke.io/gcp-service-account=${GSA_EMAIL}" \
  --overwrite

# Also create a KSA in the showcase namespace for Grafana
kubectl create serviceaccount grafana-sa \
  --namespace="${SHOWCASE_NS}" 2>/dev/null || true

kubectl annotate serviceaccount grafana-sa \
  --namespace="${SHOWCASE_NS}" \
  "iam.gke.io/gcp-service-account=${GSA_EMAIL}" \
  --overwrite

# -------------------------------------------------------------------
# Step 4: Bind Workload Identity (KSA → GSA)
# -------------------------------------------------------------------
echo "→ Step 4: Binding Workload Identity..."

gcloud iam service-accounts add-iam-policy-binding "${GSA_EMAIL}" \
  --role="roles/iam.workloadIdentityUser" \
  --member="serviceAccount:${PROJECT_ID}.svc.id.goog[${NAMESPACE}/${KSA_NAME}]" \
  --project="${PROJECT_ID}" \
  --quiet

gcloud iam service-accounts add-iam-policy-binding "${GSA_EMAIL}" \
  --role="roles/iam.workloadIdentityUser" \
  --member="serviceAccount:${PROJECT_ID}.svc.id.goog[${SHOWCASE_NS}/grafana-sa]" \
  --project="${PROJECT_ID}" \
  --quiet

# -------------------------------------------------------------------
# Step 5: Reserve static IP for Ingress
# -------------------------------------------------------------------
echo "→ Step 5: Reserving static IP for ${DOMAIN}..."
gcloud compute addresses create sympozium-showcase \
  --global \
  --project="${PROJECT_ID}" 2>/dev/null || echo "  (already exists)"

STATIC_IP=$(gcloud compute addresses describe sympozium-showcase \
  --global \
  --project="${PROJECT_ID}" \
  --format='value(address)')

echo ""
echo "================================================================"
echo "  IAM & Workload Identity configured!"
echo ""
echo "  GCP SA:     ${GSA_EMAIL}"
echo "  K8s SA:     ${KSA_NAME} (namespace: ${NAMESPACE})"
echo "  Static IP:  ${STATIC_IP}"
echo ""
echo "  ┌─────────────────────────────────────────────────────────┐"
echo "  │  ACTION REQUIRED: Go to GoDaddy DNS and add:           │"
echo "  │                                                         │"
echo "  │  Type: A                                                │"
echo "  │  Name: @                                                │"
echo "  │  Value: ${STATIC_IP}                              │"
echo "  │  TTL: 600                                               │"
echo "  │                                                         │"
echo "  │  This can propagate while we deploy everything else.    │"
echo "  └─────────────────────────────────────────────────────────┘"
echo ""
echo "  Next: Run ./02-deploy-sympozium.sh"
echo "================================================================"
