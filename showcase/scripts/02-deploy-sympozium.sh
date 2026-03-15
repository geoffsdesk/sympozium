#!/usr/bin/env bash
set -euo pipefail

# ============================================================================
# Phase 2: Deploy Sympozium Platform via Helm
# ============================================================================
#
# Installs the Sympozium Helm chart with:
#   - Workload Identity enabled
#   - Observability + GMP enabled
#   - Web UI enabled
#   - Cloud Pub/Sub as event bus
#
# Run from WSL (from the repo root):
#   ./showcase/scripts/02-deploy-sympozium.sh
# ============================================================================

export PROJECT_ID="first-cascade-490202-e3"
export REGION="us-central1"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
CHART_DIR="${REPO_ROOT}/charts/sympozium"

echo "================================================================"
echo "  Deploying Sympozium Platform"
echo "  Project: ${PROJECT_ID}"
echo "  Chart:   ${CHART_DIR}"
echo "================================================================"
echo ""

# -------------------------------------------------------------------
# Step 1: Install cert-manager (required for webhook TLS)
# -------------------------------------------------------------------
echo "→ Step 1: Installing cert-manager..."
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.14.5/cert-manager.yaml 2>/dev/null || true

echo "  Waiting for cert-manager to be ready..."
kubectl wait --for=condition=Available deployment/cert-manager \
  --namespace=cert-manager \
  --timeout=120s 2>/dev/null || echo "  (waiting...)"
kubectl wait --for=condition=Available deployment/cert-manager-webhook \
  --namespace=cert-manager \
  --timeout=120s 2>/dev/null || echo "  (waiting...)"

echo "  cert-manager is ready."

# -------------------------------------------------------------------
# Step 2: Install Sympozium via Helm
# -------------------------------------------------------------------
echo "→ Step 2: Installing Sympozium Helm chart..."

helm upgrade --install sympozium "${CHART_DIR}" \
  --namespace sympozium-system \
  --create-namespace \
  --set gcp.projectId="${PROJECT_ID}" \
  --set gcp.location="${REGION}" \
  --set gcp.workloadIdentity.enabled=true \
  --set gcp.workloadIdentity.serviceAccount="sympozium-sa" \
  --set gcp.workloadIdentity.gcpServiceAccount="sympozium-sa@${PROJECT_ID}.iam.gserviceaccount.com" \
  --set gcp.secretManager.enabled=true \
  --set gcp.artifactRegistry.enabled=true \
  --set gcp.artifactRegistry.repository="us-docker.pkg.dev/${PROJECT_ID}/sympozium" \
  --set pubsub.projectId="${PROJECT_ID}" \
  --set observability.enabled=true \
  --set observability.gmp.enabled=true \
  --set observability.gmp.alerting.enabled=true \
  --set apiserver.webUI.enabled=true \
  --set nodeProbe.enabled=true \
  --set defaultSkills.enabled=true \
  --set defaultPersonas.enabled=true \
  --set defaultPolicies.enabled=true \
  --timeout=300s \
  --wait

# -------------------------------------------------------------------
# Step 3: Verify deployment
# -------------------------------------------------------------------
echo "→ Step 3: Verifying Sympozium deployment..."
echo ""
echo "  Pods:"
kubectl get pods -n sympozium-system
echo ""
echo "  Services:"
kubectl get svc -n sympozium-system
echo ""
echo "  CRDs:"
kubectl get crd | grep sympozium || echo "  (CRDs installed)"
echo ""

# -------------------------------------------------------------------
# Step 4: Get UI token
# -------------------------------------------------------------------
echo "→ Step 4: Retrieving Web UI token..."
UI_TOKEN=$(kubectl get secret sympozium-ui-token \
  -n sympozium-system \
  -o jsonpath='{.data.token}' 2>/dev/null | base64 -d) || UI_TOKEN="(not found - check manually)"

echo ""
echo "================================================================"
echo "  Sympozium deployed!"
echo ""
echo "  Namespace:  sympozium-system"
echo "  UI Token:   ${UI_TOKEN}"
echo ""
echo "  To port-forward the UI locally:"
echo "    kubectl port-forward svc/sympozium-apiserver 8080:8080 -n sympozium-system"
echo "    Then open: http://localhost:8080/ui"
echo ""
echo "  Next: Run ./03-deploy-showcase.sh"
echo "================================================================"
