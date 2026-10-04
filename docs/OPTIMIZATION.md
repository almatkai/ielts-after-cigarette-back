# Оптимизация — 4 октября 2026

Область: основные checkout `ielts-after-cigarette-back`,
`ielts-after-cigarette/iac-web`, `ielts_landing`. Копии `*-blog` не изменялись.
Деплой не выполнялся. Правила оценки, права, дедлайны и интервал автосохранения
не менялись; история подгружается по мере прокрутки, чат — при первом открытии.

## Результат исходного аудита

| № | Что сделано / решение |
|---|---|
| 1 | Reading: пакетная загрузка закреплённых версий пассажей; тест из трёх пассажей требует 6 SQL-запросов вместо 12. Фильтрация групп напрямую по version ID. |
| 2 | Новый приватный `/attempts/{id}/status`: один SQL snapshot, без ответов, записей и транскриптов. Опрос 2 → 5 → 10 секунд; полный detail перечитывается после завершения, ошибки или abandonment. |
| 3 | Запись всех ответов — один атомарный SQL с блокировкой строки вместо BEGIN + SELECT + batch + COMMIT. Проверки владельца и Full Mock остались в сервисе; это не один запрос для всего HTTP-запроса. Интервал 2 секунды сохранён: увеличение окна потери черновика не является безопасной оптимизацией. |
| 4 | Опубликованные Listening/Writing media: приватная браузерная ревалидация по ETag; 304 не открывает object storage. Проверка доступа/публикации выполняется до 304. Черновики, admin и ошибки остаются no-store. Range сохранён. Presigned redirects и кэш Speaking recordings не добавлялись: это другой контракт и модель доступа. |
| 5 | Разбор сданных Reading/Listening попыток использует существующий bounded cache неизменяемых версий; TTL — час. Ответы пользователя не кэшируются совместно с материалами. PublicStructure при старте не кэшируется, чтобы не закэшировать разрешение на опубликованный материал. |
| 6 | Старый bulk mistakes API сохранён для совместимости. Основной интерфейс уже использует пагинацию; удалять действующий API в рамках оптимизации нельзя. |
| 7 | Cursor history: 50 попыток за раз, max 100; загрузка более старых страниц при прокрутке с кнопкой retry/load-more. Вместо восьми LEFT JOIN — skill-specific lateral lookup. Индекс в миграции 000029. Legacy полный ответ сохранён. |
| 8 | Reading branch подсчёта ошибок переписана с OR/IN на индексируемый UNION с JOIN. Новое persisted mistake_count не вводилось, чтобы не менять данные и не требовать backfill. |
| 9 | Создание Reading groups/questions и Listening parts/groups/questions использует pgx.Batch в прежней транзакции. |
| 10 | Убран Zod из route search validator. Эквивалентность defaults/coercion/UUID/page validation проверяется сравнительным тестом с прежней схемой. Zod в других формах сохранён. |
| 11 | Лёгкий mascot/widget доступен сразу; chat/page-reader загружаются при первом открытии. После этого чат остаётся смонтированным: черновик и состояние не теряются при закрытии. |
| 12 | Удалён неиспользуемый app landing/WebGL/smoothui код, общий Brand сохранён. BorderBeam использует LazyMotion и отдельный features chunk; анимации не удалены. |
| 13 | Дополнительная правка не нужна: установленный Manrope уже содержит unicode-range. Неиспользуемые subset-файлы не скачиваются автоматически. Cyrillic-ext/latin-ext нужны для казахских букв и произвольных IELTS текстов; удалять их ради размера каталога нельзя. |
| 14 | Nitro/SSR и схема деплоя сохранены. Добавлены gzip/brotli public assets и immutable cache для fingerprinted assets. В production nginx уже переписывает `/app/assets/` в `/assets/`; этот контракт не менялся. |
| 15 | Landing: gzip nginx; immutable только для `/_astro/`; обычная статика — короткий cache с ревалидацией, HTML — no-cache. Inter self-hosted с preload; GSI загружается около формы или при взаимодействии. Favicon 32px — 1.6 KB вместо 50.5 KB; отдельный Apple touch icon. |
| 16 | DB_MAX_CONNS для API/worker: default 20, валидация и примеры env/Compose. Существующий cmd/worker не удалён; размещение фонового writing worker в API не менялось, поскольку перенос требует отдельного решения по эксплуатации. |
| 17 | Завершённая Full Mock сессия больше не опрашивается после завершения AI jobs; deadline вызывает точечный refetch. Активный session polling сохранён для прежней межвкладочной/межустройственной синхронизации. ABANDONED attempt не опрашивается. |
| 18 | Репозитории blog не объединялись: это архитектурная миграция, а не оптимизация текущего запроса. |

Таким образом, безопасные изменения реализованы; несовместимые предложения
исходного аудита не выдаются за выполненные. Новые правила для оценок,
таймеров, публикации и прав доступа не вводились.

Также общий date formatter вынесен из страницы разбора в `src/lib/date.ts`:
Progress больше не импортирует весь модуль AttemptReviewPage ради даты.
Для batch-import проверен откат поздней SQL-ошибки без частичных материалов,
а для Listening — сохранность 40 вопросов и порядка частей.

## Измерения production-сборки web

Одинаковая локальная среда, размеры конкретных JS-чанков, десятичные KB.
Gzip вычислен одинаковым способом для обеих сборок.

| Чанк | До, KB | После, KB | Gzip до → после, KB |
|---|---:|---:|---:|
| index | 421.2 | 357.3 | 126.3 → 109.3 |
| _app | 169.5 | 151.0 | 53.5 → 47.9 |
| login | 132.3 | 19.9 | 43.0 → 7.9 |

Отдельно: chat — 20.1 KB / 6.9 KB gzip, motion-features — 37.3 KB / 14.0 KB gzip.
Уменьшение login chunk не равно уменьшению всех запросов страницы: features
анимации загружаются отдельным chunk. Это не production latency benchmark;
обещание исходного аудита о 100–150 KB gzip выигрыша не подтверждено замерами.

Проверено production HTTP static serving: `Content-Encoding: br`,
`Vary: Accept-Encoding`, `Cache-Control: public, max-age=31536000, immutable`.

## Проверки

- `TEST_DATABASE_URL=<isolated local test DB> go test ./...` — все пакеты.
- `go test -race` для reading/listening/writing/attempts/config; `go vet ./...`.
- Web: TypeScript, targeted ESLint, production build, 20 regression tests,
  32 Playwright tests (включая polling, pagination, lazy chat и Full Mock).
- Landing: Astro check/build; 10 phone unit tests; 31 E2E assertion для формы, маски телефона,
  согласия, языков, навигации и mobile layout; отдельный тест GSI/local fonts/
  Google signup на desktop и mobile.
- GSI/API в browser tests замоканы; реальные Google credentials не использованы.
- Nginx-конфиг не запускался локально: nginx отсутствует, Docker недоступен.
  Перед rollout выполнить `nginx -t` в целевом окружении.

## Rollout

1. Применить backend миграцию `000029_attempt_history_index` обычным механизмом
   миграций проекта. Обычный CREATE INDEX может блокировать записи; для большой
   production таблицы нужен согласованный migration window.
2. Развернуть backend с `/attempts/{id}/status` и cursor history.
3. Затем web frontend, затем landing. Frontend нельзя выкатывать первым.
4. Настроить DB_MAX_CONNS с учётом суммы лимитов API/worker/реплик и резерва
   PostgreSQL; default остаётся 20, автоматического увеличения/уменьшения нет.
5. Проверить cache/compression headers через production nginx и права на
   архивированные media. Внутренний nginx лендинга проверить через `nginx -t`.

Production-деплой не выполнялся. Push в `dev` запускает только CI-проверки;
production build/deploy в текущих workflows запускается при push в `main`.
