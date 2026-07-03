#!/bin/bash
# Applies a Kubernetes deployment, verifies health, and auto-rolls back on failure.

set -e

echo "=== Applying deployment ==="
kubectl apply -f k8s/deployment.yaml

echo "=== Waiting for rollout to complete ==="
if ! kubectl rollout status deployment/guardrail --timeout=90s; then
  echo "Rollout did not complete in time — rolling back"
  kubectl rollout undo deployment/guardrail
  exit 1
fi

echo "=== Running health check gate ==="
if ./scripts/health-check.sh; then
  echo "Deployment verified healthy. Success."
  exit 0
else
  echo "Health check failed — rolling back to previous version"
  kubectl rollout undo deployment/guardrail
  kubectl rollout status deployment/guardrail --timeout=60s
  echo "Rollback complete. Previous stable version restored."
  exit 1
fi
