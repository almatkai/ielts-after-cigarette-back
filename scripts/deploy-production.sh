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

# Full operational set is preferred when the overlays exist on the server:
# external workers, backups, monitoring and the private metrics listener all
# run through this one flow, exactly like the manual operations wrapper.
overlay=(docker-compose.observability.yml docker-compose.pgstats.yml docker-compose.server.yml)
if [[ -f operations.env && -f ${overlay[0]} && -f ${overlay[1]} && -f ${overlay[2]} ]]; then
	chmod 600 operations.env
	profiles=(--profile workers --profile backups --profile monitoring)
	full_operational=true
else
	echo "operations overlay not found; deploying base production stack only" >&2
	overlay=()
	profiles=()
	full_operational=false
fi

compose=(docker compose --env-file production.env)
if [[ $full_operational == true ]]; then
	compose+=(--env-file operations.env)
	for file in "${overlay[@]}"; do
		compose+=(-f "$file")
	done
	compose+=("${profiles[@]}")
fi

pull_targets=(backend)
if [[ $full_operational == true ]]; then
	pull_targets+=(worker)
fi
"${compose[@]}" pull "${pull_targets[@]}"

"${compose[@]}" up -d postgres redis
"${compose[@]}" run --rm -T migrate </dev/null
"${compose[@]}" up -d backend
# Recreate with the same release image after backend, in dependency order.
if [[ $full_operational == true ]]; then
	"${compose[@]}" up -d --no-deps worker
fi

for _ in $(seq 1 30); do
	if "${compose[@]}" exec -T backend wget -qO- http://localhost:8080/health/ready >/dev/null; then
		if [[ $full_operational == true ]]; then
			# The external worker carries assessment jobs; a missing container
			# must fail the deploy rather than stall processing silently.
			if ! docker inspect -f '{{.State.Running}}' "$(docker compose --env-file production.env --env-file operations.env -f "${overlay[@]}" --profile workers --profile backups --profile monitoring ps -q worker | head -1)" >/dev/null 2>&1; then
				echo "worker did not start" >&2
				"${compose[@]}" ps -a
				"${compose[@]}" logs --tail=50 worker
				exit 1
			fi
		fi
		exit 0
	fi
	sleep 2
done

"${compose[@]}" ps -a
"${compose[@]}" logs --tail=100 backend worker 2>/dev/null || "${compose[@]}" logs --tail=100 backend
exit 1
