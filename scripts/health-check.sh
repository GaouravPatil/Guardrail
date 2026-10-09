#!/bin/bash
# Polls the app's /health endpoint after a deploy.
# Exits 0 if healthy within the timeout, exits 1 if not (triggers rollback).
#
# Env overrides:
#   TIMEOUT=60          total wait budget (seconds)
#   INTERVAL=3          poll interval (seconds)
#   NAMESPACE=""        kubectl namespace (empty = current context)
#   SERVICE=guardrail   Service name to port-forward
#   HEALTH_PATH=/health
#   EXPECTED_VERSION="" if set, response "version" must equal it.
#                       Prevents a false-green where the Service still serves
#                       old pods during a rollout.
#   LOCAL_PORT=""       local bind port (empty = auto-pick a free one)

set -euo pipefail

TIMEOUT="${TIMEOUT:-60}"
INTERVAL="${INTERVAL:-3}"
NAMESPACE="${NAMESPACE:-}"
SERVICE="${SERVICE:-guardrail}"
HEALTH_PATH="${HEALTH_PATH:-/health}"
EXPECTED_VERSION="${EXPECTED_VERSION:-}"
LOCAL_PORT="${LOCAL_PORT:-}"

NS_ARGS=()
if [ -n "$NAMESPACE" ]; then
  NS_ARGS=( -n "$NAMESPACE" )
fi

command -v kubectl >/dev/null || { echo "kubectl not found" >&2; exit 1; }
command -v curl >/dev/null || { echo "curl not found" >&2; exit 1; }

pick_free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])'
}

if [ -z "$LOCAL_PORT" ]; then
  LOCAL_PORT="$(pick_free_port)"
fi

PF_LOG="$(mktemp)"
kubectl port-forward "${NS_ARGS[@]}" "svc/${SERVICE}" "${LOCAL_PORT}:5000" >"$PF_LOG" 2>&1 &
PF_PID=$!

cleanup() {
  local code=$?
  kill "$PF_PID" 2>/dev/null || true
  wait "$PF_PID" 2>/dev/null || true
  rm -f "$PF_LOG"
  exit "$code"
}
trap cleanup EXIT INT TERM

# Wait for port-forward to actually listen (not a fixed sleep).
READY=0
for _ in $(seq 1 15); do
  if (echo >/dev/tcp/127.0.0.1/"$LOCAL_PORT") 2>/dev/null; then
    READY=1
    break
  fi
  # Bail early if port-forward already died (bad context, no Service, ...).
  if ! kill -0 "$PF_PID" 2>/dev/null; then
    echo "kubectl port-forward failed to start:" >&2
    cat "$PF_LOG" >&2 || true
    exit 1
  fi
  sleep 1
done
if [ "$READY" -ne 1 ]; then
  echo "port-forward never became ready:" >&2
  cat "$PF_LOG" >&2 || true
  exit 1
fi

URL="http://127.0.0.1:${LOCAL_PORT}${HEALTH_PATH}"
echo "Checking health at ${URL} ... (timeout ${TIMEOUT}s)"
if [ -n "$EXPECTED_VERSION" ]; then
  echo "Expecting version: ${EXPECTED_VERSION}"
fi

ELAPSED=0
while [ "$ELAPSED" -lt "$TIMEOUT" ]; do
  BODY_FILE="$(mktemp)"
  # --max-time bounds each attempt so the loop budget is honest.
  HTTP_CODE="$(curl -s --max-time 5 -o "$BODY_FILE" -w "%{http_code}" "$URL" 2>/dev/null || echo "000")"
  BODY="$(cat "$BODY_FILE")"
  rm -f "$BODY_FILE"

  if [ "$HTTP_CODE" = "200" ]; then
    # Body must actually say healthy; a 200 with wrong payload is not healthy.
    if echo "$BODY" | grep -q '"status"[[:space:]]*:[[:space:]]*"healthy"'; then
      if [ -n "$EXPECTED_VERSION" ]; then
        if echo "$BODY" | grep -q "\"version\"[[:space:]]*:[[:space:]]*\"${EXPECTED_VERSION}\""; then
          echo "Healthy at version ${EXPECTED_VERSION} after ${ELAPSED}s"
          exit 0
        fi
        echo "200/healthy but version mismatch (waiting for ${EXPECTED_VERSION}), got: ${BODY}"
      else
        echo "Healthy after ${ELAPSED}s"
        exit 0
      fi
    else
      echo "200 but unexpected body: ${BODY}"
    fi
  else
    echo "Not healthy yet (status: ${HTTP_CODE}), waiting..."
  fi

  sleep "$INTERVAL"
  ELAPSED=$((ELAPSED + INTERVAL))
done

echo "Health check FAILED after ${TIMEOUT}s"
exit 1
