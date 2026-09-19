# Общая dev-БД

## Подключение на этом компьютере

Backend текущего `dev` запущен отдельным Compose-проектом `ielts-shared-dev`:

- API: `http://localhost:8080`.
- Проверка PostgreSQL и Redis: `http://localhost:8080/health/ready`.
- PostgreSQL: `a1-postgres1.alem.ai:30100`, база `ielts`, пользователь `farkhat1`.
- Пароль и DATABASE_URL: только в приватном `.env.dev`, исключённом из Git и Docker build context.
- Фронтенд использует `VITE_API_BASE_URL=http://localhost:8080`; пароль БД фронтенду не нужен.

Проверено 2026-09-12: до настройки в общей БД не было пользовательских таблиц.
Применены миграции 1–18 текущего `dev`, `schema_migrations.version=18`, `dirty=false`,
создано 29 таблиц в `public`. Аккаунты, материалы и попытки не переносились и не создавались.

**На этом порту PostgreSQL не поддерживает TLS** (ответ `N` на SSLRequest).
Сейчас используется `sslmode=disable` для временного dev-подключения по повторной просьбе
владельца окружения после предупреждения. Запросы и данные не шифруются на уровне PostgreSQL.
Не загружайте реальные данные студентов; для них сначала нужен TLS с проверкой сертификата
или защищённый туннель. Пароль, переданный в переписке, рекомендуется заменить у провайдера
и затем обновить только в `.env.dev`.

## Команды PowerShell

Из каталога `ielts-after-cigarette-back`:

```powershell
# Проверить конфигурацию без вывода секретов
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/shared-dev.ps1 Check

# Запуск/обновление backend с общими PostgreSQL, Redis и MinIO
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/shared-dev.ps1 Up

# Проверка реальных зависимостей
Invoke-RestMethod http://localhost:8080/health/ready

# Логи и остановка без удаления данных
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/shared-dev.ps1 Logs
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/shared-dev.ps1 Stop
```

Миграции запускаются отдельно, не автоматически при `Up`. На этом компьютере они уже применены.
После согласования следующих миграций с командой:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/shared-dev.ps1 Migrate
```

Эквивалент запуска без PowerShell-скрипта:

```sh
docker compose --env-file .env --env-file .env.dev -f docker-compose.dev.yml up -d --build backend
```

Фронтенд, из каталога `ielts-after-cigarette/iac-web`:

```powershell
npm.cmd run dev
```

Открыть `http://localhost:3000/app/`. После смены БД нужно заново войти:
аккаунтов и refresh-сессий из старой локальной БД здесь нет. Регистрация по email в коде
требует подтверждения телефона; SMS/WhatsApp в этом dev-окружении выключены. Google-вход
и bootstrap-администраторы зависят от `GOOGLE_CLIENT_ID` и `SUPER_ADMIN_EMAILS` в `.env`.
Пользователей и роли не создаём вручную без согласования.

## Настройка у коллеги

Создать `.env` из `.env.example`, `.env.dev` из `.env.dev.example`, передать реальные секреты
по защищённому каналу. Указать общие `REDIS_URL`, `OBJECT_STORAGE_*`, `AI_*`,
при необходимости CORS и `BACKEND_PORT`. Проверить конфигурацию и запустить `Up`.
Не публиковать вывод обычного `docker compose config`: он раскрывает подставленные секреты.

Общая БД **не означает общее аудиохранилище**. Backend поддерживает MinIO/S3-compatible
хранилище для Listening media и Speaking recordings. Все разработчики, работающие с общей БД,
должны использовать один и тот же endpoint и bucket через `OBJECT_STORAGE_*` в `.env.dev`.
Локальный `localhost` MinIO подходит только для разработки на одном компьютере: его файлы не
появятся у коллег автоматически. Текущий API привязан к `127.0.0.1` и не открыт в интернет.

`GET /health/ready` при включённом MinIO содержит `object_storage: ok`. Bucket создаётся
автоматически при старте, остаётся приватным, а загрузка и чтение выполняются через защищённые
API endpoints. Не публикуйте access/secret keys и не добавляйте `.env.dev` в Git.

Shared-dev использует OpenAI-compatible AI endpoint из `AI_CHAT_COMPLETIONS_URL`.
Writing оценивается текстовой моделью сразу. Для Speaking у text-only модели
`AI_SPEAKING_AUDIO_ENABLED=false`: понадобится транскрипт или отдельный STT-сервис.

## DataGrip

В настройках источника данных: PostgreSQL, host `a1-postgres1.alem.ai`, port `30100`,
database `ielts`, user `farkhat1`, пароль из `.env.dev`. TLS сейчас отсутствует — см. предупреждение выше.
Открыть вкладку **Schemas**, отметить **ielts → public**, нажать **Apply**, затем **Synchronize**.
Надпись `No schemas selected` на скриншоте означает, что схема не выбрана для отображения.

## Старая локальная среда и ветки

Остановлен только контейнер старого API `ielts-platform-backend-1` для освобождения порта 8080.
Локальные PostgreSQL, Redis, MinIO и их данные не удалены. `docker-compose.yml` и обычный `.env`
не переключались на внешнюю БД; для общей БД используйте именно `docker-compose.dev.yml`.

Не применяйте миграции сохранённой альтернативной ветки к общей БД: там версия 16 имела другую
схему (`productive`), здесь версия 16 — `writing`. Совпадение номера не означает совместимость.
Не использовать `force`, сброс схемы или `down -v` для исправления этого расхождения.

Чтобы вернуться к старому уже собранному локальному API без миграций:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/shared-dev.ps1 Stop
docker start ielts-platform-backend-1
```

При этом фронтенд также должен соответствовать старой ветке. Не запускайте обычный
`docker compose up` текущего `dev` против старой локальной схемы без отдельного плана перехода.
