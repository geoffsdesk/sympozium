#!/usr/bin/env bash
set -euo pipefail

# ============================================================================
# Phase 0: Create GKE Cluster for Sympozium Showcase
# ============================================================================
#
# Creates a GKE Standard cluster with:
#   - Workload Identity enabled
#   - Managed Prometheus (GMP) enabled
#   - HTTP(S) load balancing enabled
#   - Network policy enabled
#   - Cost-optimized: e2-standard-4 nodes, autoscaling 1-5
#
# Run from WSL:
#   chmod +x 00-create-cluster.sh
#   ./00-create-cluster.sh
# ============================================================================

export PROJECT_ID="first-cascade-490202-e3"
export REGION="us-central1"
export ZONE="us-central1-a"
export CLUSTER_NAME="sympozium-cluster"

echo "================================================================"
echo "  Creating GKE Cluster: ${CLUSTER_NAME}"
echo "  Project:  ${PROJECT_ID}"
echo "  Region:   ${REGION}"
echo "================================================================"
echo ""

# -------------------------------------------------------------------
# Step 1: Set project
# -------------------------------------------------------------------
echo "→ Step 1: Setting active project..."
gcloud config set project "${PROJECT_ID}"

# -------------------------------------------------------------------
# Step 2: Enable required APIs
# -------------------------------------------------------------------
echo "→ Step 2: Enabling GCP APIs (this may take a minute)..."
gcloud services enable \
  container.googleapis.com \
  artifactregistry.googleapis.com \
  cloudbuild.googleapis.com \
  secretmanager.googleapis.com \
  aiplatform.googleapis.com \
  pubsub.googleapis.com \
  firestore.googleapis.com \
  sqladmin.googleapis.com \
  monitoring.googleapis.com \
  cloudtrace.googleapis.com \
  logging.googleapis.com \
  chat.googleapis.com \
  compute.googleapis.com \
  --project="${PROJECT_ID}"

# -------------------------------------------------------------------
# Step 3: Create Artifact Registry repository
# -------------------------------------------------------------------
echo "→ Step 3: Creating Artifact Registry repository..."
gcloud artifacts repositories create sympozium \
  --repository-format=docker \
  --location=us \
  --description="Sympozium container images" \
  --project="${PROJECT_ID}" 2>/dev/null || echo "  (already exists)"

# -------------------------------------------------------------------
# Step 4: Create GKE cluster
# -------------------------------------------------------------------
echo "→ Step 4: Creating GKE cluster (this takes 5-10 minutes)..."
gcloud container clusters create "${CLUSTER_NAME}" \
  --project="${PROJECT_ID}" \
  --zone="${ZONE}" \
  --num-nodes=3 \
  --machine-type=e2-standard-4 \
  --disk-size=50 \
  --enable-autoscaling \
  --min-nodes=1 \
  --max-nodes=5 \
  --workload-pool="${PROJECT_ID}.svc.id.goog" \
  --enable-managed-prometheus \
  --enable-network-policy \
  --enable-ip-alias \
  --release-channel=regular \
  --addons=HttpLoadBalancing,HorizontalPodAutoscaling \
  --scopes="gke-default,https://www.googleapis.com/auth/cloud-platform" \
  --labels="env=showcase,app=sympozium"

# -------------------------------------------------------------------
# Step 5: Get credentials
# -------------------------------------------------------------------
echo "→ Step 5: Getting cluster credentials..."
gcloud container clusters get-credentials "${CLUSTER_NAME}" \
  --zone="${ZONE}" \
  --project="${PROJECT_ID}"

# -------------------------------------------------------------------
# Step 6: Verify
# -------------------------------------------------------------------
echo "→ Step 6: Verifying cluster..."
kubectl cluster-info
kubectl get nodes
echo ""
echo "================================================================"
echo "  GKE Cluster created successfully!"
echo "  Cluster: ${CLUSTER_NAME}"
echo "  Zone:    ${ZONE}"
echo "  Nodes:   $(kubectl get nodes --no-headers | wc -l)"
echo ""
echo "  Next: Run ./01-setup-iam.sh"
echo "================================================================"
