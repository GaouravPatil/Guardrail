#!/bin/bash
# Deploy guardrail and make sure it's actually healthy. Rolls back if not.
#
# Usage:
#   ./scripts/deploy-and-verify.sh
#   IMAGE=ghcr.io/owner/guardrail:<sha> ./scripts/deploy-and-verify.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

IMAGE="${IMAGE:-}"
EXPECTED_VERSION="${EXPECTED_VERSION:-}"
NAMESPACE="${NAMESPACE:-guardrail}"

# If image tag is ghcr.io/owner/guardrail:<sha>, expect that version back from /health.
if [ -z "$EXPECTED_VERSION" ] && [ -n "$IMAGE" ]; then
  EXPECTED_VERSION="${IMAGE##*:}"
fi

echo "=== Applying manifests ==="
kubectl apply -f "$REPO_ROOT/k8s/00-namespace.yaml"
for dir in 01-deployments 02-services 03-configs 04-secrets 05-pvcs 06-ingress; do
  kubectl apply -R -f "$REPO_ROOT/k8s/$dir"
done

if [ -n "$IMAGE" ]; then
  echo "=== Pinning image: ${IMAGE} ==="
  kubectl -n "$NAMESPACE" set image deployment/guardrail "guardrail=${IMAGE}"
  kubectl -n "$NAMESPACE" set env deployment/guardrail "APP_VERSION=${EXPECTED_VERSION}"
fi

rollback() {
  echo "$1 - rolling back"
  kubectl -n "$NAMESPACE" rollout undo deployment/guardrail
  kubectl -n "$NAMESPACE" rollout status deployment/guardrail --timeout=120s
}

echo "=== Waiting for rollout ==="
if ! kubectl -n "$NAMESPACE" rollout status deployment/guardrail --timeout=120s; then
  rollback "Rollout failed"
  exit 1
fi

echo "=== Health check ==="
export EXPECTED_VERSION
if "$SCRIPT_DIR/health-check.sh"; then
  echo "Deploy looks good."
else
  rollback "Health check failed"
  exit 1
fi
