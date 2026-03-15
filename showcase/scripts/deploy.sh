#!/usr/bin/env bash
set -euo pipefail

# ============================================================================
# Sympozium GCP Showcase — One-Command Deployment
# ============================================================================
#
# Deploys:
#   1. Sympozium platform (Helm chart → sympozium-system namespace)
#   2. Grafana with GMP datasource + Sympozium dashboards
#   3. Showcase static site (Astro → nginx)
#   4. GKE Ingress with Google-managed TLS
#
# Prerequisites:
#   - gcloud CLI authenticated
#   - kubectl configured for your GKE cluster
#   - Helm 3 installed
#   - A GKE cluster with:
#     - Workload Identity enabled
#     - Managed Prometheus enabled (for GMP)
#     - HTTP(S) load balancing enabled
#
# Usage:
#   export GCP_PROJECT=your-project-id
#   export DOMAIN=sympozium.yourdomain.dev
#   export GCP_REGION=us-central1
#   ./deploy.sh
# ============================================================================

: "${GCP_PROJECT:?Set GCP_PROJECT environment variable}"
: "${DOMAIN:?Set DOMAIN environment variable (e.g., sympozium.yourdomain.dev)}"
: "${GCP_REGION:=us-central1}"

REPO="us-docker.pkg.dev/${GCP_PROJECT}/sympozium"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
CHART_DIR="$(cd "${ROOT_DIR}/../charts/sympozium" && pwd)"

echo "================================================================"
echo "  Sympozium GCP Showcase Deployment"
echo "  Project:  ${GCP_PROJECT}"
echo "  Domain:   ${DOMAIN}"
echo "  Region:   ${GCP_REGION}"
echo "  Registry: ${REPO}"
echo "================================================================"
echo ""

# -------------------------------------------------------------------
# Step 1: Reserve static IP (idempotent)
# -------------------------------------------------------------------
echo "→ Reserving static IP..."
gcloud compute addresses create sympozium-showcase --global --project="${GCP_PROJECT}" 2>/dev/null || true
STATIC_IP=$(gcloud compute addresses describe sympozium-showcase --global --project="${GCP_PROJECT}" --format='value(address)')
echo "  Static IP: ${STATIC_IP}"
echo "  ⚠️  Point DNS A record for ${DOMAIN} → ${STATIC_IP}"
echo ""

# -------------------------------------------------------------------
# Step 2: Create Artifact Registry repo (idempotent)
# -------------------------------------------------------------------
echo "→ Creating Artifact Registry repository..."
gcloud artifacts repositories create sympozium \
  --repository-format=docker \
  --location=us \
  --project="${GCP_PROJECT}" 2>/dev/null || true

# -------------------------------------------------------------------
# Step 3: Build and push showcase site
# -------------------------------------------------------------------
echo "→ Building showcase site..."
cd "${ROOT_DIR}"
gcloud builds submit \
  --project="${GCP_PROJECT}" \
  --tag="${REPO}/showcase-site:latest" \
  --timeout=600s

# -------------------------------------------------------------------
# Step 4: Install Sympozium platform via Helm
# -------------------------------------------------------------------
echo "→ Installing Sympozium platform..."
helm upgrade --install sympozium "${CHART_DIR}" \
  --namespace sympozium-system \
  --create-namespace \
  --set gcp.projectId="${GCP_PROJECT}" \
  --set gcp.location="${GCP_REGION}" \
  --set gcp.workloadIdentity.enabled=true \
  --set observability.enabled=true \
  --set observability.gmp.enabled=true \
  --set apiserver.webUI.enabled=true \
  --wait --timeout=300s

# -------------------------------------------------------------------
# Step 5: Copy Grafana dashboard JSON to ConfigMap
# -------------------------------------------------------------------
echo "→ Creating Grafana dashboard ConfigMap..."
kubectl create namespace sympozium-showcase 2>/dev/null || true
kubectl create configmap grafana-dashboards \
  --from-file=sympozium-overview.json="${ROOT_DIR}/../config/observability/grafana-dashboard.json" \
  --namespace=sympozium-showcase \
  --dry-run=client -o yaml | kubectl apply -f -

# -------------------------------------------------------------------
# Step 6: Deploy Grafana
# -------------------------------------------------------------------
echo "→ Deploying Grafana..."
# Substitute GMP datasource URL with actual project ID
sed "s|YOUR_PROJECT_ID|${GCP_PROJECT}|g" "${ROOT_DIR}/k8s/grafana.yaml" | kubectl apply -f -

# -------------------------------------------------------------------
# Step 7: Deploy showcase site
# -------------------------------------------------------------------
echo "→ Deploying showcase site..."
sed "s|YOUR_PROJECT|${GCP_PROJECT}|g" "${ROOT_DIR}/k8s/showcase-site.yaml" | kubectl apply -f -

# -------------------------------------------------------------------
# Step 8: Deploy ingress with domain and TLS
# -------------------------------------------------------------------
echo "→ Deploying ingress..."
sed "s|YOUR_DOMAIN_HERE|${DOMAIN}|g" "${ROOT_DIR}/k8s/ingress.yaml" | kubectl apply -f -

# -------------------------------------------------------------------
# Done
# -------------------------------------------------------------------
echo ""
echo "================================================================"
echo "  Deployment complete!"
echo ""
echo "  Site:       https://${DOMAIN}"
echo "  Grafana:    https://${DOMAIN}/grafana/"
echo "  API/UI:     https://${DOMAIN}/api/ui"
echo "  Static IP:  ${STATIC_IP}"
echo ""
echo "  ⚠️  Google-managed TLS cert can take 15-60 minutes to provision."
echo "  ⚠️  Run: kubectl describe managedcertificate showcase-cert -n sympozium-showcase"
echo "================================================================"
