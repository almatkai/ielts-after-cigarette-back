# Надёжность и наблюдаемость: первый этап

Обновление: production rollout выполнен 2026-10-04 по отдельной просьбе.
Фактическое состояние, доступ и оставшиеся ограничения описаны в
`DEPLOYMENT_2026-10-04.md`. Ниже — исходный отчёт первого локального этапа.

Работа выполнена в основных checkout backend/web, без переноса в `*-blog`.
Production, DNS и внешние аккаунты не изменялись. GlitchTip/Sentry не подключены.
Исходные оптимизации уже описаны в `OPTIMIZATION.md`; здесь только новый этап.

## Реализовано

- Единый `httpx.InternalError` во всех 14 ветках неожиданных handler-ошибок:
  причина, request ID и внутренний user ID в JSON-логах, безопасный ответ 500.
  Паники по-прежнему содержат stack trace в Recover. CORS теперь позволяет
  фронту прочитать `X-Request-ID` и ETag. Исправлен учёт первого HTTP-статуса.
- На фронте QueryCache, MutationCache, root error boundary, global error и
  unhandled rejection сходятся в `configureErrorReporter`/`reportError`.
  По умолчанию **нет отправки событий**: внешний трекер будет выбран позже.
  4xx и AbortError игнорируются; один объект ошибки не отправляется дважды.
  Передаются только source/name/code/status/requestId, без ответов, токенов,
  сообщений исключений, query keys и URL parameters. Это основа, а не готовый
  SDK со stack traces/source maps. Сбой reporter не ломает приложение.
- Ошибка gateway с не-JSON ответом получает requestId из заголовка.
  После трёх последовательных сбоев автосейва создаётся один сигнал;
  после успешного сохранения счётчик сбрасывается. Dirty answers сохраняются
  для повтора, интервал автосейва и существующий UI не изменены.
- Кэш grading: bounded L1 (256 версий, час), Redis L2 (24 часа).
  Namespace `iac:grading:v1:<skill>:<material>:<version>`; менять `v1` при
  несовместимом изменении DTO. Один MGET для всех L1 misses, один pipeline
  для записи. Не кэшируются ownership, попытки, ответы студентов и AI feedback.
  Повреждённый JSON — miss; PostgreSQL остаётся источником истины.
  Кэш использует отдельный Redis client с короткими таймаутами, без ретраев,
  лимитом 4 MiB на значение и 30-секундным circuit breaker.
- PostgreSQL jobs **уже** создаются транзакционно с submit и имеют retry/backoff,
  heartbeat и восстановление lost leases. Поэтому River/Asynq не добавлены
  поверх существующей durable очереди. Recovery теперь обрабатывает задания
  напрямую из БД, а не пытается вновь отправить их в недоступный Redis.
  Redis остаётся необязательным wake-up сигналом. За один recovery tick берётся
  не больше одной задачи каждого вида, чтобы не блокировать очередь на пачку
  длинных внешних вызовов. Ошибки claim/persistence больше не теряются молча.
- `cmd/worker` может запускать Writing без SPEECH_ENABLED; Speaking включается
  отдельно. `WRITING_WORKER_EXTERNAL=true` отключает Writing worker в API.
  По умолчанию false сохраняет прежний deployment. Внешний worker получает
  signal cancellation и ждёт окончания Run до закрытия PostgreSQL.
- Prometheus: route-template counters/histograms, Go/process stats, pgx pool,
  количество/возраст durable jobs и показатель успешности сбора job stats.
  Один SQL aggregate на scrape, timeout 2s; READY история не агрегируется.
  Метрики — отдельный listener: `METRICS_ADDR` пуст по умолчанию, в overlay
  `:9090` без публикации host port. Публичный `/metrics` не добавлен.
- Подготовлены opt-in Compose profiles: Prometheus, Grafana dashboard, Loki,
  Alloy (не устаревший Promtail), backups, отдельный worker. Логи ротируются;
  retention Loki 7 дней, Prometheus 15 дней.
- Бэкап: custom pg_dump + проверка TOC + SHA256, upload dump/checksum в S3,
  только затем атомарная публикация локального файла и success heartbeat.
  Сбой upload возвращает ошибку при BACKUP_ONCE, в daemon режиме retry через
  5 минут; старые локальные копии удаляются только после успешного upload.
  node-exporter textfile даёт метрику возраста последнего off-host бэкапа.
- Отдельный opt-in overlay для pg_stat_statements и SQL для DBA. Нет новой
  автоматической миграции/рестарта PostgreSQL ради диагностического extension.

## Включение (не было выполнено)

1. Собрать backend image с этим кодом. Проверить выбранные pinned версии
   monitoring images на совместимость/security updates перед rollout.
2. Добавить значения из `ops/.env.observability.example` в защищённый deploy env.
   Grafana требует сильный пароль даже при использовании отдельных profiles.
   Backup bucket должен быть private, на другом сервере/провайдере, с encryption,
   ограниченным IAM (upload только в нужный prefix) и remote retention/versioning.
   Локальный MinIO на том же сервере — не полноценный disaster-recovery backup.
3. Проверить Compose (нужна версия с поддержкой `!reset`, >=2.24):

   ```sh
   docker compose --env-file <deployment.env> \
     -f docker-compose.production.yml -f docker-compose.observability.yml \
     --profile workers --profile backups --profile monitoring config --quiet
   ```

4. Для переноса Writing выставить `WRITING_WORKER_EXTERNAL=true` и обязательно
   запускать profile workers (или отдельный cmd/worker в другой инфраструктуре).
   Иначе задачи останутся ожидать worker. Учесть SUM пулов: API default 20,
   worker default 5, умноженные на реплики, плюс migration/admin reserve.
   SPEECH_ENABLED=true требует существующего speech-service и корректных
   URL/token; overlay сам speech-service не создаёт.
5. После проверки rollout:

   ```sh
   docker compose --env-file <deployment.env> \
     -f docker-compose.production.yml -f docker-compose.observability.yml \
     --profile workers --profile backups --profile monitoring up -d --build
   ```

   Убедиться, что `/metrics` не доступен через public ingress. Grafana доступна
   только на 127.0.0.1:3300; пользоваться SSH tunnel/VPN, не открывать публично.
   Не публиковать Prometheus/Loki/Alloy/node-exporter без authentication.
   Alloy обращается к отдельному Docker socket proxy в private log-control
   network; POST запрещён. Сам proxy монтирует Docker socket и остаётся
   привилегированным: `:ro` mount сам по себе **не делает Docker API read-only**.
   GET inspect/logs тоже могут раскрывать чувствительные данные, поэтому
   proxy не публикуется и недоступен из общей application network.
6. В Grafana есть IAC Operations и Explore. Поиск:

   ```logql
   {service=~"backend|worker"} | json | request_id="<support-id>"
   ```

   Request/user/attempt IDs не становятся Loki/Prometheus labels: иначе растёт
   cardinality. Не добавлять логирование тел запросов, JWT, essays или аудио.
7. Правила проверяют 5xx, latency, пул, зависшие/FAILED jobs и возраст бэкапа.
   **Правила не равны уведомлениям:** Alertmanager/contact point/канал доставки
   не подключён. До объявления monitoring рабочим настроить получателя,
   отправить test alert и проверить доставку. FAILED jobs не исчезают сами
   из БД; они требуют разбора/повтора, поэтому соответствующий alert сохраняется.
8. pg_stat_statements требует согласованного PostgreSQL restart. Добавить
   `docker-compose.pgstats.yml` только в maintenance window; сохранить другие
   preload libraries при наличии. После restart выполнить `ops/postgres-stats.sql`
   как DBA. Не использовать предложенную SQL команду для reset статистики:
   накопленные данные нужны для сравнения нагрузки. Доступ к query texts — DBA.

## Проверка restore

`pg_restore --list` проверяет читаемость архива, но не доказывает восстановимость.
Нужно регулярно восстанавливать реальные off-host backups в **отдельную** БД:

```sh
aws s3 cp s3://<bucket>/<prefix>/<file>.dump ./
aws s3 cp s3://<bucket>/<prefix>/<file>.dump.sha256 ./
sha256sum -c <file>.dump.sha256
createdb --maintenance-db '<isolated admin DB URL>' iac_restore_check
pg_restore --exit-on-error --no-owner --no-acl \
  --dbname '<isolated restore DB URL>' <file>.dump
```

Проверить counts пользователей/попыток/оценок, FK и application smoke test.
Не делать restore в production и не перезаписывать её случайным dump.
Role ownership/grants не включены (`--no-owner --no-acl`): их задаёт инфраструктура.
**Media volumes не входят в PostgreSQL dump.** Для текущего production
filesystem-storage отдельно бэкапить listening/writing/speaking volumes и
проверять восстановление файлов, иначе восстановятся ссылки, но не записи.

## Что сознательно остаётся на следующие этапы

- Внешний error tracker, stack traces, release/source map uploads и sampling:
  отложены по просьбе пользователя. UI reporter ещё не является трекингом.
- Полный public material cache: требуется отделить immutable version content
  от mutable material metadata и всегда перепроверять публикацию/архивирование.
  Нельзя кэшировать результат permission check на 24 часа.
- Dashboard/progress cache с корректной invalidation после submit **и** окончания
  фоновой оценки **и** смены цели. Текущий dashboard уже использует SQL batch;
  без замеров и согласованной допустимой stale-границы не добавлен TTL вслепую.
- Job status в Redis/SSE: лёгкий ownership-aware SQL endpoint уже существует.
  Подмена его Redis-only ответом может выдать чужую попытку или устаревший статус.
- Presigned media URLs: production сейчас использует filesystem, не MinIO.
  Требуются внешний HTTPS endpoint, access contract/TTL/CORS и возможность
  отзыва доступа. Текущий authenticated Range proxy с ETag сохранён.
- Cloudflare/DNS/uptime сервис, alert notification channel, remote IAM/lifecycle:
  нужна настройка внешней инфраструктуры, не выполнена локальными правками.
- PgBouncer только при измеренной необходимости; Postgres не заменяется.
- Объединение blog worktree, SPA migration, удаление legacy API — отдельные
  архитектурные изменения, не делаются в ходе этой оптимизации.

Redis cache значения имеют TTL, но общий Redis memory limit этим не задаётся.
При росте нагрузки выделить отдельный cache Redis с memory policy; не включать
allkeys eviction на общей очереди/ratelimiter без анализа последствий.
При ручном SQL исправлении immutable version content необходимо очистить
соответствующие L1/L2 кэши, а лучше создать новую версию штатным механизмом.

## Проверки этого этапа

- `go test ./...`, `go vet ./...`; integration tests — на отдельном временном
  localhost PostgreSQL cluster и Redis, без чтения production DATABASE_URL.
- `go test -race ./...` для всех backend пакетов (с теми же isolated services).
- Redis: TTL, L2 после нового Service, corrupt value fallback, чужой user,
  свежие private answers, black-hole TCP timeout/circuit breaker.
- Recovery Writing без Redis и соблюдение backoff; metrics route cardinality,
  recovered panic 500, collector на мигрированной пустой БД.
- `ops/backup/test_backup.py`: реальный dump/restore и SHA256, mocked S3 upload,
  failure не обновляет heartbeat и не публикует partial dump.
- Web: 23 regression tests, TypeScript, targeted ESLint и production build.
- Compose config/profiles/наследование worker/приватные ports, shell syntax.
- Docker daemon недоступен: runtime запуск monitoring images, promtool,
  Alloy/Loki validation и реальный S3 transport **не проверены**. Перед rollout
  выполнить container smoke tests и restore настоящего off-host backup.
