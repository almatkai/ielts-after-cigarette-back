# Billing and paid access

`000045_billing` adds plans, settings, orders, entitlements and lifetime usage.
Existing migrations are unchanged. Apply migrations before enabling billing.

## Configuration

- `PAYWALL_ENABLED=false` by default. When disabled, practice, mocks, guest
  claim and assistant keep the existing behavior and write **no** free usage.
- `PAYMENT_PROVIDER=none` by default: checkout returns
  `503 PAYMENT_PROVIDER_UNAVAILABLE` without creating an order.
- `PAYMENT_PROVIDER=fake` is for non-production environments only. Both config
  validation and provider construction reject `APP_ENV=production`
  (case-insensitive). Fake checkout settles through the same notification
  transaction as real callbacks. Its public callback rejects all requests;
  there is no unsigned route for granting access.
- `PAYMENT_RETURN_URL` defaults to the first `CORS_ALLOWED_ORIGINS` entry plus
  `/app/billing/return` (the web app is served under `/app`). It must be an absolute HTTP(S) URL. The fake provider adds
  `orderId` while preserving existing query parameters.

The base and production Compose manifests pass the three variables to the API.
The shared development manifest already imports `.env` and `.env.dev`.
The production manifest currently sets `APP_ENV=development` in the existing
configuration; operators must use `APP_ENV=production` to activate the production
configuration guards. This change does not modify that existing environment.

Plans are seeded as follows; all prices are integer KZT:

| code | listPrice | salePrice / actual price | days | meetings |
| --- | ---: | ---: | ---: | --- |
| self | 9990 | 4990 | 30 | 0 |
| group | 24990 | 14990 | 30 | 4 GROUP |
| personal | 54990 | 34990 | 30 | 4 PERSONAL |

## Public and authenticated API

All paths below are relative to `/api/v1`. JSON uses camelCase. Errors use the
existing `httpx.WriteError` envelope (`code`, `message`, optional `details` and
`requestId`). `details` now supports nested JSON while preserving existing
string-valued validation responses.

- `GET /billing/plans` is public and returns
  `{currency:"KZT",plans:[{code,title,listPrice,salePrice,price,accessDays,meetingsCount,meetingsKind}]}`.
  Only active plans, ordered by sort order then code. `salePrice` and
  `meetingsKind` are explicitly nullable.
- `GET /billing/me` is protected. Cookie guests may access this route and receive
  only `{paywallEnabled,guest:true}`. Accounts receive
  `{paywallEnabled,guest:false,staff,active,plan,endsAt,queuedUntil,meetingsTotal,meetingsUsed,free,assistant}`.
  `plan` is `{code,title}` or null, dates are RFC3339 or null, each of the four
  `free` skills is `{limit,used,remaining}`, `assistant` is `{limit,remaining}`.
  `queuedUntil` is the last non-revoked future entitlement's end, including the
  current entitlement when there is no queue. Meetings describe the current
  entitlement. Staff have assistant limit/remaining 0 to denote exemption.
- `POST /billing/checkout` requires an account and
  `{planCode,acceptedTerms:true}`. Missing/false terms: `400 VALIDATION_ERROR`;
  unknown/inactive plan: 404; configured provider: `201 {orderId,redirectUrl}`.
  Missing provider: 503 without an order. Provider creation failure: 503 with
  the pending order retained for reconciliation.
- `GET /billing/orders/{orderID}` returns only the owner's
  `{id,status,planCode,amount,listPrice,provider,providerOrderId,createdAt,paidAt}`; foreign orders are 404.
- `POST /billing/callbacks/{provider}` is public and bypasses browser CORS
  filtering for this POST path only. The provider must verify the signature
  before returning a notification. Valid notifications return `200 {ok:true}`;
  invalid callbacks return `400 INVALID_PAYMENT_CALLBACK`; missing providers
  return 503. Fake payments reject public callbacks.

Account-specific responses use `Cache-Control: private, no-store`.

## Administrator API

Every route below requires `ADMIN`, independently of editor/content permissions.

| Method and path | Request / response |
| --- | --- |
| `GET /admin/billing/orders?status=&limit=&offset=` | `{orders,limit,offset}`; includes `userId`, `email` and the order fields. Default limit 50, range 1–200; offset >= 0. Optional status must be valid. |
| `GET /admin/users/{userID}/billing` | `{access,entitlements,orders}`; full entitlement and order history. |
| `POST /admin/users/{userID}/entitlements` | `{planCode,days?,source:"ADMIN"\|"BONUS",note}` -> 201 entitlement. Days defaults to plan accessDays, allowed 1–36500. |
| `POST /admin/entitlements/{entitlementID}/revoke` | `{note}` -> `200 {ok:true}`. Preserves the original revocation time, appends a supplied note. |
| `PATCH /admin/entitlements/{entitlementID}/meetings` | `{meetingsUsed}` -> `200 {ok:true}`. Value must be between 0 and meetingsTotal. |
| `GET /admin/billing/settings` | `{freeListening,freeReading,freeWriting,freeSpeaking,freeAssistantDailyLimit,updatedAt,paywallEnabled}`. |
| `PUT /admin/billing/settings` | Requires all five nonnegative numeric limits, returns saved settings. `paywallEnabled` and `updatedAt` are read-only and must be omitted. |
| `PUT /admin/billing/plans/{code}` | `{listPrice,salePrice,active}` -> saved plan. `salePrice:null` removes discount; omitted salePrice also removes it. List price and active are required; 0 <= salePrice <= listPrice. |

Mutating requests reject unknown JSON fields and trailing JSON. Notes are capped
at 4000 bytes. Missing resources are 404; invalid values are 400.

## Transaction and access rules

Practice creation locks the `users` row, rechecks for an existing standalone
`IN_PROGRESS`/`PROCESSING` attempt, checks the entitlement and lifetime allowance,
and inserts `free_usage` **in the same transaction as the attempt INSERT**.
Both concurrent clicks on the same material and different-material starts are
serialized. A failed insert or transaction rollback restores the allowance.
Only a genuinely new attempt is charged. Reading/listening review and mistake
practice have no backend creation path and do not charge usage. Writing's two
tasks and Speaking's three parts belong to one attempt and cost one unit.

Generated full mocks retain the existing per-user advisory lock. A new mock,
including restart, takes the same account row lock used by standalone practice,
checks all four skills before inserting any usage, and records four rows under
one `FULL_MOCK_SESSION` source ID. Session, section attempts, abandonment during
restart and usage all commit together. Failed restarts preserve the prior
session. Resuming and opening sections never debit again. Legacy curated mock
starts/restarts also use a single billed transaction when paywall is enabled.

Guest actors and ADMIN/EDITOR bypass the paywall. Enabled guest claim locks the
destination account and records one historical full mock (four idempotent
usage rows) in the ownership-transfer transaction. Claim is allowed even when
the destination already exhausted its free allowance or has paid access; usage
is historical and remaining is clamped at zero. Staff claims are exempt. Disabled
claim records no usage.

Paid access uses non-revoked entitlements with `startsAt <= now < endsAt`.
Purchases and administrator grants lock the account and start at
`max(now,last non-revoked endsAt)`, so concurrent grants form a queue. Ends use
calendar-day arithmetic in Asia/Almaty. Revocation/refund does not pull future
queued grants forward. No automatic renewal. Orders capture list price, actual
amount, access days and meeting count at checkout; later plan changes do not
rewrite an existing order's benefits.

Notifications lock the order and the account, validate provider, provider order
ID, exact amount and currency, and atomically mark paid and insert one
entitlement. `order_id UNIQUE` is a second guard against double grants. Duplicate
notifications succeed without changes. Pending orders may become PAID, FAILED
or CANCELLED; PAID may become REFUNDED, which revokes its entitlement. A refunded
order cannot be replayed into PAID.

Access has no cache: payment, grants, revocation and meeting/settings edits are
visible immediately across API instances. Lifetime skill limits default to 3.
Free assistant requests share a separate atomic Redis daily counter across
`/assistant/chat` and `/assistant/chat/stream`, default 5, reset on the Almaty
calendar day. Zero denies all free assistant messages. Paid accounts retain the
existing AI assistant daily limit; existing Writing/Speaking daily limits
continue independently. Redis errors fail free assistant admission closed;
a missing Redis connection cannot turn the new free quota into unlimited access.

Quota exhaustion returns 402 with `PAYMENT_REQUIRED` and
`details:{feature,skill,remaining:{listening,reading,writing,speaking}}`.
For full mocks `skill` is the first exhausted skill in that order. Assistant
uses `feature:"assistant",skill:"assistant"`. Russian messages:

- «Бесплатные тесты по этому навыку закончились. Выберите тариф, чтобы продолжить.»
- «Бесплатные сообщения ассистента на сегодня закончились.»

## Verification and remaining integration

```
go build ./...
go vet ./...
go test ./...
TEST_DATABASE_URL=... TEST_REDIS_URL=... go test ./...
```

Tests use `internal/testdb` isolated schemas and apply the full migration chain.
Billing coverage includes parallel quota starts, mixed mock/practice races,
resume, atomic restart failure, guest claim, callback replay, concurrent queued
grants, refund/revoke, meetings, settings, discounts, ownership/admin routes,
callback CORS, and separate shared assistant counters.

BCC is deliberately unimplemented until its API/signature documentation is
available. Implement the `billing.Provider` boundary and authenticate callbacks
before returning a `Notification`; browser return URLs never confer access.
`internal/analytics` currently tracks presence/activity and derives conversion
statistics; it exposes no generic product-event mechanism. The optional
`paywall_blocked`, `checkout_started` and `payment_succeeded` events are therefore
not added.
