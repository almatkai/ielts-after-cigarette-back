# IELTS preparation API

Backend-основа платформы подготовки к IELTS. Это модульный монолит на Go с
PostgreSQL как источником истины и Redis для rate limiting и readiness.

Текущий scope: инфраструктура, регистрация/вход, JWT access token, отзываемые и
ротируемые refresh-сессии, профиль, учебная цель, агрегированный dashboard и
health checks.

## Стек

- Go 1.26, `net/http`, chi;
- PostgreSQL 17, pgx;
- Redis 8;
- JWT HS256, bcrypt;
- SQL-миграции через `golang-migrate`;
- Docker Compose;
- стандартный `testing`, `slog`.

GORM и `sqlc` не добавлены: на текущем небольшом наборе параметризованных
запросов они не дают практического выигрыша.

## Архитектура

HTTP-обработчики отвечают за транспорт и формат ошибок, сервисы — за валидацию и
бизнес-правила, PostgreSQL-репозитории — за SQL и транзакции. Зависимости
создаются явно в `internal/app`.

```text
cmd/api/              entrypoint и graceful shutdown
internal/app/         сборка роутера и зависимостей
internal/auth/        auth, JWT, refresh rotation
internal/user/        профиль и цель
internal/dashboard/   агрегированный dashboard
internal/health/      liveness/readiness
internal/database/    PostgreSQL pool
internal/cache/       Redis и rate limiting
internal/httpx/       JSON, ошибки, middleware
migrations/           versioned SQL migrations
docs/API.md            REST-контракт и примеры
```

## Предварительные требования

Для Docker-сценария нужны Docker Desktop/Engine и Docker Compose. Для локального
запуска без контейнера нужны Go 1.26, PostgreSQL, Redis и `golang-migrate`.

## Конфигурация

Скопируйте `.env.example` в `.env` и замените локальные пароли и `JWT_SECRET`.
В production секрет должен быть случайным значением длиной не менее 32 символов;
значение с `change-me` отклоняется. Wildcard `*` для CORS также отклоняется.

Основные переменные:

| Переменная | Назначение |
|---|---|
| `DATABASE_URL` | PostgreSQL DSN |
| `REDIS_URL` | Redis URL |
| `JWT_SECRET` | ключ подписи access JWT |
| `JWT_ISSUER`, `JWT_AUDIENCE` | обязательные JWT claims |
| `ACCESS_TOKEN_TTL` | обычно `15m` |
| `REFRESH_TOKEN_TTL` | обычно `720h` |
| `REFRESH_COOKIE_*` | имя, Secure и SameSite для HttpOnly refresh cookie |
| `CORS_ALLOWED_ORIGINS` | список точных origins через запятую |
| `AUTH_RATE_LIMIT`, `AUTH_RATE_WINDOW` | лимит register/login/refresh |
| `MAX_REQUEST_BODY_BYTES` | максимальный размер JSON body |

`.env` исключён из Git. Значения по умолчанию в Compose предназначены только для
локальной разработки и должны быть переопределены в любом общем окружении.

## Запуск через Docker

```bash
docker compose up --build
```

Compose запускает PostgreSQL и Redis с health checks, применяет миграции
одноразовым сервисом `migrate`, после чего запускает API на
`http://localhost:8080`.

Остановка:

```bash
docker compose down
```

Для удаления локальных volume используйте `docker compose down -v` только если
данные действительно больше не нужны.

## Локальный запуск

При запущенных PostgreSQL/Redis и заполненных env-переменных:

```bash
make migrate-up
make run
```

Прямые команды для Windows PowerShell без Make:

```powershell
docker compose run --rm migrate -path=/migrations -database=$env:DATABASE_URL up
go run ./cmd/api
```

## Миграции

```bash
make migrate-up
make migrate-down
```

Прямые команды:

```powershell
docker compose run --rm migrate -path=/migrations -database=$env:DATABASE_URL up
docker compose run --rm migrate -path=/migrations -database=$env:DATABASE_URL down 1
```

Все события и аудиторские поля (`created_at`, `updated_at`,
`terms_accepted_at`, `expires_at`, `revoked_at`) используют PostgreSQL
`TIMESTAMPTZ` и UTC. `exam_date` использует `DATE`: это календарная дата, а не
момент времени, поэтому она не должна меняться при смене timezone.

Первая миграция создаёт только необходимые таблицы:

- `users`;
- `user_profiles`;
- `refresh_sessions`;
- `user_skill_progress`;
- служебную `schema_migrations`, создаваемую migration tool.

## Проверки

```bash
make lint
make test
make build
```

Прямые команды для Windows:

```powershell
go fmt ./...
go vet ./...
go test ./...
go build ./...
docker compose config
docker build -t ielts-api:local .
```

## Endpoints

| Метод | Путь | Auth | Назначение |
|---|---|---:|---|
| GET | `/health/live` | нет | процесс работает |
| GET | `/health/ready` | нет | PostgreSQL и Redis доступны |
| POST | `/api/v1/auth/register` | нет | регистрация STUDENT |
| POST | `/api/v1/auth/login` | нет | вход |
| POST | `/api/v1/auth/refresh` | нет | атомарная ротация refresh token |
| POST | `/api/v1/auth/logout` | нет | отзыв текущей refresh-сессии |
| GET | `/api/v1/users/me` | Bearer | текущий пользователь |
| GET | `/api/v1/profile` | Bearer | профиль и цель |
| PATCH | `/api/v1/profile` | Bearer | `displayName` и/или `timezone` |
| PUT | `/api/v1/profile/goal` | Bearer | target band, тип и дата экзамена |
| GET | `/api/v1/dashboard` | Bearer | агрегированная сводка |

Полный контракт с request/response-примерами описан в [docs/API.md](docs/API.md).

## Найденные frontend-контракты

Frontend находится в соседнем репозитории `ielts-after-cigarette/iac-web`.

Фактический код frontend задаёт:

- регистрационные поля `name`, `email`, `password`, `confirmPassword`,
  `acceptedTerms`;
- поля входа `email`, `password`, `remember`;
- профильные UI-поля имени/фамилии/email и цель `examFormat`, `targetScore`,
  `examDate`;
- exam type `academic` и `general`;
- навыки `listening`, `reading`, `writing`, `speaking`;
- target band в UI от 5.5 до 8.5 с шагом 0.5;
- существующий маршрут диагностики пока отсутствует; кнопка ведёт на
  `/dashboard/practice`.

Первоначально во frontend отсутствовали API client, auth state, guards и backend
env URL. Интеграционный этап добавляет централизованный fetch client и явные DTO
adapter-функции, не меняя backend-модели ради имён UI-полей.

## Принятые допущения

- Access token возвращается JSON и хранится frontend только в памяти. Refresh
  token устанавливается в HttpOnly cookie с ограниченным auth path; frontend
  отправляет cookie с `credentials: include`.
- Refresh token — криптографически случайное значение; в БД хранится только
  SHA-256 hash. Ротация выполняется транзакционно с `FOR UPDATE`. Повторное
  применение уже использованного токена отзывает активные сессии пользователя.
- `remember` принимается для совместимости формы, но пока не меняет серверный
  TTL.
- Frontend first/last name при подключении должен собрать в `displayName`.
- API использует lowercase skill IDs из реального `SkillId` frontend, а не
  uppercase из первоначального примерного ответа.
- `recommendedAction.target` равен существующему
  `/dashboard/practice`, пока отдельного `/diagnostic` нет.
- Band API и БД принимают 0–9 с шагом 0.5; текущий frontend предлагает более
  узкий UX-диапазон.
- Изменение email и `currentBand` публичным profile endpoint не разрешено.

## Какие mock/static data заменить позже

- `OverviewPage`: карточки текущего/целевого band и даты, recommended action,
  today plan, skill progress и unread notifications — одним
  `GET /api/v1/dashboard`;
- `ProfilePage`: поля профиля и цели — `GET/PATCH /api/v1/profile` и
  `PUT /api/v1/profile/goal`;
- `LoginForm`/`RegisterForm`: локальное сообщение «форма готова» — auth
  endpoints;
- landing `skillProgress` остаётся демонстрационным marketing content и не
  должно становиться пользовательским API;
- practice catalog пока пуст и не покрывается текущим backend scope.

## Пока не реализовано

Диагностика, генерация плана, practice/content engine, mistakes, реальные
результаты прогресса, Writing AI, Speaking/audio, media storage, уведомления,
email delivery/password recovery, смена пароля, avatar upload, admin, платежи и
подписки. Пустые таблицы и заглушки для них намеренно не создавались.

## Эксплуатационные замечания

- Недоступный PostgreSQL останавливает запуск с понятной ошибкой.
- Недоступный Redis не вызывает panic: приложение стартует, readiness возвращает
  503, а rate-limited auth endpoints временно отвечают 503.
- PostgreSQL остаётся источником истины; пользовательские данные и refresh
  sessions не хранятся только в Redis.
- Для нескольких API replicas Redis rate limit общий. Кэш dashboard пока не
  добавлен: без измеренной необходимости он только усложнил бы инвалидирование.
