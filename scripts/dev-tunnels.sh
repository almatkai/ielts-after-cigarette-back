#!/usr/bin/env bash
# Supervises SSH tunnels from localhost to the dev stack on the server
# (docker network 192.168.64.0/24 of /home/almat/ielts-api-dev):
#   127.0.0.1:5432 -> postgres container (192.168.64.2:5432)
#   127.0.0.1:6379 -> redis container    (192.168.64.3:6379)
# Note: container IPs change if the compose stack is recreated.
# Run with e.g. `scripts/dev-tunnels.sh` and leave in the background.
set -u

SSH_SERVER="almat@78.40.109.172"
SSH_PORT="9865"

echo "[dev-tunnels] supervising tunnels ${SSH_SERVER}:${SSH_PORT} (pg 5432, redis 6379)"
while true; do
  # Standby mode: if both ports are already forwarded (e.g. a manual
  # tunnel), just wait and re-check.
  if nc -z 127.0.0.1 5432 2>/dev/null && nc -z 127.0.0.1 6379 2>/dev/null; then
    sleep 15
    continue
  fi
  ssh -N \
    -o BatchMode=yes \
    -o ExitOnForwardFailure=yes \
    -o ServerAliveInterval=30 \
    -o ServerAliveCountMax=3 \
    -o ConnectTimeout=10 \
    -p "${SSH_PORT}" \
    -L 127.0.0.1:5432:192.168.64.2:5432 \
    -L 127.0.0.1:6379:192.168.64.3:6379 \
    "${SSH_SERVER}" \
    && echo "[dev-tunnels] ssh exited cleanly, restarting" \
    || echo "[dev-tunnels] ssh died (exit $?), restarting in 3s"
  sleep 3
done
