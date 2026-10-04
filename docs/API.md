# REST API v1

Base URL: `/api/v1`. Успешные ответы — чистые JSON-объекты без дополнительной
обёртки. Все даты-времена — ISO 8601/RFC 3339 в UTC, `examDate` — `YYYY-MM-DD`.

## Ошибки

```json
{
  "code": "VALIDATION_ERROR",
  "message": "Request validation failed",
  "details": {
    "targetBand": "must be between 0 and 9 in increments of 0.5"
  },
  "requestId": "8c7922549493eddf841e050d6e6bed38"
}
```

Сервер не возвращает SQL, stack trace, password/token values или внутренние
тексты ошибок.

## Phone verification

Номер передаётся в E.164 с `+`, например `+77001234567`. Пробелы, дефисы и
скобки нормализуются. Один verification token можно использовать только один
раз и только для purpose, для которого он был выдан.

### `POST /phone-verifications`

Отправляет шестизначный код через одобренный WhatsApp authentication template:

```json
{
  "phone": "+77001234567",
  "purpose": "waitlist"
}
```

`purpose`: `waitlist` или `registration`. Ответ `202`:

```json
{
  "verificationId": "74d36aae-c984-474c-a507-41aaf3cd8bd9",
  "expiresAt": "2026-08-02T12:05:00Z",
  "retryAfter": 60
}
```

Слишком ранний resend возвращает `429 VERIFICATION_RESEND_TOO_SOON`. Пока
Infobip выключен, endpoint возвращает `503 WHATSAPP_NOT_CONFIGURED`.

### `POST /phone-verifications/{verificationId}/confirm`

```json
{
  "phone": "+77001234567",
  "purpose": "waitlist",
  "code": "123456"
}
```

Ответ `200`:

```json
{
  "verificationToken": "<single-use-token>",
  "expiresAt": "2026-08-02T12:10:00Z"
}
```

Неверный, истёкший или уже использованный challenge даёт одинаковый
`422 INVALID_VERIFICATION_CODE`.

## Waitlist

Заявки waitlist хранятся в таблице `users` со статусом `WAITING`/`INVITED`
(без пароля); регистрация через `POST /auth/google/complete` дозавершает
ту же строку и переводит её в `REGISTERED`.

### `POST /waitlist`

Для добавления в waitlist нужен Google-аккаунт: передайте Google ID token,
email берётся из подтверждённых данных аккаунта. Номер телефона принимается
без WhatsApp-проверки:

```json
{
  "firstName": "Ada",
  "lastName": "Lovelace",
  "phone": "+77001234567",
  "source": "landing",
  "googleToken": "<google-id-token>",
  "ref": "instagram"
}
```

`firstName`, `lastName`, `phone`, `googleToken` обязательны (каждое имя — от
2 до 100 символов), `source` необязателен. `ref` необязателен — реферальный
код пригласившего или тег кампании (`^[a-z0-9_-]{1,64}$` после lower-case);
невалидный `ref` молча игнорируется и не блокирует запись. Ответ `201`
содержит созданную заявку:

```json
{
  "id": "2fffd066-e824-4adc-b099-c7c266b7513a",
  "phone": "+77001234567",
  "email": "ada@example.com",
  "firstName": "Ada",
  "lastName": "Lovelace",
  "source": "landing",
  "referralCode": "k7m2p9xq",
  "referredByCode": "instagram",
  "referrals": 0,
  "status": "WAITING",
  "createdAt": "2026-08-02T12:00:35Z"
}
```

Повторный номер даёт `409 WAITLIST_ENTRY_EXISTS`; отсутствующий,
недействительный или не содержащий подтверждённый email Google token, а
также некорректные поля — `422 VALIDATION_ERROR` с деталями по `firstName`,
`lastName`, `phone`, `googleToken`.

### `POST /waitlist/check`

Проверяет дубликаты до попытки записи: по Google ID token определяет, есть
ли уже заявка у этого аккаунта, а при переданном `phone` — занят ли номер.
Один Google-аккаунт и один номер соответствуют одной заявке:

```json
{
  "phone": "+77001234567",
  "googleToken": "<google-id-token>"
}
```

Ответ `200`:

```json
{
  "accountRegistered": false,
  "phoneTaken": false
}
```

`googleToken` обязателен и должен быть действительным (`422 VALIDATION_ERROR`),
`phone` необязателен; если аккаунт уже записан, проверка номера не выполняется
и `phoneTaken` всегда `false`.

### `GET /admin/waitlist`

Список заявок waitlist для супер-админа. Требует
`Authorization: Bearer <accessToken>` с ролью `ADMIN`; список админов
складывается из `SUPER_ADMIN_EMAILS` (CSV в env) и таблицы `super_admins`
(управляется через `/admin/super-admins`).

Ответ `200`:

```json
{
  "entries": [
    {
      "id": "b7c1...",
      "phone": "+77001234567",
      "email": "ada@example.com",
      "firstName": "Ada",
      "lastName": "Lovelace",
      "source": "landing",
      "referralCode": "k7m2p9xq",
      "referredByCode": "instagram",
      "referrals": 3,
      "status": "WAITING",
      "createdAt": "2026-08-02T12:00:35Z"
    }
  ],
  "total": 1
}
```

`referrals` — число заявок, указавших `referralCode` этой записи как `ref`;
`referredByCode` — `null`, если запись пришла без атрибуции. Токен отсутствует
или недействителен — `401 UNAUTHENTICATED`; роль ниже `ADMIN` —
`403 FORBIDDEN`.

### `GET /admin/super-admins`

Список супер-админов. Та же авторизация, что у `GET /admin/waitlist`.
Ответ `200`:

```json
{
  "admins": [
    { "email": "owner@example.com", "source": "env" },
    { "email": "second@example.com", "source": "db" }
  ]
}
```

`source: "env"` — админ задан переменной `SUPER_ADMIN_EMAILS` и не может быть
удалён через API; `source: "db"` — админ добавлен в таблицу `super_admins`.

### `POST /admin/super-admins`

Добавить супер-админа. Тело:

```json
{ "email": "new-admin@example.com" }
```

Ответ `201` без тела. Невалидный email — `422 VALIDATION_ERROR`; дубликат
трактуется как успех (идемпотентно).

### `DELETE /admin/super-admins/{email}`

Удалить супер-админа из таблицы `super_admins`. Ответ `204` без тела.
Попытка удалить админа, заданного через `SUPER_ADMIN_EMAILS`, — `409
ADMIN_PROTECTED` (его можно убрать только правкой env).

## Auth

`POST /auth/register` и `POST /auth/login` отключены (`404`): создание
аккаунта и вход доступны только через Google. Успешная аутентификация выдаёт
HttpOnly refresh cookie с `Path=/api/v1/auth` и настраиваемыми
`SameSite`/`Secure`; браузер отправляет её с `credentials: include`.

### `POST /auth/google`

```json
{ "googleToken": "<google-id-token>" }
```

Вход по Google ID token (Google Sign-In). Существующий аккаунт получает сессию
в формате auth-ответа; если супер-админ (`SUPER_ADMIN_EMAILS` или таблица
`super_admins`) входит в первый раз, аккаунт создаётся с ролью `ADMIN`, а роль
существующего супер-админа повышается до `ADMIN` (понижения здесь никогда не
происходит). Лид из waitlist (строка `users` со статусом `WAITING`/`INVITED`)
с email супер-админа апгрейдится до `ADMIN` на той же строке.

Если аккаунта нет, ответ `200` возвращает pending-регистрацию (без сессии и
refresh cookie):

```json
{
  "registrationRequired": true,
  "registrationToken": "<jwt, 30 минут>",
  "profile": {
    "email": "ada@example.com",
    "name": "Ada Lovelace",
    "phone": "+77001234567"
  }
}
```

`profile.name` берётся из Google-аккаунта, а если Google-профиль совпал с
лидом waitlist (по `google_sub` или email) — из `firstName`/`lastName` заявки,
`profile.phone` — из заявки (поле отсутствует, если заявки не было). Клиент
показывает форму завершения регистрации и вызывает
`POST /auth/google/complete`. Ошибки: `401 GOOGLE_TOKEN_INVALID`
(несуществующий/невалидный токен или неподтверждённый email),
`422 VALIDATION_ERROR` (пустой `googleToken`).

### `POST /auth/google/complete`

```json
{
  "registrationToken": "<jwt из /auth/google>",
  "name": "Ada Lovelace",
  "phone": "+77001234567",
  "acceptedTerms": true
}
```

Завершает регистрацию по pending-токену: создаёт студента или дозавершает
строку лида waitlist (статус становится `REGISTERED`), Google identity заменяет
WhatsApp-проверку телефона. Ответ `201` — auth-формат с refresh cookie.
Ошибки: `401 GOOGLE_TOKEN_INVALID` (токен регистрации недействителен или
истёк), `409 EMAIL_ALREADY_EXISTS`, `409 PHONE_ALREADY_EXISTS`,
`422 VALIDATION_ERROR` (`name`, `phone`, `acceptedTerms`). Пароль не создаётся.
Повторная попытка с тем же токеном после временной ошибки разрешена.

### `POST /auth/refresh`

Пустой POST с refresh cookie. Ответ `200` имеет auth-формат и ротирует cookie.
Предыдущий token после успешного ответа недействителен. Конкурентное или
повторное применение даёт `401 INVALID_REFRESH_TOKEN`, очищает cookie и
инициирует отзыв активных сессий.

### `POST /auth/logout`

Пустой POST с refresh cookie. Успешный и идемпотентный с точки зрения клиента
ответ: `204` без body; cookie удаляется. Временный JSON-body fallback с полем
`refreshToken` пока сохранён для совместимости старых API-клиентов.

### `GET /users/me`

Требует `Authorization: Bearer <accessToken>`. Ответ `200` — объект `user` из
auth-ответа без токенов.

## Profile

Все endpoints требуют Bearer token.

### `GET /profile`

Ответ `200` — тот же profile/user object, что `GET /users/me`.

### `PATCH /profile`

Разрешены только:

```json
{
  "displayName": "Ada Byron",
  "timezone": "Asia/Qyzylorda"
}
```

Достаточно одного поля. Email, role и currentBand через этот endpoint изменить
нельзя.

### `PUT /profile/goal`

```json
{
  "targetBand": 7.5,
  "examDate": "2026-10-15",
  "examType": "academic"
}
```

`targetBand` — 0–9 с шагом 0.5. `examType` — `academic` или `general`.
`examDate` не может быть раньше текущей даты в timezone профиля. Ответ `200` —
обновлённый profile object.

## Admin access

### `GET /admin/access`

Требует Bearer token с ролью `EDITOR` или `ADMIN`. Обычный `STUDENT` получает
`403 FORBIDDEN`. Ответ `200` подтверждает, что проверку выполнил backend:

```json
{
  "userId": "2c6eea74-968f-4af4-9f30-929bbf47bc45",
  "role": "ADMIN"
}
```

Публичного API изменения ролей нет. Первый администратор назначается CLI-командой
из README; все refresh-сессии пользователя при этом отзываются.

## Admin Reading materials

Все endpoints требуют `EDITOR` или `ADMIN`. Публикация дополнительно требует
`ADMIN`. Материал содержит стабильную запись каталога и неизменяемые версии
текста. Каждое сохранение создаёт новую версию; поле `revision` используется для
optimistic locking.

### `GET /admin/reading/materials`

Ответ `200`: `{ "items": [...] }`. Пустой список всегда представлен `[]`.

### `POST /admin/reading/materials`

```json
{
  "slug": "urban-wildlife",
  "examType": "academic",
  "difficulty": "intermediate",
  "title": "Urban wildlife",
  "description": "Practice passage",
  "body": "Full passage text...",
  "sourceTitle": "Licensed source",
  "sourceUrl": "https://example.com/source"
}
```

`slug` можно не передавать при создании — backend сгенерирует его. `body` должен
содержать 50–100000 символов. Ответ `201` — созданный material с `revision: 1`.

### `GET /admin/reading/materials/{id}`

Возвращает текущую редактируемую версию материала.

### `PUT /admin/reading/materials/{id}`

Принимает полный объект сохранения и обязательный актуальный `revision`.
Создаёт новую неизменяемую версию текста и увеличивает revision. Устаревший
revision возвращает `409 REVISION_CONFLICT`, повторяющийся slug —
`409 READING_SLUG_EXISTS`.

При обновлении `kind: "TEST"` можно передать `refreshPassages: true`, чтобы
новая версия теста ссылалась на текущие версии его passages. Без этого флага
сохраняются прежние ссылки. Порядок и ID passages не меняются; старые версии
теста, опубликованный снимок и попытки учеников остаются неизменными.
Флаг разрешён только для обновления TEST, иначе — `422 VALIDATION_ERROR`.

### `POST /admin/reading/materials/{id}/publish`

```json
{ "revision": 2 }
```

Фиксирует текущую версию как опубликованную. Только `ADMIN`; `EDITOR` получает
`403 FORBIDDEN`. Последующие изменения создают новый черновик и выставляют
`hasUnpublishedChanges: true`, не изменяя опубликованную версию.

## Reading materials (студент)

Требуют Bearer token. Видны только опубликованные материалы.

### `GET /reading/materials`

Список опубликованных материалов (без текста passage и вопросов), свежие
первые. Ответ `200`:

```json
{
  "items": [
    {
      "id": "3f6f26a1-9b2c-4b6e-9d6d-2f0d8f2a1c10",
      "slug": "cambridge-18-reading-1",
      "examType": "academic",
      "difficulty": "intermediate",
      "title": "Cambridge 18 Reading Passage 1",
      "description": "",
      "publishedAt": "2026-08-01T09:00:00Z"
    }
  ]
}
```

### `GET /reading/materials/{materialId}`

Публичная структура опубликованной версии: текст passage и вопросы БЕЗ
`answer` и `explanation`. Материал не опубликован или не найден —
`404 READING_MATERIAL_NOT_FOUND`. Ответ `200`:

```json
{
  "id": "3f6f26a1-9b2c-4b6e-9d6d-2f0d8f2a1c10",
  "slug": "cambridge-18-reading-1",
  "examType": "academic",
  "difficulty": "intermediate",
  "title": "Cambridge 18 Reading Passage 1",
  "description": "",
  "body": "The full passage text…",
  "questionGroups": [
    {
      "id": "d43f2c73-4e0f-5a3b-1f6d-2c3b4e5f6a78",
      "position": 1,
      "type": "true_false_not_given",
      "instructions": "Do the following statements agree with the information given in the passage?",
      "questions": [
        {
          "id": "b21d0a51-2c8d-4e1f-9d4b-0a1f2e3d4c56",
          "position": 1,
          "prompt": "The library moved to North Campus in 2015.",
          "content": {},
          "points": 1
        }
      ]
    }
  ]
}
```

## Dashboard

### `GET /dashboard`

Требует Bearer token. Ответ нового пользователя:

```json
{
  "profile": {
    "currentBand": null,
    "targetBand": null,
    "examDate": null
  },
  "recommendedAction": {
    "type": "START_DIAGNOSTIC",
    "title": "Определите стартовый уровень",
    "description": "Короткая диагностика поможет подобрать подходящую сложность.",
    "target": "/dashboard/practice"
  },
  "todayPlan": [],
  "skillProgress": [
    {
      "skill": "listening",
      "estimatedBand": null,
      "accuracyPercent": null,
      "completedTasks": 0
    },
    {
      "skill": "reading",
      "estimatedBand": null,
      "accuracyPercent": null,
      "completedTasks": 0
    },
    {
      "skill": "writing",
      "estimatedBand": null,
      "accuracyPercent": null,
      "completedTasks": 0
    },
    {
      "skill": "speaking",
      "estimatedBand": null,
      "accuracyPercent": null,
      "completedTasks": 0
    }
  ],
  "unreadNotifications": 0
}
```

## Attempts

Попытки прохождения тестов студентами. Все endpoint'ы требуют Bearer token и
доступны только владельцу попытки (чужая попытка даёт `404 NOT_FOUND`).
Поддерживаются `materialType: "listening"`, `"reading"`, `"writing"` и `"speaking"`. Попытка привязана к
конкретной версии материала (`materialVersionId`): грейдинг и разбор всегда
идут по той версии, на которой попытка была начата.

Формат ответа студента зависит от типа вопроса:

- выбор варианта (multiple choice, matching, labelling): `{"optionId": "A"}`
  или `{"optionIds": ["A", "C"]}` — буквы в верхнем регистре;
- true/false/not given и yes/no/not given (reading): `{"value": "TRUE"}` —
  одно из `TRUE`/`FALSE`/`NOT_GIVEN` или `YES`/`NO`/`NOT_GIVEN`, сравнение
  без учёта регистра и пробелов по краям;
- текстовый ответ (completion, short answer): `{"value": "Green Street"}` —
  сравнение без учёта регистра и пробелов по краям.

### `POST /listening/tests/{testId}/attempts`

Старт попытки. Тест должен быть опубликован, иначе `404 NOT_FOUND`. Если у
пользователя уже есть незавершённая попытка на этот тест — возвращается она
(`200`), иначе создаётся новая (`201`). Ответ содержит попытку и публичную
структуру теста (без правильных ответов):

```json
{
  "attempt": {
    "id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
    "materialType": "listening",
    "materialId": "3f6f26a1-9b2c-4b6e-9d6d-2f0d8f2a1c10",
    "materialVersionId": "a12c9f40-1b7c-4d0e-8c3a-9f0e1d2b3a45",
    "status": "IN_PROGRESS",
    "score": null,
    "maxScore": null,
    "band": null,
    "startedAt": "2026-08-02T12:00:00Z",
    "submittedAt": null
  },
  "test": {
    "id": "3f6f26a1-9b2c-4b6e-9d6d-2f0d8f2a1c10",
    "slug": "cambridge-18-test-1",
    "examType": "academic",
    "title": "Cambridge 18 Test 1",
    "description": "",
    "durationMinutes": 40,
    "parts": []
  }
}
```

### `POST /reading/materials/{materialId}/attempts`

Та же семантика, что и для listening: материал должен быть опубликован,
существующая `IN_PROGRESS` попытка возвращается с `200`, новая создаётся с
`201`. Ответ содержит попытку и публичную структуру материала (ключ
`material` вместо `test`, структура — как в
`GET /reading/materials/{materialId}`):

```json
{
  "attempt": {
    "id": "8d0f778a-8536-41ef-a55c-f18ad2a01bf8",
    "materialType": "reading",
    "materialId": "3f6f26a1-9b2c-4b6e-9d6d-2f0d8f2a1c10",
    "materialVersionId": "b23d0a51-2c8d-4e1f-9d4b-0a1f2e3d4c56",
    "status": "IN_PROGRESS",
    "score": null,
    "maxScore": null,
    "band": null,
    "startedAt": "2026-08-02T12:00:00Z",
    "submittedAt": null
  },
  "material": {
    "id": "3f6f26a1-9b2c-4b6e-9d6d-2f0d8f2a1c10",
    "slug": "cambridge-18-reading-1",
    "examType": "academic",
    "difficulty": "intermediate",
    "title": "Cambridge 18 Reading Passage 1",
    "description": "",
    "body": "The full passage text…",
    "questionGroups": []
  }
}
```

### `POST /writing/materials/{materialId}/attempts`

Стартует Writing-попытку по опубликованному материалу. Ответ содержит
`attempt` и `material` с двумя заданиями (`tasks`): Task 1 и Task 2. Для
Academic Task 1 материал содержит `visualType` и может содержать `visualUrl`;
для General Task 1 — `letterTone`. В черновиках каждого задания используется
идентификатор `tasks[].id` и текстовый ответ:

```json
{
  "answers": [
    {"questionId": "task-uuid", "answer": {"value": "My essay in English..."}}
  ]
}
```

При `POST /attempts/{attemptId}/submit` оба задания обязательны. Backend
отправляет их в настроенный OpenAI-compatible AI provider и возвращает попытку с итоговым band. Если
`AI_API_KEY` (или legacy `OPENROUTER_API_KEY`) не настроен, ответ — `503 AI_NOT_CONFIGURED`; при ошибке
провайдера — `502 AI_EVALUATION_FAILED`. Детали сданной Writing-попытки
(`GET /attempts/{attemptId}`) включают `writingEvaluation`: four criteria
`taskResponse`, `coherence`, `lexicalResource`, `grammar`, их band и feedback,
а также summary и рекомендации по каждой задаче.

### `GET /speaking/materials` и `GET /speaking/materials/{materialId}`

Возвращают опубликованные Speaking-материалы и их полную структуру. Материал
состоит ровно из трёх частей: `part1`, `part2`, `part3`. В Part 1 и Part 3
заданы вопросы (`questions`); в Part 2 — cue card (`cueCard`), время на
подготовку и ответ (`preparationSeconds`, `responseSeconds`).

### `POST /speaking/materials/{materialId}/attempts`

Стартует Speaking-попытку по опубликованному материалу. Повторный вызов для
незавершённой попытки возвращает её с `200`, новая попытка создаётся с `201`.
Ответ содержит `attempt` и `material` с тремя частями Speaking.

### `POST /attempts/{attemptId}/recordings`

Сохраняет либо заменяет аудиозапись одной части Speaking. Запрос
`multipart/form-data`: поле `partId` содержит UUID части, `recording` — аудио
`webm`, `ogg`, `wav`, `mp3`, `m4a` или `aac`. Максимальный размер одной записи
— 12 MiB. Ответ `201` содержит метаданные записи; файл доступен владельцу по
`GET /attempts/{attemptId}/recordings/{partId}`.

### Speaking submit и результат

Для Speaking `POST /attempts/{attemptId}/submit` требует запись или текстовую
расшифровку для каждой из трёх частей. Записи передаются аудио-совместимой
настроенной AI-модели для расшифровки и учебной оценки. Для text-only моделей
нужен готовый транскрипт или отдельный STT. Детали сданной попытки
(`GET /attempts/{attemptId}`) содержат `speakingEvaluation`: общий band,
`fluency`, `lexicalResource`, `grammar`, `pronunciation`, `summary`, а также
расшифровку и рекомендации по каждой части. Если AI не настроен —
`503 AI_NOT_CONFIGURED`; если оценка не была получена —
`502 AI_EVALUATION_FAILED`.

## Full Mock Test

Full Mock объединяет четыре независимые попытки в одну экзаменационную сессию:
`listening → reading → writing → speaking`. Каждая дочерняя попытка закрепляется
за опубликованной версией материала при старте сессии; она не переиспользует
отдельную практическую попытку пользователя.

### `GET /full-mocks/overview`

Требует авторизацию. Возвращает `examType` из профиля, `durationMinutes: 165`,
`ready`, `activeSession` (или `null`) и `banks` в порядке секций. Каждая запись
содержит `skill`, `total`, `completed`, `remaining`, `isExhausted`.
Считаются уникальные доступные материалы с попыткой `SUBMITTED` или `PROCESSING`
как в практике, так и в Full Mock. Начатые и брошенные попытки не засчитываются;
повторные сдачи одного материала не увеличивают счётчик. Успешная сдача означает
завершение попытки, а не достижение определённого band.

Банк использует только полные опубликованные тесты: Listening — 4 части и
40 баллов, Reading — `TEST` с 3 текстами и 40 баллами, Writing — 2 задания,
Speaking — 3 части. Reading/Writing соответствуют Academic/General из профиля;
Listening/Speaking общие. Draft и archived материалы не участвуют в новом подборе.
Пустой банк означает `ready: false`, но не `isExhausted: true`.
Без формата экзамена `examType: ""`, `ready: false`; существующую сессию можно продолжить.

### `POST /full-mocks/start`

Body: `{"restart": false}` (можно без body). Подбирает случайный **непройденный**
тест для каждой секции. Если непройденных нет, выбирает случайный повтор,
исключая материал последней mock-сессии, когда есть альтернативы.
Фронтенд предупреждает о повторах по `isExhausted` до запуска, а также после
сдачи последнего Listening. Новый опубликованный материал автоматически
возвращает секцию в состояние с непройденными тестами. Новая версия уже
выполненного материала не сбрасывает его счётчик.

Создание сессии, четырёх attempts и привязок атомарно; одновременные запросы
без `restart` возвращают одну сессию. Новая сессия: `201`, продолжение: `200`.
`restart: true` закрывает незавершённую сессию и делает новый подбор. Если нового
экзамена собрать нельзя, старая сессия и её ответы сохраняются.
Ошибки: `422 EXAM_TYPE_REQUIRED`, `409 FULL_MOCK_BANK_INCOMPLETE`.

Отдельная запись в `full_mock_tests` **не создаётся**. Метаданные и лимит времени
хранятся в `full_mock_sessions`, выбранные версии — в attempts. Поле `mockTest`
в ответе оставлено для совместимости экзаменационного плеера и синтезируется
из сессии; `mockTestId` для сгенерированных сессий — нулевой UUID.
Состав, версии, `currentSection` и `deadlineAt` сохраняются при продолжении.
История старых ручных экзаменов остаётся доступна владельцу.

Старые публичные endpoints каталога/выбора (`GET /full-mocks`,
`GET /full-mocks/{mockId}`, `POST /full-mocks/{mockId}/sessions`) удалены.

### `GET /full-mock-sessions/{sessionId}` и `POST /full-mock-sessions/{sessionId}/advance`

Получение сессии доступно только владельцу. `advance` разрешён только после
сдачи текущей секции; обратного перехода API не предоставляет. После Speaking
сессия получает статус `SUBMITTED`, а ответ содержит band каждой секции и
`overallBand` — среднее четырёх band с округлением до 0,5.

### Управление наборами Full Mock

Ручная сборка и публикация отключены. `EDITOR`/`ADMIN` могут читать архив через
`GET /admin/full-mocks` и `GET /admin/full-mocks/{mockId}`; `ADMIN` может архивировать
старую запись через `POST /admin/full-mocks/{mockId}/archive` с `revision`.
Создание и редактирование недоступны через API и интерфейс.
Для наполнения банка нужно публиковать обычные тесты соответствующих секций.

Перед запуском обновлённого API примените миграцию `000030_dynamic_full_mock`.
Обратная миграция намеренно запрещена, пока есть сгенерированные сессии:
она не должна удалять историю экзаменов.

### `PUT /attempts/{attemptId}/answers`

Сохраняет черновик ответов (идемпотентно, upsert по `questionId`). Только для
попытки в статусе `IN_PROGRESS`, иначе `409 ATTEMPT_ALREADY_SUBMITTED`.

```json
{
  "answers": [
    {"questionId": "b21d0a51-2c8d-4e1f-9d4b-0a1f2e3d4c56", "answer": {"optionId": "B"}},
    {"questionId": "c32e1b62-3d9e-4f2a-0e5c-1b2a3f4e5d67", "answer": {"value": "Green Street"}}
  ]
}
```

Ответ `200`: `{"saved": 2}`.

### `POST /attempts/{attemptId}/submit`

Сдача попытки. Принимает тот же body, что и сохранение черновика (финальные
ответы мержатся поверх ранее сохранённых), грейдит, проставляет
`score`/`maxScore`/`band`/`submittedAt` и переводит попытку в `SUBMITTED`.
Повторный submit — `409 ATTEMPT_ALREADY_SUBMITTED`. Band считается по таблице
IELTS для skill'а попытки: у listening одна таблица, у reading — отдельные
таблицы для `academic` и `general` (выбор по `examType` материала); raw score
масштабируется к 40 вопросам. После грейдинга обновляется
`user_skill_progress` для соответствующего skill (`completedTasks +1`,
`accuracyPercent` и `estimatedBand` — по этой попытке). Ответ `200` — попытка:

```json
{
  "id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
  "materialType": "listening",
  "materialId": "3f6f26a1-9b2c-4b6e-9d6d-2f0d8f2a1c10",
  "materialVersionId": "a12c9f40-1b7c-4d0e-8c3a-9f0e1d2b3a45",
  "status": "SUBMITTED",
  "score": 34,
  "maxScore": 40,
  "band": 7.5,
  "startedAt": "2026-08-02T12:00:00Z",
  "submittedAt": "2026-08-02T12:31:07Z"
}
```

### `GET /attempts?materialType=listening`

История попыток пользователя. `materialType` — `listening`, `reading`,
`writing` или `speaking`, другие значения — `422 VALIDATION_ERROR`; без
параметра возвращаются попытки всех типов. `testTitle`/`testSlug` — название и
slug материала. Без `limit` и `cursor` сохранён прежний полный ответ.

Для постраничного чтения: `GET /attempts?limit=50&cursor=<nextCursor>`.
`limit` — 1–100 (по умолчанию 50, если указан только `cursor`). Ответ содержит
`items` и необязательный `nextCursor`; отсутствие курсора означает конец
истории. Курсор непрозрачный: его нужно передавать без изменений вместе с тем
же `materialType`. Порядок страниц — `(COALESCE(submitted_at, started_at), id)`
по убыванию, как в истории на странице Progress. Неверные параметры дают
`422 VALIDATION_ERROR`. Все страницы ограничены текущим пользователем.
Ответ `200`:

```json
{
  "items": [
    {
      "id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
      "materialType": "listening",
      "materialId": "3f6f26a1-9b2c-4b6e-9d6d-2f0d8f2a1c10",
      "materialVersionId": "a12c9f40-1b7c-4d0e-8c3a-9f0e1d2b3a45",
      "status": "SUBMITTED",
      "score": 34,
      "maxScore": 40,
      "band": 7.5,
      "startedAt": "2026-08-02T12:00:00Z",
      "submittedAt": "2026-08-02T12:31:07Z",
      "testTitle": "Cambridge 18 Test 1",
      "testSlug": "cambridge-18-test-1"
    }
  ]
}
```

### `GET /attempts/mistakes/attempts`

Компактный список для банка ошибок. Query: `materialType` (по умолчанию
`reading`, также `listening`, `writing`, `speaking`), `page` (1–100000,
по умолчанию 1), `limit` (1–50, по умолчанию 12). Неверные параметры → `422`.

Ответ: `{ "items": [...], "page": 1, "hasNext": false }`. Каждый item содержит
поля списка попыток и `mistakeCount`: число неверных/пропущенных вопросов для
Reading/Listening, `null` для Writing/Speaking (используйте `band`). Считаются
вопросы, а не потерянные баллы; используются версии материалов, закреплённые
за попыткой, включая passages Reading-теста.

Возвращаются только `SUBMITTED`: объективные попытки с `score < maxScore`,
Writing/Speaking с сохранённой оценкой. Сортировка: `submittedAt DESC, id DESC`.
Пагинация выполняется до загрузки вопросов; ответы не содержат текстов,
транскриптов, правильных ответов или подробного AI-feedback.

### `GET /attempts/{attemptId}/mistakes`

Разбор выбранной попытки своего пользователя (чужая попытка → `404`).
Ответ содержит `attempt`, `review` (только неверные/пропущенные вопросы),
`contexts` и, при наличии, `writingEvaluation` / `speakingEvaluation`.
Каждый вопрос `review` содержит `contextIndex` — индекс в `contexts`.
`passageBody` и `transcript` передаются один раз в соответствующем context,
остальные поля вопроса остаются как в полном разборе. Пустые списки — `[]`.
Контракт полного разбора и старый bulk endpoint `/attempts/mistakes`
сохранены для совместимости.

### `GET /attempts/{attemptId}/status`

Лёгкий snapshot для ожидания фоновой AI-оценки (требует Bearer token).
Возвращает только ID, статус попытки и, если есть, `writingAssessment` или
`speakingAssessment` для активной попытки:

```json
{
  "id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
  "status": "PROCESSING",
  "writingAssessment": {"status": "QUEUED", "attempts": 0}
}
```

Статусы попытки: `IN_PROGRESS`, `PROCESSING`, `SUBMITTED`, `ABANDONED`.
Job: `QUEUED`, `PROCESSING`, `READY`, `FAILED`; может содержать `errorCode` и
`errorMessage`. Отсутствующий job не включается в ответ. Ответ не содержит
answers, recordings, transcripts или evaluations; `Cache-Control: no-store`.
Чужая и несуществующая попытка одинаково возвращают `404 NOT_FOUND`.

Клиент опрашивает только пока попытка `PROCESSING`, с интервалом 2 → 5 → 10 с.
После выхода из `PROCESSING` один раз загружает полный detail — в том числе
при возврате в `IN_PROGRESS` после неудачной оценки. Сначала деплоится backend
с этой ручкой, затем frontend, использующий её.

### `GET /attempts/{attemptId}`

Детали попытки. Для `IN_PROGRESS` — только сохранённые ответы, без правильных:

```json
{
  "id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
  "materialType": "listening",
  "materialId": "3f6f26a1-9b2c-4b6e-9d6d-2f0d8f2a1c10",
  "materialVersionId": "a12c9f40-1b7c-4d0e-8c3a-9f0e1d2b3a45",
  "status": "IN_PROGRESS",
  "score": null,
  "maxScore": null,
  "band": null,
  "startedAt": "2026-08-02T12:00:00Z",
  "submittedAt": null,
  "answers": [
    {"questionId": "b21d0a51-2c8d-4e1f-9d4b-0a1f2e3d4c56", "answer": {"optionId": "B"}}
  ]
}
```

Для `SUBMITTED` — разбор по каждому вопросу с правильным ответом и
объяснением:

```json
{
  "id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
  "materialType": "listening",
  "materialId": "3f6f26a1-9b2c-4b6e-9d6d-2f0d8f2a1c10",
  "materialVersionId": "a12c9f40-1b7c-4d0e-8c3a-9f0e1d2b3a45",
  "status": "SUBMITTED",
  "score": 34,
  "maxScore": 40,
  "band": 7.5,
  "startedAt": "2026-08-02T12:00:00Z",
  "submittedAt": "2026-08-02T12:31:07Z",
  "review": [
    {
      "questionId": "b21d0a51-2c8d-4e1f-9d4b-0a1f2e3d4c56",
      "number": 1,
      "prompt": "What does the speaker say about the library?",
      "answer": {"optionId": "B"},
      "isCorrect": true,
      "pointsAwarded": 1,
      "correctAnswer": {"optionId": "B"},
      "explanation": "The speaker mentions the library moved to North Campus."
    }
  ]
}
```

## Health

- `GET /health/live` → `200 {"status":"ok"}`;
- `GET /health/ready` → `200`, только если PostgreSQL и Redis отвечают;
- при отказе зависимости readiness → `503` с безопасными статусами
  `postgres`/`redis`, без внутреннего текста подключения.
