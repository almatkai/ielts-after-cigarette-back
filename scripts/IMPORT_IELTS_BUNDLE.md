# Bulk-import IELTS materials

`import-ielts-bundle.py` imports pre-authored Reading V1, Listening V1, and Writing JSON materials through the local admin API. It validates each item with the API parser first, uploads optional media, resumes safely, and never publishes.

## Run

Put a fresh admin access token in a local file (do not commit it):

```sh
umask 077
printf '%s' 'Bearer <fresh-admin-token>' > /tmp/iac-admin-token
```

Validate the bundle without writes:

```sh
python3 scripts/import-ielts-bundle.py scripts/cambridge16-import-manifest.example.json \
  --token-file /tmp/iac-admin-token --dry-run
```

Import or resume it:

```sh
python3 scripts/import-ielts-bundle.py scripts/cambridge16-import-manifest.example.json \
  --token-file /tmp/iac-admin-token
```

Filter by title when testing a subset:

```sh
python3 scripts/import-ielts-bundle.py bundle.json --token-file /tmp/iac-admin-token \
  --only 'Academic Reading Test 4'
```

The runner checks the admin catalog before each item and skips an existing exact-title DRAFT. It refuses to overwrite a PUBLISHED/ARCHIVED match. On an expired token, either rerun with a fresh token or use `--wait-auth 900`; in that mode the script waits for the token file to be replaced and retries the request.

## Manifest

Each item has a `kind` (`reading`, `listening`, or `writing`), a unique display `title`, and a `source` path. Reading additionally uses `examType`, `difficulty`, and optional `slugPrefix`, `sourceTitle`, `sourceUrl`; Listening may set `slug`; Writing selects the material whose exact title matches the item title.

An item may have a `media` mapping. Each media entry has a `path` and kind `writing_image`, `listening_audio`, or `listening_image`. Put `{{MEDIA:key}}` in the source where its uploaded UUID should go. For Listening this works in V1 `audio_asset_id` and `image_asset_id`; for Writing it works in the JSON `visualAssetId`.

The example manifest references the Cambridge 16 authoring files staged under `/tmp`. Add a manifest entry only after its source file is ready. The importer intentionally does not try to infer questions or answers from raw web pages; source extraction and authoring remain an explicit step.
