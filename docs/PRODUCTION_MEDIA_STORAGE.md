# Production media storage

Production IELTS stores media on `78.40.109.172` using
`OBJECT_STORAGE_BACKEND=filesystem`. The `production` GitHub environment uses
this value as well. Development can continue using its separate MinIO storage.

The production Compose project `ielts-api-production` mounts three persistent
Docker volumes. Each file retains its complete database `storage_key`, including
the `listening/`, `writing/`, or `speaking/` prefix:

| Volume | Container directory |
| --- | --- |
| `ielts-api-production_listening_media` | `/data/listening-media` |
| `ielts-api-production_writing_media` | `/data/writing-media` |
| `ielts-api-production_speaking_media` | `/data/speaking-media` |

On 2026-10-03, all 57 objects (290,539,011 bytes) from the alem.ai `ielts`
bucket were copied to these volumes. This includes all 51 media records in the
production database and six additional Speaking files retained from the source.
Database IDs and storage keys are unchanged. Every copied object was checked
using SHA-256; single-part source objects were also checked against their ETag.

The migration manifest, staging copy, verification result, and previous Compose
and environment files are in `~/ielts-api/media-migration-20261003` on the server.
Source objects remain in alem.ai as a recovery copy. They are not synchronized
with subsequent production uploads.

Back up all three media volumes together with the production database. Ordinary
container recreation and deployments retain these volumes. Do not remove them
with `docker compose down -v`.
