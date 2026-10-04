# Listening production synchronization — 2026-10-03

The Listening content in `a1-postgres1.alem.ai` was compared with the production
PostgreSQL instance on `78.40.109.172`. All six Listening tables already matched
by ID and content. Only `created_by` / `updated_by` differ where the previous
migration mapped development authors to production accounts. No content rewrite
was necessary.

Verified inventory:

- 20 tests and 42 preserved historical versions.
- 168 versioned parts, 345 question groups, and 1,680 versioned questions.
- 30 Listening media records; the actual media files reside on the production
  server following the earlier 57-object storage migration.
- The admin API was checked against the source for all 20 current tests, 80
  current parts, and 800 current questions, including transcript text, segments,
  audio references, question content, answer keys, and explanations.
- The current source contains transcripts in five tests / 20 parts and answer
  timestamps in 200 questions. The other 15 tests have no transcripts in the
  source either; synchronization cannot supply data that has not been generated.

Production configuration now passes the working STT endpoint/key and the separate
AI alignment endpoint/key/model through the GitHub production environment, deploy
workflow, generated environment file, and Compose container configuration. Secrets
are stored in GitHub secrets and never in this report. The runtime image includes
ffmpeg for compressed-format conversion.

A read-only real-audio probe confirmed the source configuration returns transcript
text with timestamped segments and an AI question alignment with a quote and hint.
After deployment, the same probe is run inside the production container without
saving or publishing tests.

Production deployment commit: `c535d6a98b8eec7f16586f69e57d3c92f0eb3e8e`.
Development configuration commit: `80bd8e8`.

A full custom-format production database backup was made and its archive listing
validated:

`~/ielts-api/backups/before-stt-config-20261003.dump`

SHA-256: `5329b8d2360463d21fa30e13f7ddb525875f04df7a4b310a81b60507b09641f5`.

Source content, verification fingerprints, and API comparison results are stored
in `~/ielts-api/migration-check-20261003` on the server. The existing storage
migration manifest remains in `~/ielts-api/media-migration-20261003`.

This synchronization does not implement the remaining items in the STT audit:
post-submission data access, visible partial alignment failures, evidence
validation, background processing, and transcript generation for the missing tests.
