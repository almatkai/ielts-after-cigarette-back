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
if [[ -f operations.env && ! -f ${overlay[0]} ]]; then
	echo "operations.env exists but an overlay file is missing; refusing partial deploy" >&2
	exit 1
fi
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

compose=(docker compose -f docker-compose.production.yml --env-file production.env)
if [[ $full_operational == true ]]; then
	compose+=(--env-file operations.env)
	for file in "${overlay[@]}"; do
		compose+=(-f "$file")
	done
	compose+=("${profiles[@]}")
fi

if [[ -f "$HOME/drone-league/monitoring/require-ielts-integration" ]]; then
    [[ -f "$HOME/drone-league/monitoring/redeploy_guard.py" ]] || { echo 'Required Drone monitoring guard is missing' >&2; exit 1; }
    [[ $full_operational == true ]] || { echo 'Drone monitoring requires operational overlays' >&2; exit 1; }
fi

pull_targets=(backend)
if [[ $full_operational == true ]]; then
	pull_targets+=(worker)
fi
# Databases first: if they need to boot, they now do it while the release
# image is still downloading instead of after it.
"${compose[@]}" up -d postgres redis

# Pull in the background so the migration container can start while layers
# are still downloading; `wait` aborts the deploy if the pull failed.
"${compose[@]}" pull "${pull_targets[@]}" &
pull_pid=$!

"${compose[@]}" run --rm -T migrate </dev/null
wait "$pull_pid"

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
			worker_id=$("${compose[@]}" ps -q worker | head -1)
			if [[ -z $worker_id ]] || [[ $(docker inspect -f '{{.State.Running}}' "$worker_id" 2>/dev/null) != true ]]; then
				echo "worker did not start" >&2
				"${compose[@]}" ps -a
				"${compose[@]}" logs --tail=50 worker
				exit 1
			fi
			# Alertbridge is built from the uploaded source. Prometheus reads
			# its bind-mounted rules at startup; recreate both so this release's
			# notification code and alert rules become active together.
			"${compose[@]}" up -d --no-deps --build --force-recreate prometheus alertbridge
		fi
        monitoring="$HOME/drone-league/monitoring"
        if [[ -f "$monitoring/require-ielts-integration" ]]; then
            [[ $full_operational == true ]] || { echo 'Drone monitoring requires the operational overlays' >&2; exit 1; }
            # Uploaded configuration is prepared before live replacement. Reload
            # Alloy in place; no new containers or networks are needed for it.
            docker exec ielts-api-production-alloy-1 alloy validate /etc/alloy/config.alloy
            docker kill --signal=HUP ielts-api-production-alloy-1 >/dev/null
            monitoring_ok=false
            for attempt in $(seq 1 6); do
                if timeout 40s python3 "$monitoring/redeploy_guard.py" verify --ielts-root "$deploy_dir"; then
                    monitoring_ok=true
                    break
                fi
                sleep 5
            done
            [[ $monitoring_ok == true ]] || { echo 'Drone monitoring verification failed' >&2; exit 1; }
        fi
		exit 0
	fi
	sleep 2
done

"${compose[@]}" ps -a
"${compose[@]}" logs --tail=100 backend worker 2>/dev/null || "${compose[@]}" logs --tail=100 backend
exit 1
