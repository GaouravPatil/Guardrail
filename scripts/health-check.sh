#!/bin/bash
# Hit /health until it looks good. Exit 0 = healthy, 1 = not (triggers rollback).
#
# Env:
#   TIMEOUT=60            how long to keep trying (seconds)
#   EXPECTED_VERSION=""   if set, /health must report this version.
#                         Stops a false pass from old pods during a rollout.

set -euo pipefail

TIMEOUT="${TIMEOUT:-60}"
EXPECTED_VERSION="${EXPECTED_VERSION:-}"
NAMESPACE="${NAMESPACE:-guardrail}"

# Grab a free local port so we never clash.
PORT=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')  

kubectl -n "$NAMESPACE" port-forward svc/guardrail "$PORT:5000" >/dev/null 2>&1 &
PF_PID=$!

cleanup() {
  kill "$PF_PID" 2>/dev/null || true
}
trap cleanup EXIT INT TERM

sleep 3 # let port-forward come up

URL="http://127.0.0.1:$PORT/health"
echo "Checking $URL ... (timeout ${TIMEOUT}s)"

ELAPSED=0
while [ "$ELAPSED" -lt "$TIMEOUT" ]; do
  BODY_FILE="$(mktemp)"
  HTTP_CODE="$(curl -s --max-time 5 -o "$BODY_FILE" -w "%{http_code}" "$URL" 2>/dev/null || echo "000")"
  BODY="$(cat "$BODY_FILE")"
  rm -f "$BODY_FILE"

  if [ "$HTTP_CODE" = "200" ] && echo "$BODY" | grep -q '"healthy"'; then
    # No version asked for: any healthy pod is fine.
    if [ -z "$EXPECTED_VERSION" ]; then
      echo "Healthy after ${ELAPSED}s"
      exit 0
    fi
    # Version asked for: wait until the new pods actually serve.
    if echo "$BODY" | grep -q "$EXPECTED_VERSION"; then
      echo "Healthy at version ${EXPECTED_VERSION} after ${ELAPSED}s"
      exit 0
    fi
    echo "Healthy but old version, waiting for ${EXPECTED_VERSION}..."
  else
    echo "Not healthy yet (status: ${HTTP_CODE}), waiting..."
  fi

  sleep 3
  ELAPSED=$((ELAPSED + 3))
done

echo "Health check FAILED after ${TIMEOUT}s"
exit 1

