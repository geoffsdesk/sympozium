#!/usr/bin/env bash
set -euo pipefail

# ============================================================================
# Phase 3: Deploy Showcase Site + Grafana + Ingress
# ============================================================================
#
# Deploys:
#   - Grafana (anonymous read-only, GMP datasource, Sympozium dashboard)
#   - Showcase static site (Astro → nginx via Cloud Build)
#   - GKE Ingress with Google-managed TLS for geoffsdesk.com
#
# Run from WSL (from the repo root):
#   ./showcase/scripts/03-deploy-showcase.sh
# ============================================================================

export PROJECT_ID="first-cascade-490202-e3"
export DOMAIN="geoffsdesk.com"
export REGION="us-central1"
export ZONE="us-central1-a"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SHOWCASE_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"
REPO_ROOT="$(cd "${SHOWCASE_DIR}/.." && pwd)"

echo "================================================================"
echo "  Deploying Showcase Stack"
echo "  Domain:  ${DOMAIN}"
echo "  Project: ${PROJECT_ID}"
echo "================================================================"
echo ""

# -------------------------------------------------------------------
# Step 1: Create namespace
# -------------------------------------------------------------------
echo "→ Step 1: Creating showcase namespace..."
kubectl apply -f "${SHOWCASE_DIR}/k8s/namespace.yaml"

# -------------------------------------------------------------------
# Step 2: Create Grafana dashboard ConfigMap from actual JSON
# -------------------------------------------------------------------
echo "→ Step 2: Loading Grafana dashboard..."
kubectl create configmap grafana-dashboards \
  --from-file=sympozium-overview.json="${REPO_ROOT}/config/observability/grafana-dashboard.json" \
  --namespace=sympozium-showcase \
  --dry-run=client -o yaml | kubectl apply -f -

# -------------------------------------------------------------------
# Step 3: Deploy Grafana
# -------------------------------------------------------------------
echo "→ Step 3: Deploying Grafana..."
# Substitute project ID in datasource config
sed "s|YOUR_PROJECT_ID|${PROJECT_ID}|g" "${SHOWCASE_DIR}/k8s/grafana.yaml" | kubectl apply -f -

echo "  Waiting for Grafana..."
kubectl wait --for=condition=Available deployment/grafana \
  --namespace=sympozium-showcase \
  --timeout=120s 2>/dev/null || echo "  (still starting...)"

# -------------------------------------------------------------------
# Step 4: Build and push showcase site via Cloud Build
# -------------------------------------------------------------------
echo "→ Step 4: Building showcase site via Cloud Build..."
echo "  (This submits a build to Cloud Build — takes 2-3 minutes)"

# Update astro.config.mjs with actual domain
sed -i "s|sympozium.yourdomain.dev|${DOMAIN}|g" "${SHOWCASE_DIR}/astro.config.mjs"

gcloud builds submit "${SHOWCASE_DIR}" \
  --project="${PROJECT_ID}" \
  --tag="us-docker.pkg.dev/${PROJECT_ID}/sympozium/showcase-site:latest" \
  --timeout=600s

# -------------------------------------------------------------------
# Step 5: Deploy showcase site
# -------------------------------------------------------------------
echo "→ Step 5: Deploying showcase site..."
sed "s|SHOWCASE_PROJECT_ID|${PROJECT_ID}|g" "${SHOWCASE_DIR}/k8s/showcase-site.yaml" | kubectl apply -f -

echo "  Waiting for showcase site..."
kubectl wait --for=condition=Available deployment/showcase-site \
  --namespace=sympozium-showcase \
  --timeout=120s 2>/dev/null || echo "  (still starting...)"

# -------------------------------------------------------------------
# Step 6: Deploy Ingress with TLS
# -------------------------------------------------------------------
echo "→ Step 6: Deploying Ingress for ${DOMAIN}..."
sed "s|YOUR_DOMAIN_HERE|${DOMAIN}|g" "${SHOWCASE_DIR}/k8s/ingress.yaml" | kubectl apply -f -

# -------------------------------------------------------------------
# Step 7: Verify
# -------------------------------------------------------------------
echo ""
echo "→ Step 7: Checking deployment status..."
echo ""
echo "  Pods:"
kubectl get pods -n sympozium-showcase
echo ""
echo "  Services:"
kubectl get svc -n sympozium-showcase
echo ""
echo "  Ingress:"
kubectl get ingress -n sympozium-showcase
echo ""
echo "  Managed Certificate:"
kubectl get managedcertificate -n sympozium-showcase
echo ""

STATIC_IP=$(gcloud compute addresses describe sympozium-showcase \
  --global \
  --project="${PROJECT_ID}" \
  --format='value(address)' 2>/dev/null) || STATIC_IP="(run 01-setup-iam.sh first)"

echo "================================================================"
echo "  Showcase deployed!"
echo ""
echo "  Static IP:  ${STATIC_IP}"
echo "  Domain:     https://${DOMAIN}"
echo "  Grafana:    https://${DOMAIN}/grafana/"
echo "  API/UI:     https://${DOMAIN}/api/ui"
echo ""
echo "  ┌─────────────────────────────────────────────────────────┐"
echo "  │  IMPORTANT:                                             │"
echo "  │                                                         │"
echo "  │  1. Google-managed TLS takes 15-60 min to provision.    │"
echo "  │     Check: kubectl describe managedcertificate          │"
echo "  │            showcase-cert -n sympozium-showcase           │"
echo "  │                                                         │"
echo "  │  2. GoDaddy DNS A record must point to: ${STATIC_IP}   │"
echo "  │     (if not done already)                               │"
echo "  │                                                         │"
echo "  │  3. Until TLS is ready, HTTP works at:                  │"
echo "  │     http://${STATIC_IP}                                 │"
echo "  └─────────────────────────────────────────────────────────┘"
echo ""
echo "================================================================"
