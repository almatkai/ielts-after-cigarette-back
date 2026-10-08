# Guest Full Mock

Entry page: `/app/`. Anonymous visitors see the normal dashboard navigation,
practice categories and published test summaries. AI chat is hidden. Paid test
buttons and private account pages ask visitors to sign in; their API access
remains denied. The free Academic Full Mock is embedded in the dashboard and
Full Mock page. Launching it accepts the linked terms inline and verifies
Turnstile, without a separate opt-in page or format selection. `/app/try`
redirects to the dashboard for old links.

Migration 36 introduces `guest_mock_materials`. When guest mode is enabled,
startup reserves four eligible Academic mock sections in one transaction under
an advisory lock. Every new guest receives those exact material/version pairs.
Reservations survive publication changes and restarts. Existing sessions retain
their original pinned sections. Public catalogs, new practice starts and normal
mock generation exclude the reserved material IDs, including when an account
has exhausted the remaining bank. A partial or incomplete reservation denies a
new trial instead of drawing different materials. Detailed review uses the
existing version-pinned runners and grading queues without signing in.

## Enable

1. Apply migrations `000035_guest_trials`, `000036_fixed_guest_mock` and
   `000037_full_mock_section_clocks` and `000038_full_mock_section_pause`
   before deploying the new API, including
   when guest trials remain disabled.
2. Keep `GUEST_TRIAL_EXAM_TYPES=academic` for the fixed trial. The current trial
   is Academic, independent of the registered student's selected exam format.
   Create a Cloudflare Turnstile widget for the frontend hostname(s).
3. Configure the API environment:

   ```dotenv
   GUEST_TRIAL_ENABLED=true
   GUEST_TRIAL_IP_LIMIT=5
   GUEST_TRIAL_GLOBAL_LIMIT=100
   TURNSTILE_SITE_KEY=<public site key>
   TURNSTILE_SECRET_KEY=<server secret>
   TURNSTILE_HOSTNAMES=your-frontend.example
   REFRESH_COOKIE_SECURE=true
   ```

4. Keep the exact frontend origin in `CORS_ALLOWED_ORIGINS`. The API validates
   Origin on all cookie-authorized guest writes. Prefer frontend and API on the
   same site; browsers may block cross-site cookies. If the deployment already
   uses `SameSite=None`, guest cookies inherit that setting and require HTTPS.
5. Allow `https://challenges.cloudflare.com` in any deployed CSP for scripts
   and frames, and allow API access in `connect-src`. The public site key is
   served by `/api/v1/guest/config`; no separate frontend key is needed.
6. Ensure all four published full-test banks exist and the existing Writing,
   Speaking, transcription workers and configured AI providers are operational.

Turnstile validation checks `success`, the `guest_mock` action, and an explicit
frontend hostname allowlist. Tokens are checked on the server, following
[Cloudflare Siteverify documentation](https://developers.cloudflare.com/turnstile/get-started/server-side-validation/).
Missing keys are permitted only in explicitly enabled `APP_ENV=development`.
The feature defaults to disabled in all environments.

## Limits and identity

- Opaque 256-bit cookie, `HttpOnly`, path `/api/v1`; only its HMAC is stored in
  PostgreSQL. The JWT secret keys that HMAC; rotating it invalidates guest
  cookies. The cookie persists 180 days to remember a consumed trial.
- A `GUEST` actor has no usable password, Google identity, access token or
  refresh session. Guests are excluded from registration counts and authenticated
  presence tracking.
- Access expires 7 days after issuance. Each section's server clock begins on
  first opening and survives reloads. «Продолжить позже» saves answers, then
  pauses that section on the server; reopening resumes its saved remaining time.
  The overview also offers «На паузу» for an already-running clock. Pausing is
  idempotent, does not grant a new duration, and cannot rescue an expired section.
  Closing a tab without explicitly pausing does not stop the clock or extend
  the guest's 7-day access. Listening, Reading and Writing use their
  material durations; Speaking allows 15 minutes. Section expiry grades saved
  answers and leaves the next section untimed until opened. Restart/racing
  requests reuse the same mock under a PostgreSQL advisory lock, even after completion.
- The default quotas allow 5 new guest identities per IP and 100 globally per
  fixed 24-hour window, anchored to its first request. These are Redis quotas
  shared by API replicas, not per-process limits. Failed starts may consume a
  reservation; resuming an existing identity does not consume a new start.
- IP identifiers are HMACs. Only forwarding headers from the existing trusted
  private/loopback proxy path are used; the edge proxy must replace client-sent
  forwarding headers, and the API must not be exposed through an untrusted proxy.
- 180 API requests per minute per guest, 6 audio uploads across the trial
  (three parts plus retries), and 3 submit requests per attempt across the trial.
  Uploads are bounded to 12 MiB of audio plus 1 MiB multipart overhead. Answer
  requests are bounded to 128 KiB. Existing worker retries still apply.
- Redis failures deny guest access. Setting guest mode off stops guest access
  immediately, including review. Normal account authentication remains available.
- Guests can access only owned attempts, their mock, and media referenced by
  their pinned test versions. They cannot create practice attempts or access
  account, history, admin or AI chat endpoints. Anonymous catalog access exposes
  summaries only; test bodies, media and starts require an account or the
  capability for the visitor's own trial.

Cookie clearing, incognito, different devices and VPNs can obtain another guest
identity. IP addresses are coarse rate signals and can be shared by many students.
Neither cookies nor Turnstile prove a unique person. The global start cap bounds
the number of admitted trials across changing IPs; it is not a monetary spending
cap. Costs depend on providers, upload duration, input sizes and worker retries.
This version uses the configured AI providers, without a new guest-specific model
or invasive browser fingerprinting.

## Results and retention

Signing in switches to the account's own data. This version does **not** transfer
guest results into that account or claim that they were saved to it. Review
remains accessible with the guest cookie when signed out, within its access
period. The seven-day expiry limits access; it does not delete DB rows or audio.
Database and object-storage retention must be scheduled together. Keep consumed
token tombstones for the cookie's 180-day lifetime when adding cleanup, otherwise
old cookies could start a new trial after their rows are deleted.

## Verification

`go test ./...` runs handler/auth/config coverage. Set `TEST_DATABASE_URL` to an
explicit local test database to also exercise migrations, actor creation,
ownership, concurrent generation and expired-session reuse in isolated schemas.
The frontend has `tests/guest-trial.spec.ts` for anonymous launch, AI review,
reload, public browsing, fixed Academic launch, mobile layout and locked navigation. Browser tests
mock AI results; they do not call paid providers.
