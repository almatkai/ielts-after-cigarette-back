# Отчёт: бэкенд платного доступа Yrys Academy

Работа выполнена только в `ielts-after-cigarette-back-billing`, ветка
`feat/billing-core`, база `origin/main` — `7140ee5`.
Push, PR, merge и deployment не выполнялись.

## Коммиты реализации

- `813b40c` — `feat(billing): add plans, orders, entitlements and payment provider`.
- `81f4789` — `feat(billing): enforce atomic practice and mock quotas`.
- Документация, этот отчёт и полный список файлов находятся в отдельном
  коммите `docs(billing): document paywall API and implementation results`.

У каждого коммита последний абзац —
`Co-Authored-By: Codex <noreply@openai.com>`.

## Реализованный API

Точный контракт, тела запросов, ответы и коды ошибок:
[BILLING.md](BILLING.md#public-and-authenticated-api) и
[администраторские маршруты](BILLING.md#administrator-api).
Все пути имеют префикс `/api/v1`.

Публичные маршруты:

- `GET /billing/plans`;
- `POST /billing/callbacks/{provider}` — подпись проверяет Provider, обход CORS
  действует только для POST на этот путь.

Защищённые маршруты:

- `GET /billing/me` — доступен также действующему cookie-гостю;
- `POST /billing/checkout` — только аккаунт, обязательно acceptedTerms;
- `GET /billing/orders/{orderID}` — только владелец.

Только ADMIN:

- `GET /admin/billing/orders`;
- `GET /admin/users/{userID}/billing`;
- `POST /admin/users/{userID}/entitlements`;
- `POST /admin/entitlements/{entitlementID}/revoke`;
- `PATCH /admin/entitlements/{entitlementID}/meetings`;
- `GET /admin/billing/settings`;
- `PUT /admin/billing/settings`;
- `PUT /admin/billing/plans/{code}`.

Основной контракт сохранён. Уточнения для полей, не полностью определённых
в задании: админский список возвращает `{orders,limit,offset}`, карточка
пользователя — `{access,entitlements,orders}`; успешные revoke/meetings/callback —
`{ok:true}`. `queuedUntil` включает текущий entitlement, если будущей очереди нет.
Для staff значения assistant limit/remaining равны 0 как признак освобождения.
В ошибке ассистента `feature` и `skill` равны `assistant`.
PUT settings требует все пять лимитов без read-only полей.

## Закрытие гонок и поведение доступа

Старт отдельного теста берёт `users FOR UPDATE`, повторно ищет текущую попытку,
проверяет доступ/остаток и вставляет free_usage вместе с новой попыткой в одной
транзакции. Конкурентные старты одного материала возвращают одну попытку,
старты разных материалов делят один остаток. Ошибка INSERT откатывает списание.

Мок использует существующую advisory-блокировку и ту же блокировку пользователя.
Все четыре навыка проверяются до списаний. Четыре списания, сессия, её попытки и
замена старой сессии при restart фиксируются вместе. Неудачный restart сохраняет
предыдущую сессию. Возобновление, открытие секций и разбор ошибок не списывают.

Claim записывает четыре исторических списания в транзакции переноса владельца;
повторный claim идемпотентен. Исчерпанная квота не блокирует сохранение уже
пройденного гостевого результата. Staff освобождены от списаний.
При выключенном PAYWALL_ENABLED новые проверки не выполняются, usage не пишется.

Оплата и выдача администратором сериализуются на пользователе и образуют очередь
без перекрытий. Колбэк блокирует заказ и пользователя; запись PAID и выдача
entitlement атомарны. Повторные уведомления не выдают доступ повторно;
`order_id UNIQUE` дополнительно защищает инвариант. Цена и купленные преимущества
фиксируются в заказе. Refund отзывает доступ; отзыв не сдвигает будущую очередь.
Кэша доступа нет, поэтому изменения немедленно видны на всех инстансах API.

## Проверки

Успешно выполнены:

- `go build ./...`;
- `go vet ./...`;
- `go test ./...` без интеграционных переменных;
- `go test ./...` с TEST_DATABASE_URL и TEST_REDIS_URL;
- `go test -race ./internal/billing ./internal/attempts ./internal/fullmock ./internal/guest ./internal/app`
  с интеграционными переменными;
- отдельные проверки новых legacy-mock и migration rollback/reapply тестов;
- `git diff --check`.

Для интеграционных тестов подняты временные локальные Postgres и Redis.
Данные лежали в игнорируемых `tmp/billing-postgres` и `tmp/billing-redis`
выбранного worktree. Тесты использовали `internal/testdb` с изолированными
схемами и полной цепочкой миграций. Существующая прикладная/общая БД не менялась.
Временные сервисы после проверки остановлены, их файлы удалены.

Покрыты квоты всех навыков, совместная гонка mock/practice за последнюю попытку,
resume, restart/rollback, гостевой claim, 10 параллельных уведомлений оплаты,
конкурентная очередь выдач, повторная покупка, refund/revoke, встречи,
сохранение условий покупки после редактирования тарифа, скидки/настройки,
права администратора/владельца/гостя, CORS колбэка, отключённый флаг и отдельный
общий Redis-лимит ассистента для chat/stream.

## Что осталось

BCC не реализован по условию: требуется документация банка по созданию платежа
и проверке подписи. Оставлен интерфейс `billing.Provider`. Fake разрешён только
вне APP_ENV=production и не принимает публичных неподписанных уведомлений.

Опциональные аналитические события не добавлены: существующий пакет analytics
ведёт presence/activity и вычисляет агрегаты; общего механизма событий нет.

XERJ оказался недоступен для индексации из-за серверного disk watermark
(read_only_allow_delete, HTTP 429). Исследование продолжено через rg.

## Полный список изменённых файлов

- `.env.dev.example`
- `.env.example`
- `README.md`
- `docker-compose.production.yml`
- `docker-compose.yml`
- `docs/BILLING.md`
- `docs/BILLING_IMPLEMENTATION_REPORT.md`
- `internal/app/app.go`
- `internal/app/billing_integration_test.go`
- `internal/attempts/billing_integration_test.go`
- `internal/attempts/handler.go`
- `internal/attempts/repository.go`
- `internal/attempts/service.go`
- `internal/billing/assistant_integration_test.go`
- `internal/billing/handler.go`
- `internal/billing/handler_test.go`
- `internal/billing/models.go`
- `internal/billing/provider.go`
- `internal/billing/repository.go`
- `internal/billing/repository_integration_test.go`
- `internal/billing/service.go`
- `internal/billing/service_test.go`
- `internal/cache/daily_limiter.go`
- `internal/config/config.go`
- `internal/config/config_test.go`
- `internal/fullmock/billing_integration_test.go`
- `internal/fullmock/generator.go`
- `internal/fullmock/handler.go`
- `internal/fullmock/repository.go`
- `internal/fullmock/service.go`
- `internal/guest/billing_integration_test.go`
- `internal/guest/claim.go`
- `internal/guest/handler.go`
- `internal/guest/handler_test.go`
- `internal/guest/repository.go`
- `internal/httpx/response.go`
- `migrations/000045_billing.down.sql`
- `migrations/000045_billing.up.sql`
