#!/bin/bash
# Applies a Kubernetes deployment, verifies health, and auto-rolls back on failure.
#
# Usage:
#   IMAGE=ghcr.io/owner/guardrail:<sha> EXPECTED_VERSION=<sha> ./scripts/deploy-and-verify.sh
#   ./scripts/deploy-and-verify.sh   # uses image already in k8s/deployment.yaml
#
# Env:
#   IMAGE=""             if set: `kubectl set image ...` (no YAML sed-editing)
#   EXPECTED_VERSION=""  if set: passed to health-check.sh to defeat
#                        Service-level false-positives from old pods.
#                        Defaults to IMAGE tag suffix when IMAGE is set.
#   NAMESPACE=""         kubectl namespace (empty = current context)
#   ROLLOUT_TIMEOUT=120s must be <= progressDeadlineSeconds in deployment.yaml
#   HEALTH_TIMEOUT=60
#   GRAFANA_ANNOTATE_URL="" optional: POST a deploy annotation (Grafana API)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

IMAGE="${IMAGE:-}"
EXPECTED_VERSION="${EXPECTED_VERSION:-}"
NAMESPACE="${NAMESPACE:-}"
ROLLOUT_TIMEOUT="${ROLLOUT_TIMEOUT:-120s}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-60}"
GRAFANA_ANNOTATE_URL="${GRAFANA_ANNOTATE_URL:-}"

NS_ARGS=()
if [ -n "$NAMESPACE" ]; then
  NS_ARGS=( -n "$NAMESPACE" )
fi

command -v kubectl >/dev/null || { echo "kubectl not found" >&2; exit 1; }

# Derive EXPECTED_VERSION from IMAGE tag when not given explicitly
# (ghcr.io/owner/guardrail:<sha> -> <sha>).
if [ -z "$EXPECTED_VERSION" ] && [ -n "$IMAGE" ]; then
  EXPECTED_VERSION="${IMAGE##*:}"
  if [ "$EXPECTED_VERSION" = "$IMAGE" ]; then
    EXPECTED_VERSION=""
  fi
fi

echo "=== Applying manifests ==="
kubectl apply "${NS_ARGS[@]}" -f "$REPO_ROOT/k8s/service.yaml"
kubectl apply "${NS_ARGS[@]}" -f "$REPO_ROOT/k8s/deployment.yaml"

if [ -n "$IMAGE" ]; then
  echo "=== Pinning image: ${IMAGE} ==="
  kubectl set image "${NS_ARGS[@]}" deployment/guardrail "guardrail=${IMAGE}"
  if [ -n "$EXPECTED_VERSION" ]; then
    kubectl set env "${NS_ARGS[@]}" deployment/guardrail "APP_VERSION=${EXPECTED_VERSION}"
  fi
fi

rollback_and_wait() {
  local reason="$1"
  echo "$reason — rolling back"
  kubectl rollout undo "${NS_ARGS[@]}" deployment/guardrail
  # Always wait so the pipeline never reports before the cluster healed.
  kubectl rollout status "${NS_ARGS[@]}" deployment/guardrail --timeout=120s
  kubectl rollout history "${NS_ARGS[@]}" deployment/guardrail | tail -5
  echo "Rollback complete. Previous stable version restored."
}

echo "=== Waiting for rollout (${ROLLOUT_TIMEOUT}) ==="
if ! kubectl rollout status "${NS_ARGS[@]}" deployment/guardrail --timeout="$ROLLOUT_TIMEOUT"; then
  rollback_and_wait "Rollout did not complete in time"
  exit 1
fi

echo "=== Running health check gate ==="
export TIMEOUT="$HEALTH_TIMEOUT"
export EXPECTED_VERSION
if "$SCRIPT_DIR/health-check.sh"; then
  echo "Deployment verified healthy. Success."
  kubectl rollout history "${NS_ARGS[@]}" deployment/guardrail | tail -5
  if [ -n "$GRAFANA_ANNOTATE_URL" ]; then
    curl -s --max-time 5 -X POST "$GRAFANA_ANNOTATE_URL" \
      -H 'Content-Type: application/json' \
      -d "{\"text\":\"deploy ${EXPECTED_VERSION:-unknown} healthy\"}" || echo "Grafana annotation failed (non-fatal)"
  fi
  exit 0
else
  rollback_and_wait "Health check failed"
  exit 1
fi
