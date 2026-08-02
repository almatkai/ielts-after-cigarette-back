#!/usr/bin/env bash

set -euo pipefail

deploy_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$deploy_dir"

docker_config_dir="$deploy_dir/.docker"
install -d -m 700 "$docker_config_dir"
export DOCKER_CONFIG="$docker_config_dir"
cleanup() {
	rm -f "$docker_config_dir/config.json"
}
trap cleanup EXIT

chmod 600 production.env
compose=(docker compose --env-file production.env -f docker-compose.production.yml)

"${compose[@]}" pull backend
"${compose[@]}" up -d postgres redis
"${compose[@]}" run --rm -T migrate </dev/null
"${compose[@]}" up -d backend

for _ in $(seq 1 30); do
	if "${compose[@]}" exec -T backend wget -qO- http://localhost:8080/health/ready >/dev/null; then
		exit 0
	fi
	sleep 2
done

"${compose[@]}" ps -a
"${compose[@]}" logs --tail=100 backend
exit 1
