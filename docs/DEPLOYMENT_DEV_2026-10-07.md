# IELTS Cloudflare development — 2026-10-07

Public entry: https://ielts.woolet.cc/ (redirects to `/app/`).

Cloudflare Worker `ielts-dev` owns the custom domain and HTTPS certificate.
It proxies through Workers VPC service `01a114c5-ec46-7253-ac99-7f8020e8b453`
and named Tunnel `b945e3c5-ccaf-4a88-896e-9d49cd65ee11`
(`ielts-dev-20261007`) to the private gateway at `127.0.0.1:18097`.
Source/configuration: `ops/cloudflare-dev/`. Public responses have `noindex`
and the development environment header. The Worker replaces incoming proxy
headers with Cloudflare's client address; its origin URL is fixed.

Application containers, PostgreSQL 17, Redis 8, and private MinIO run in the
separate Compose project `ielts-guest-dev`, directory
`/home/almat/ielts-guest-dev` on `ssh -p 9865 almat@78.40.109.172`.
No production container, volume, database, nginx site, or environment was changed.
This is Cloudflare's public entry with a server-backed application, not a
migration of the Go/PostgreSQL backend into Workers or D1.

## Data and configuration

- Fresh custom-format dump of production PostgreSQL restored only into the new
  `ielts_dev` database; migrations 35–38 applied.
- Production's IELTS media uses filesystem Docker volumes. MinIO on that host
  is backup storage, so active Listening/Writing objects were copied from
  those volumes into the new private bucket `ielts-dev-media` instead.
- Student identities/profiles were anonymized; attempts, answers, recordings,
  phone verification state, refresh sessions and usage history were cleared.
  Admin/editor identities and published materials were preserved. Password
  login hashes were disabled in the cloned database; admin Google sign-in
  may require this origin in the Google OAuth client's authorized origins.
- Existing AI/STT credentials were copied privately for assessment; JWT,
  PostgreSQL, Redis, phone-verification and MinIO credentials are independent.
  SMS/WhatsApp sending and production error reporting are disabled.
- Turnstile managed widget `0x4AAAAAAFP9t4vr1StfYDiU`, restricted to
  `ielts.woolet.cc`, verifies `guest_mock` action server-side.
- Guest quotas: 3 starts/IP and 20 starts globally per 24-hour window; one
  session/identity, 7-day access. These are trial quotas, not a monetary cap.
- The normal dashboard and catalogs are open to anonymous browsing. Private
  pages and other test launches show a sign-in prompt; AI chat is hidden.
- Academic trial sections are reserved in `guest_mock_materials` and pinned to
  the previously smoke-tested mock `dabe29c7-46dd-4bad-9c64-dc593d847091`.
  Every new guest uses those four versions. Existing trials remain intact.
  Reserved materials are excluded from public practice catalogs, new practice
  starts and ordinary generated mocks, even when other banks are exhausted.

Private files on the SSH host: `dev.env`, `app.env`, `turnstile.json`,
`tunnel-token`, `snapshot/`, `release/`. The parent directory is mode 0700;
environment/Turnstile files are 0600. `tunnel-token` is 0444 inside this private
directory so the non-root cloudflared container can read its mounted secret.
No secret or raw database dump is stored in this repository.

## Operations

```sh
ssh -p 9865 almat@78.40.109.172
cd /home/almat/ielts-guest-dev
docker compose --env-file dev.env -f compose.yml ps
docker compose --env-file dev.env -f compose.yml logs --tail=50 backend worker tunnel
docker compose --env-file dev.env -f compose.yml up -d --no-deps backend worker web
docker compose --env-file dev.env -f compose.yml up -d --no-deps --force-recreate gateway
```

Recreate the gateway after replacing application containers: nginx resolves their
Docker addresses at startup, and stale addresses can otherwise return HTTP 502.

The application images are `iac-backend:guest-dev-20261007` and
`iac-web:guest-dev-20261007`, built from the local uncommitted guest-trial
implementation. Rebuild API/worker for Linux amd64 and web with
`VITE_API_BASE_URL=https://ielts.woolet.cc`, then replace the private release
bundle and rebuild these images. Do not re-run snapshot restore over an active
dev database: `restore.py` refuses once `restore-complete` exists.

To stop development without deleting its data:
`docker compose --env-file dev.env -f compose.yml stop`.
Redeploy only the edge Worker from this checkout with
`npx wrangler deploy --config ops/cloudflare-dev/wrangler.jsonc`.

References: [Workers VPC](https://developers.cloudflare.com/workers-vpc/get-started/),
[Worker custom domains](https://developers.cloudflare.com/workers/configuration/routing/custom-domains/),
[Turnstile](https://developers.cloudflare.com/turnstile/get-started/widget-management/api/).

## Verified on the deployed environment

- All eight dev containers started; API readiness and PostgreSQL health pass.
- Public root redirects to `/app/`, HTML/assets and guest config return 200
  through the Cloudflare Worker and private tunnel.
- Academic draw pins four real sections. Cookie restoration and repeated starts
  return the same mock, including after completion.
- Bound audio supports HTTP 206; anonymous audio, guest profile/history and AI chat are rejected. Anonymous catalogs expose
  published summaries; questions and other test launches stay protected. Foreign sessions are not returned. Wrong Origins
  and invalid Turnstile tokens are rejected.
- All 51 database-linked Listening/Writing objects exist in the new MinIO
  bucket and their byte sizes match the restored database.
- One synthetic full mock completed through the real API: Listening/Reading
  scoring, queued Writing assessment (band 8), uploaded Speaking audio/STT and
  Speaking assessment (band 6), final result and reviews. Speaking's first
  model response failed validation; retry succeeded. Pronunciation remains
  unavailable with the existing text-only provider configuration.
- Live browser rendered the guest page and real Turnstile checkbox. Cloudflare
  rejected the automated browser's verification, so a successful live human
  challenge was not claimed. The async-script initialization bug discovered
  during this check was fixed and redeployed.
- Six guest browser regressions and ten registered-user mock regressions pass,
  including inline Turnstile, public catalog locks and mobile navigation. Frontend build, TypeScript/scoped ESLint, backend
  guest/config/app tests and diff checks pass.

## Update: normal anonymous browsing and fixed trial

Migration 36 adds the reservation table. The update uses the four material/version
pairs from the previously verified trial and does not reset existing sessions.
`/app/try` remains a compatibility entry redirecting to the regular dashboard.
The trial card starts directly after Turnstile, with inline linked terms and no
exam chooser or consent checkbox. Account-only pages display sign-in prompts
inside the normal dashboard shell. The reserved four materials are excluded from
normal catalogs and generated exams for every registered user.

Integration checks cover identical sections for separate guests, pinned versions
after archiving, exclusion after exhaustion, summary filtering and denied new
practice starts. Browser regressions cover anonymous catalog browsing without
private API reads, full trial review/restoration, inline Turnstile, fixed Academic
launch, mobile navigation and the existing registered-user mock flow.

The frontend sets Nitro `baseURL: '/app/'` in `vite.config.ts`, matching Vite's
asset prefix and TanStack Router's basepath. This is part of the source config;
releases do not depend on a shell-only `NITRO_APP_BASE_URL` override. Live checks
must fetch the emitted JS/CSS assets as well as HTML to detect prefix mismatch.

## Update: Full Mock session overview spacing

The active session overview is vertically centered with responsive page padding.
Its exam label maps the API values to `Academic` or `General Training` rather
than exposing the lowercase enum.

## Update: opening sections and separate timers

The exam session route renders its nested section through `Outlet`; its overview
is now an index route. Opening a section mounts its runner and loads its material.
Link preloading is disabled so hovering does not start a timed section.

Migration 37 stores a start/deadline for each section. First opening starts only
that clock, and repeated opening/reloads preserve it. Listening, Reading and
Writing use the pinned material's duration; Speaking has a 15-minute section
limit in addition to its existing part timers. Advancing unlocks the next section
without starting its clock. The overall mock has no countdown/deadline.

Expiry grades the current section's saved answers and permits advancing. Empty
Writing/Speaking sections close without a band. Existing attempts and answers
are preserved; an active section without a stored clock starts on opening.

Twenty-six browser checks pass, including real nested-route mounting, all four
runner timers, ignored stale browser timer values, and preservation after reload.
Go checks against isolated PostgreSQL schemas cover expiry, saved-answer grading,
late-answer rejection, fresh next-section clocks, and empty AI-section expiry.

Deployed migration 37 is clean. Live API checks and a browser against the public
Cloudflare URL opened all four runners with an isolated, seeded guest cookie:
Listening 40 minutes, Reading 60, Writing 60, Speaking 15. Hover did not start
clocks; clicking started the runner; reloading preserved each stored deadline.
Forced expiry allowed moving to the next section with its full time. Listening
media returned HTTP 206. The browser reported no page errors. Temporary probe
users/sessions were removed; these checks did not invoke paid AI or prove a
successful human Turnstile challenge. All 33 local guest, full-mock and Google
sign-in browser checks pass; an existing standalone-login hydration warning
remains in the development-server logs.

The trial card now describes four sections instead of a misleading hard-coded
165-minute total, and explains that each timer starts when its section opens.

## Update: Continue Later pauses the section

Migration 38 stores the remaining milliseconds when the current section is
paused, clearing its active deadline. `POST /full-mock-sessions/{sessionID}/pause`
is owner-scoped and available to authenticated and guest sessions; guests still
require a valid Origin. Remaining time is computed by PostgreSQL, never supplied
by the client. Duplicate pauses preserve the same amount, paused attempts cannot
be modified/submitted, and expired sections cannot be paused to avoid grading.

«Продолжить позже» flushes pending answers/audio, awaits the server pause, clears
the cached section runner and returns to the overview. Save/pause failures keep
the runner open and show an error. The overview displays a fixed «На паузе» time
and also offers «На паузу» for a currently running section. Opening resumes from
the saved time, without resetting the original start or touching other sections.
Closing the browser without pausing still consumes time.

Migration 38 is deployed cleanly. Live guest browser checks against the public
URL exercised pause, wait, overview reload, hover without resuming, and explicit
resume for all four sections, plus the overview pause button. They reported no
page errors or paid AI invocations; the temporary guest/session was removed.
The full set of 33 local browser checks passes, including failure handling and
pause/resume for each runner. PostgreSQL integration coverage additionally checks
idempotency, ownership, paused-edit rejection, preserved answers and expiry.
TypeScript, scoped ESLint, frontend build and Go tests pass.

## Update: return from the mock to the website

The session overview/report has a «Вернуться на сайт» button in the header.
It returns to the regular dashboard without finishing the mock. If the current
section is running, it first pauses the server clock; existing pauses and
unopened/completed sessions are left intact. Pause failure keeps the overview
open and displays an error. The guest dashboard retains its resume link.

Five regressions cover unopened, running, paused and completed mocks, mobile
return and pause failure; all 38 guest/full-mock/Google-auth browser checks pass.
Live guest checks through Cloudflare confirmed dashboard return, automatic pause
of an active clock, unchanged remaining time after returning again, a visible
mobile button, and a resumable trial, with no browser page errors. Temporary
probe data was removed. TypeScript, scoped ESLint and the production build pass.

## Update: sign-in dialog on the current page

Anonymous clicks on the four practice cards, account-only sidebar pages and
dashboard sign-in links open the existing Google sign-in/registration form in
a dialog. Closing it keeps the current page and restores focus to the clicked
link. Successful desktop sign-in or registration opens the selected section;
the existing iOS Google redirect flow and standalone login route are preserved.
The free mock remains available outside this dialog.

Eighteen guest and Google-auth browser checks pass, including all four cards,
sidebar/header prompts, login and registration, Escape/focus restoration and
mobile dialog sizing. TypeScript, scoped ESLint and the production build pass.
