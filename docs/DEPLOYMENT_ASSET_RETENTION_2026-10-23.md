# Dev static assets retained across web replacements

Applies only to the isolated `ielts-guest-dev` Compose project in
`/home/almat/ielts-guest-dev`, public URL `https://ielts.woolet.cc/app/`.

On 2026-10-23 an already open tab still ran `index-BhF-oxyJ.js` from the blue
release. Replacing its web container with the white-logo release removed its
lazy chunks. Plan, mistakes, profile and the assistant then returned 404, and
router preloading failed. All seven reported files were recovered unchanged
from `iac-web:guest-dev-blue-aee711f`.

The gateway now serves `/app/assets/` from a persistent, read-only asset archive,
with fallback to the current web container. Content-hashed assets use
`Cache-Control: public, max-age=31536000, immutable`; HTML under `/app/` uses
`Cache-Control: no-store`. Missing assets remain 404 and never become SPA HTML.
Backend, worker, database, auth credentials and production are unchanged.

## Before every dev web replacement

Copy `ops/cloudflare-dev/archive-web-assets.py` to the private server's `bin/`
directory. Retain both the currently deployed image and the candidate image
**before** changing the web service image:

```sh
cd /home/almat/ielts-guest-dev
python3 bin/archive-web-assets.py iac-web:guest-dev-CURRENT iac-web:guest-dev-NEXT
# Update only the dev web image in compose.yml after the archive succeeds.
docker compose --env-file dev.env -f compose.yml up -d --no-deps web
docker compose --env-file dev.env -f compose.yml up -d --no-deps --force-recreate gateway
```

With no arguments the archiver seeds all locally available `iac-web:guest-dev-*`
images. It creates stopped export containers only; it never starts their apps.
It copies only public build assets, rejects symlinks and conflicting immutable
filenames, skips hidden metadata and publishes each new file atomically. Image
manifests in `asset-manifests/` record file sizes and SHA-256 hashes. The archive
stays outside image/release directories; do not clear it during a release or
Docker image cleanup.

The dev gateway's volume list includes:

```yaml
volumes:
  - ./nginx.conf:/etc/nginx/conf.d/default.conf:ro
  - ./static-assets:/srv/retained-static/app/assets:ro
```

`ops/cloudflare-dev/nginx-dev-assets.conf` supplies the two asset locations for
the existing dev server block. The normal `/app/` proxy location additionally
sets `Cache-Control: no-store` and retains the noindex header. Validate the
candidate with `nginx -t` on the dev Docker network before replacing the gateway.
Do not use `docker compose config` without `--quiet`: it can print secrets.

## Verification and rollback

Fetch old and current hashed JS through the public URL and verify HTTP 200,
JavaScript MIME and matching SHA-256. A deliberately absent asset must return
404. Replay an affected old entry document and navigate to plan, mistakes,
profile and chat while fetching its assets through the current gateway. This
checks an open-tab deployment transition, rather than only a fresh page load.

The incident's private server backup is
`releases/20261023-assets-retention/{compose.yml,nginx.conf}.before`.
Restore those gateway configurations and recreate only `gateway` to roll back.
Do not restore databases or remove retained assets as part of rollback.
