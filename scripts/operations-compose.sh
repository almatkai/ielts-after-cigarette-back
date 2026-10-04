#!/usr/bin/env bash
# Server operational stack: secrets stay in protected env files, never arguments.
set -Eeuo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"
for file in production.env operations.env docker-compose.production.yml docker-compose.observability.yml docker-compose.pgstats.yml docker-compose.server.yml; do
  [[ -f "$file" ]] || { echo "Missing operational deployment file: $file" >&2; exit 1; }
done
chmod 600 production.env operations.env
exec docker compose --env-file production.env --env-file operations.env \
  -f docker-compose.production.yml -f docker-compose.observability.yml \
  -f docker-compose.pgstats.yml -f docker-compose.server.yml \
  --profile workers --profile backups --profile monitoring "$@"
