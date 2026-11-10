# BCC e-Commerce WEBVIEW purchase (TRTYPE=1)

## Scope and authority

Source: [BCC Postman documentation](https://documenter.getpostman.com/view/23274245/2s7YYo95nr), collection sections:

- «ТИПЫ ТРАНЗАКЦИЙ → Покупка → CARD → TRTYPE=1», field descriptions;
- «МАКИРОВАНИЕ (MAC) → MAC TRTYPE=1» and its golden vector;
- «3DSECURE 2.X.X → M_INFO → EMV 3-D Secure Protocol»;
- «НАСТРОЙКИ УВЕДОМЛЕНИЙ», «КОДЫ ОТВЕТОВ → ACTION/RC»;
- «ВВЕДЕНИЕ → Терминал ID», «ПРОМЫШЛЕННАЯ СРЕДА».

The collection's 26/05/2026 changelog adds `billAddrLine1` (max 50 characters).
The adapter performs purchases only. It sends **no bank HTTP authorization
requests from the server**. No PAN, expiry, CVC, card token or 3DS challenge
passes through our backend. Do not add card fields to this form/API.

There has been no live bank test or production activation. Completing the bank
acceptance checklist below is required before enabling acquiring externally.

## Flow and stored identifiers

1. Existing authenticated `POST /api/v1/billing/checkout` accepts unchanged
   `{planCode,acceptedTerms:true}` and returns `{orderId,redirectUrl}`.
2. The backend snapshots whole-KZT plan prices and benefits. A PostgreSQL
   sequence allocates a **16-digit numeric ORDER**, distinct from the internal
   UUID. The BCC FAQ says different full ORDER values can still be duplicates
   when their last six digits match, and recommends suffix uniqueness for at
   least a day. The allocator atomically reserves that suffix for **48 hours
   plus the maximum 30-minute bridge lifetime** and saves the full mapping
   before exposing a bridge URL. Reservations survive order deletion, process
   restarts and concurrent API instances; wraparound skips active reservations.
   Allocation fails closed if no suffix is obtained within 64 attempts. The
   six-digit space allows at most one million outstanding leases. Confirm the
   bank's actual deduplication window fits this lease before enabling acquiring,
   including any other systems sharing the terminal. Existing
   `UNIQUE(provider,provider_order_id)` also prevents full-ID reuse. Additive
   migration **000049** creates the allocator and enforces numeric mappings.
   `MERCH_RN_ID` equals this same unique, stable 16-character number.
3. `redirectUrl` is `/api/v1/billing/bcc/bridge?token=...` on the configured
   backend public origin. Its HMAC-SHA256 bearer capability binds the UUID,
   owner, bank ORDER, amount and expiry. It expires from order creation (15m
   default), works only for a mapped BCC PENDING order, and is not a session
   cookie. Knowing an order UUID does not grant bridge access. Do not share it.
4. GET displays a backend-hosted preparation page. The browser reads actual
   `window.screen.height/width`. The cardholder explicitly enters country code,
   subscriber number and billing address. `users.phone` is available in profile,
   but is **not necessarily the cardholder's number**. We deliberately do not
   expose profile PII through a bearer URL or silently substitute it.
5. A same-origin, URL-encoded POST to the bridge verifies the capability,
   current order status, dimensions (positive, max 6 digits), phone (numeric,
   E.164 total max 15), and UTF-8 address (1–50 characters, no controls). Only the
   six preparation fields are accepted; callers cannot choose price, merchant,
   terminal, bank ORDER, gateway, BACKREF or frontend target.
6. The server renders an escaped form with a fresh crypto NONCE and current UTC
   TIMESTAMP; the **browser POSTs directly to BCC**. M_INFO is Base64 of UTF-8
   JSON, not JavaScript `btoa` on Unicode. The bank collects card data/3DS.
   `MK_TOKEN` is absent. Prices are integer KZT, e.g. `4990` becomes `4990.00`,
   not `49.90`. No float arithmetic is used. MAC is HMAC-SHA1 on the decoded
   16-byte HEX key, with decimal-length-prefixed values in documented order:
   AMOUNT, CURRENCY, ORDER, MERCHANT, TERMINAL, MERCH_GMT, TIMESTAMP, TRTYPE, NONCE.
7. Bank/browser GET or POST to `/api/v1/billing/bcc/return/{internalUUID}`
   ignores all result claims and returns **303** to configured PAYMENT_RETURN_URL,
   preserving its existing query and adding `orderId`. It never grants premium
   or changes payment status, even if ACTION/RC/P_SIGN claim success.
8. Only the authenticated server notification can atomically settle the order
   and grant one entitlement. Frontend order polling/return behavior is unchanged.

HTML is no-store, no-referrer, unframeable, with nonce-based CSP, no external
assets and restricted form-action. MAC/Basic credentials/bridge signing key
never appear in HTML or redirect URLs. Phone/address/IP are neither persisted
in notification payload nor written by the payment code to logs. The bridge
capability may be replayed within its TTL **while the order remains PENDING**;
all preparations use the same bank ORDER/RN_ID, never a new purchase identity.
Bank duplicate-ORDER behavior must be verified during acceptance. Once paid,
the bridge refuses further preparations. Callback retries remain idempotent.

## Notification contract to agree with BCC

Register **POST** (not GET):

```
https://YOUR_BACKEND_HOST:443/api/v1/billing/callbacks/bcc
```

Tell the bank: port **443**, TLS 1.2 or newer, your real virtual-host name,
`Content-Type: application/x-www-form-urlencoded`, and agreed Basic Auth
username/password. Configure the same URL in BCC_NOTIFY_URL. Keep the explicit
`:443` even though it is HTTPS's default port. Proxy must preserve Authorization.
The bank must send standard `Authorization: Basic <base64(username:password)>`
preemptively; a missing/wrong credential returns 400, not a Basic challenge.

The collection's notification section says the payload contains the original
request fields plus standard result fields, with additional fields configurable
by the bank. Agree and demonstrate this exact mandatory subset:

| Field | Accepted value |
| --- | --- |
| TERMINAL / MERCHANT | Exact configured identifiers, including leading zeros |
| ORDER | Saved numeric bank ORDER, not our UUID |
| MERCH_RN_ID | Same 16-character value as ORDER |
| AMOUNT | Exact saved whole-KZT amount; integer, `.0` or `.00` accepted |
| CURRENCY | `398` only (normalized internally to `KZT`) |
| TRTYPE | `1` only |
| ACTION | `0` (documented transaction successfully completed) |
| RC | `00` (full approval; confirm literal representation with BCC in acceptance) |

Partial approval `RC=10` is not accepted. Neither `RC=0` nor an empty result is
accepted. Authentication compares hashes in constant time. Duplicate parameters
(including unknown ones), query parameters, invalid URL encoding, non-form
content type and bodies above 32 KiB are rejected. Extra *single-valued* bank
fields may be received, but are not stored. Only ACTION, RC and TRTYPE metadata
are retained. The repository independently validates provider, provider ORDER,
amount and currency inside its transaction and locks the order/account;
`UNIQUE(order_id)` on entitlements also prevents repeat grants.

**There is no documented result-signature algorithm in this collection.** The
outgoing request P_SIGN covers neither ACTION nor RC. Copying/echoing it is NOT
proof of payment. We do not invent an incoming MAC contract or grant access on
an unauthenticated browser response. Result authenticity relies on **TLS plus
the bank-configured Basic Auth notification**. Use dedicated, long, random
notification credentials; do not reuse MAC or app credentials. Any additional
bank result-signature contract requires a documented parser/test before use.

Valid/repeated paid notifications return `200 {"ok":true}`. Invalid ones return
`400 INVALID_PAYMENT_CALLBACK`; database failures return 500 so the bank can
retry. Confirm retry policy and acknowledgement expectations with BCC.

**Non-success limitations:** this adapter only acknowledges the verified full
purchase approval above. Declines, cancellations, intermediate 3DS messages,
refunds and other TRTYPEs are not interpreted as PAID or automatically finalized;
the order remains PENDING unless another supported local workflow changes it.
Agree success-only delivery or operational handling of rejected non-success
notifications with the bank. Do not label PENDING as a successful payment.
Missing notifications, definitive declines, timeouts and ambiguous bank results
require bank-portal/support reconciliation. **TRTYPE=90 reconciliation is not
implemented.** Neither are refunds, cancellation commands, recurring payments,
card storage or token creation. A local REFUNDED status/revocation is not a bank
refund. Do not manually grant on the basis of a browser return.

## Configuration and deployment plumbing

Default PAYMENT_PROVIDER remains **none**, including deployment. Existing fake
provider remains non-production only. BCC is opt-in and startup validation fails
closed when required settings are missing. API base/production Compose and
`.github/workflows/deploy.yml` forward:

| Setting | Source in production GitHub environment | Meaning |
| --- | --- | --- |
| PAYMENT_PROVIDER | Variable | Explicitly select `bcc` only after acceptance |
| PAYMENT_RETURN_URL | Variable | Existing frontend HTTPS return page |
| BCC_GATEWAY | Variable | Test or production endpoint below |
| BCC_TERMINAL | Variable | Bank-issued TID |
| BCC_MERCHANT | Variable | Bank-issued numeric merchant identifier |
| BCC_MERCHANT_NAME | Variable | Uppercase Latin, at most 21 characters |
| BCC_MAC_KEY | **Secret** | Bank MAC key, 16 bytes as 32 HEX characters |
| BCC_NOTIFY_URL | Variable | HTTPS callback URL, explicit port, exact path above |
| BCC_NOTIFY_USER | **Secret** | Dedicated agreed notification username |
| BCC_NOTIFY_PASSWORD | **Secret** | Dedicated password, at least 16 characters |
| BCC_PUBLIC_BASE_URL | Variable | Public backend HTTPS origin, no path/query/credentials |
| BCC_BRIDGE_KEY | **Secret** | Independent random 32-byte key, 64 HEX characters |
| BCC_BRIDGE_TTL | Variable | `15m` default; allowed `1m`–`30m` |
| BCC_LANGUAGE | Variable | `ru` default, `en` or `kz` |
| BCC_TRUSTED_PROXY_CIDRS | Variable | Comma-separated, controlled proxy CIDRs; empty trusts none |

Endpoints (no arbitrary gateway override):

- Test: `https://test3ds.bcc.kz:5445/cgi-bin/cgi_link`
- Production: `https://3dsecure.bcc.kz/webview`

Production rejects the test endpoint, TIDs `88888881` / `88888888`, and the
public test MAC key from the collection. Credentials are never production
defaults. All-zero keys are rejected. BCC additionally requires an HTTPS frontend
return URL. Rotation of bridge key invalidates outstanding bridge capabilities;
rotation/disablement of acquiring does not reconcile pending bank operations.
Coordinate MAC/Basic credential rotation and outstanding notifications with BCC.

The deployment workflow writes BCC values as JSON-escaped, double-quoted dotenv
values and escapes dollar signs for Compose. Credentials containing quotes,
backslashes (including trailing ones), `$`, `#` and Unicode round-trip through
the actual Compose parser; multiline settings are rejected.
For manual dotenv configuration quote secrets appropriately. Do not log env files,
use shell tracing on secrets, or put credentials in URLs. No keys need to be
created/read for unit or isolated integration tests.

### Reverse proxy and logging prerequisites

The legacy app rate-limit IP resolver trusts private peers and leftmost XFF.
BCC deliberately **does not reuse that weaker policy**: it parses RemoteAddr and
walks X-Forwarded-For right-to-left **only through explicitly allowlisted proxy
CIDRs**. Untrusted direct callers cannot spoof their IP. No fallback fake client
IP is sent. Configure only narrow, controlled proxy addresses; restrict direct
backend access, and have the edge sanitize/append actual client XFF. With multiple
proxies, allowlist each controlled hop, never arbitrary client networks or `/0`.
A missing configuration behind a proxy would send its actual peer IP rather than
inventing a customer IP: verify CLIENT_IP against a real bank test before launch.

The backend request logger logs paths, not query strings/bodies. Ensure **external
reverse-proxy/WAF/APM access logs also omit query strings, request/response bodies
and Authorization** for these routes (use `$uri`, not `$request_uri`, where
applicable). Capabilities are bearer credentials; payment preparation responses
contain phone/address in M_INFO. Disable their capture, caching and analytics.
Serve the configured backend origin over HTTPS, expose these routes without a
frontend SPA rewrite, allow notification Authorization through, and permit bridge
same-origin POST/BACKREF bank POST without a generic CSRF/CORS filter blocking
before the application. App enforces same-origin on preparation POST itself.

## Safe verification and external enablement

Local tests perform no bank HTTP requests and use test-only literals. Run:

```
go test ./internal/billing ./internal/config ./internal/app ./scripts
go build ./...
go vet ./...
TEST_DATABASE_URL=<isolated-test-db> TEST_REDIS_URL=<isolated-test-redis> go test -count=1 ./...
TEST_DATABASE_URL=<isolated-test-db> TEST_REDIS_URL=<isolated-test-redis> go test -race ./internal/billing ./internal/app ./internal/config
```

`internal/testdb` creates/drops isolated schemas and applies all migrations; it
never reads app DATABASE_URL or dotenv files. Without TEST_DATABASE_URL/REDIS,
integration tests explicitly skip. Do not run these commands against a working
application database/Redis. Tests cover golden MAC, exact decimal amounts,
mapping/constraints, required fields, UTF-8/XSS, bridge owner/amount/order binding,
expiry/status/forgery, headers, Basic Auth, malformed/duplicate/oversized callbacks,
amount/currency/terminal/merchant/type/result rejection, navigation-only forged
BACKREF, real HTTP routing/CORS and concurrent notifications granting exactly once.
Allocator regressions cover concurrent checkouts, suffix wraparound, retained
leases after order deletion, expired-lease reuse and safe allocation exhaustion.
The deployment serializer is checked against Docker Compose without starting
containers or reading an existing Docker configuration.

Before any external production activation, a bank/operator must:

1. Issue test merchant/TID/key; agree POST notification URL with explicit `:443`,
   Basic credentials, exact result payload/RC representation and retry semantics.
   Confirm the test gateway's hosted card-entry/WEBVIEW flow for this terminal.
2. In a separate test environment configure all BCC variables, apply 000049, set
   PAYMENT_PROVIDER=bcc. Visit checkout using the existing client, confirm genuine
   screen/phone/address collection, direct browser bank POST and no card data at
   our origin. Use **only bank-approved sandbox test cards**, never real charges.
3. Bank acceptance: successful frictionless and challenge 3DS, failure/cancel,
   delayed/out-of-order and repeated notifications, browser closed/forged return,
   capability expiry, refresh/double-submit/duplicate ORDER, suffix deduplication
   window and any other terminal users, non-ASCII address,
   exact whole-KZT amount and correct public CLIENT_IP. Confirm declines do not
   grant access, and establish a process for PENDING/ambiguous results. Verify
   payment amount/order in the bank portal and exactly one entitlement locally.
4. Confirm callback Basic Auth really arrives (no unauthenticated approval is
   accepted), TLS/virtual-host/firewall routing, callback body fields and successful
   200 acknowledgement/retry behavior. Confirm logs/caches do not retain bearer
   URLs or customer data. Obtain explicit bank acceptance.
5. Obtain production TID/key from BCC via its secure process. Keep any supplied
   XOR components/key handling private and offline; do not use public XOR web
   tools. Configure production endpoint/credentials/bridge key and narrow proxy
   trust. Review pending sandbox operations and switch only with explicit release
   approval. Provider selection is never changed automatically by this code.

Neither unit tests nor the prepared configuration constitute bank acceptance,
deployment approval, or proof of a live payment integration.
