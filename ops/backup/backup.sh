#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

: "${PGHOST:?PGHOST is required}"
: "${PGUSER:?PGUSER is required}"
: "${PGDATABASE:?PGDATABASE is required}"
: "${PGPASSWORD:?PGPASSWORD is required}"
: "${BACKUP_S3_URI:?Set s3://bucket/prefix on an off-host storage provider}"
[[ "$BACKUP_S3_URI" == s3://* ]] || { echo 'BACKUP_S3_URI must start with s3://' >&2; exit 1; }
interval=${BACKUP_INTERVAL_SECONDS:-86400}
retention=${BACKUP_LOCAL_RETENTION_DAYS:-7}
[[ "$interval" =~ ^[0-9]+$ && "$interval" -ge 60 ]] || exit 1
[[ "$retention" =~ ^[0-9]+$ && "$retention" -ge 1 ]] || exit 1
backup_dir=${BACKUP_DIR:-/backups}
status_dir=${BACKUP_STATUS_DIR:-/status}
mkdir -p "$backup_dir" "$status_dir"
work=''
trap '[[ -z "$work" ]] || rm -rf "$work"' EXIT
trap 'exit 0' TERM INT

backup_once() {
  work=$(mktemp -d "$backup_dir/.partial.XXXXXX") || return 1
  name="iac-$(date -u +%Y%m%dT%H%M%SZ)-${RANDOM}.dump"
  # Any failure returns without publishing a partial artifact or heartbeat.
  pg_dump --format=custom --no-owner --no-acl --file="$work/$name" || return 1
  pg_restore --list "$work/$name" >/dev/null || return 1
  (cd "$work" && sha256sum "$name" >"$name.sha256") || return 1
  if [[ "${BACKUP_MEDIA_ENABLED:-false}" == true ]]; then
    : "${BACKUP_MEDIA_S3_URI:?BACKUP_MEDIA_S3_URI is required for media snapshots}"
    media_name="iac-media-${name#iac-}"
    media_name="${media_name%.dump}.tar.gz"
    tar -C "${BACKUP_MEDIA_DIR:-/media}" -czf "$work/$media_name" listening writing speaking || return 1
    (cd "$work" && sha256sum "$media_name" >"$media_name.sha256") || return 1
  fi
  set -- aws
  [[ -z "${AWS_ENDPOINT_URL:-}" ]] || set -- "$@" --endpoint-url "$AWS_ENDPOINT_URL"
  for artifact in "$work"/*; do
    destination=${BACKUP_S3_URI%/}
    [[ "$(basename "$artifact")" != iac-media-* ]] || destination=${BACKUP_MEDIA_S3_URI%/}
    "$@" s3 cp "$artifact" "$destination/$(basename "$artifact")" --only-show-errors || return 1
  done
  mv "$work"/* "$backup_dir/" || return 1
  rm -rf "$work"; work=''
  # Success means all artifacts were uploaded, not necessarily off-host DR.
  offhost=0
  [[ "${BACKUP_OFFHOST:-true}" != true ]] || offhost=1
  printf 'iac_backup_last_success_timestamp_seconds %s\niac_backup_offhost %s\n' "$(date +%s)" "$offhost" >"$status_dir/backup.prom.tmp" || return 1
  chmod 644 "$status_dir/backup.prom.tmp" || return 1
  mv "$status_dir/backup.prom.tmp" "$status_dir/backup.prom" || return 1
  find "$backup_dir" -maxdepth 1 -type f \( -name 'iac-*.dump' -o -name 'iac-*.dump.sha256' -o -name 'iac-media-*.tar.gz' -o -name 'iac-media-*.tar.gz.sha256' \) -mtime "+$retention" -delete
  echo "$(date -u +%FT%TZ) backup uploaded: $name"
}

while true; do
  if backup_once; then delay=$interval; result=0
  else
    echo "$(date -u +%FT%TZ) backup FAILED (no success heartbeat written)" >&2
    [[ -z "$work" ]] || rm -rf "$work"; work=''
    delay=300; result=1
  fi
  [[ "${BACKUP_ONCE:-false}" != true ]] || exit "$result"
  sleep "$delay" & wait $!
done
