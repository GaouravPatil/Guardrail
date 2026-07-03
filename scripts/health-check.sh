#!/bin/bash
# Polls the app's /health endpoint after a deploy.
# Exits 0 if healthy within the timeout, exits 1 if not (triggers rollback).

set -e

TIMEOUT=60
INTERVAL=3
ELAPSED=0

# Port-forward in the background so this script is self-contained
kubectl port-forward svc/guardrail 5000:5000 > /dev/null 2>&1 &
PF_PID=$!
sleep 2   # give port-forward a moment to establish

cleanup() {
  kill $PF_PID 2>/dev/null
}
trap cleanup EXIT

echo "Checking health at http://localhost:5000/health ..."

while [ $ELAPSED -lt $TIMEOUT ]; do
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" "http://localhost:5000/health" || echo "000")

  if [ "$STATUS" = "200" ]; then
    echo "Healthy after ${ELAPSED}s"
    exit 0
  fi

  echo "Not healthy yet (status: $STATUS), waiting..."
  sleep $INTERVAL
  ELAPSED=$((ELAPSED + INTERVAL))
done

echo "Health check FAILED after ${TIMEOUT}s"
exit 1
