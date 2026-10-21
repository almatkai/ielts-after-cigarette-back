# Google Drive Backup (Off-Host Disaster Recovery)

This document describes how to configure and run automated off-host backups to Google Drive for PostgreSQL database dumps and media snapshots.

## Overview

The backup container (`ops/backup`) supports uploading backups to:
1. **S3 / MinIO** via `BACKUP_S3_URI` and `BACKUP_MEDIA_S3_URI`.
2. **Google Drive** via `rclone` when `BACKUP_GDRIVE_ENABLED=true` (or when `BACKUP_GDRIVE_REMOTE` is defined).

When Google Drive backup is enabled, the Prometheus metric `iac_backup_offhost` is set to `1` upon a successful upload, fulfilling the off-host disaster recovery requirement and silencing the `BackupNotOffHost` alert.

## Configuration Options

Add the following variables to `operations.env`:

```env
# Enable Google Drive off-host backup
BACKUP_GDRIVE_ENABLED=true

# Remote paths on Google Drive (default: gdrive:iac-backups/postgres and gdrive:iac-backups/media)
BACKUP_GDRIVE_REMOTE=gdrive:iac-backups/postgres
BACKUP_MEDIA_GDRIVE_REMOTE=gdrive:iac-backups/media

# Remote retention in days on Google Drive (default: same as BACKUP_LOCAL_RETENTION_DAYS, e.g. 7 or 30)
BACKUP_GDRIVE_RETENTION_DAYS=7

# Rclone configuration
RCLONE_CONFIG_GDRIVE_TYPE=drive
RCLONE_CONFIG_GDRIVE_SCOPE=drive.file
RCLONE_CONFIG_GDRIVE_TOKEN={"access_token":"...","token_type":"Bearer","refresh_token":"...","expiry":"..."}
```

Alternatively, you can provide an `rclone.conf` file inside the `rclone_config` Docker volume mounted at `/root/.config/rclone/rclone.conf`.

## Obtaining the Google Drive OAuth Token

On your local workstation (where a web browser is available):

1. Run the rclone authorization command:
   ```sh
   rclone authorize "drive"
   ```
   Or use `rclone config create gdrive drive scope drive.file` to configure it interactively.

2. A browser window will open asking you to authenticate with your Google account.
   Review the permissions and click **Allow**.

3. The terminal will print a JSON token blob:
   ```json
   {"access_token":"ya29...","token_type":"Bearer","refresh_token":"1//...","expiry":"..."}
   ```

4. Copy this JSON string (as a single line or escaped) into `operations.env` as `RCLONE_CONFIG_GDRIVE_TOKEN`.

## Verification

To run a single backup and verify the upload:

```sh
docker compose --env-file production.env --env-file operations.env \
  -f docker-compose.production.yml -f docker-compose.observability.yml -f docker-compose.server.yml \
  run --rm -e BACKUP_ONCE=true backup
```

Expected result:
- Database dump `iac-*.dump` and sha256 checksum are created and uploaded to `gdrive:iac-backups/postgres`.
- Media snapshot `iac-media-*.tar.gz` and sha256 checksum are created and uploaded to `gdrive:iac-backups/media`.
- Heartbeat metric in `/status/backup.prom` records:
  ```
  iac_backup_last_success_timestamp_seconds <timestamp>
  iac_backup_offhost 1
  ```
