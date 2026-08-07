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

### `POST /auth/register`

```json
{
  "name": "Ada Lovelace",
  "email": "ada@example.com",
  "phone": "+77001234567",
  "password": "correct horse battery staple",
  "confirmPassword": "correct horse battery staple",
  "acceptedTerms": true,
  "verificationToken": "<single-use-registration-token>"
}
```

`confirmPassword` необязателен для API-клиента, но если передан, обязан
совпадать. `acceptedTerms` обязателен. Перед регистрацией необходимо выполнить
phone verification с purpose `registration`. Ответ `201`:

```json
{
  "accessToken": "<jwt>",
  "tokenType": "Bearer",
  "expiresIn": 900,
  "user": {
    "id": "2c6eea74-968f-4af4-9f30-929bbf47bc45",
    "email": "ada@example.com",
    "phone": "+77001234567",
    "displayName": "Ada Lovelace",
    "role": "STUDENT",
    "currentBand": null,
    "targetBand": null,
    "examDate": null,
    "examType": null,
    "timezone": "UTC",
    "createdAt": "2026-07-26T10:00:00Z",
    "updatedAt": "2026-07-26T10:00:00Z"
  }
}
```

Регистрация и login также устанавливают refresh token cookie. Cookie недоступна
JavaScript (`HttpOnly`), ограничена `Path=/api/v1/auth`, имеет настраиваемые
`SameSite`/`Secure` и отправляется браузером только с `credentials: include`.

Duplicate normalized email возвращает `409 EMAIL_ALREADY_EXISTS`, duplicate
phone — `409 PHONE_ALREADY_EXISTS`, а неверный proof token —
`422 PHONE_NOT_VERIFIED`.

### `POST /auth/login`

```json
{
  "email": "ada@example.com",
  "password": "correct horse battery staple",
  "remember": false
}
```

Ответ `200` имеет тот же формат, что регистрация. Неверные данные всегда дают
одинаковый `401 INVALID_CREDENTIALS`, чтобы не раскрывать наличие email.

### `POST /auth/google`

```json
{ "googleToken": "<google-id-token>" }
```

Вход по Google ID token (Google Sign-In). Существующий аккаунт получает сессию
в формате auth-ответа. Если аккаунта нет, он создаётся с ролью `ADMIN` только
для супер-админов (`SUPER_ADMIN_EMAILS` или таблица `super_admins`); роль
существующего супер-админа повышается до `ADMIN` (понижения здесь никогда не
происходит). Ответ `200` также устанавливает refresh cookie. Ошибки:
`401 GOOGLE_TOKEN_INVALID` (несуществующий/невалидный токен или неподтверждённый
email), `403 ACCOUNT_NOT_FOUND` (Google-аккаунт не супер-админ и пользователя с
таким email нет).

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

### `POST /admin/reading/materials/{id}/publish`

```json
{ "revision": 2 }
```

Фиксирует текущую версию как опубликованную. Только `ADMIN`; `EDITOR` получает
`403 FORBIDDEN`. Последующие изменения создают новый черновик и выставляют
`hasUnpublishedChanges: true`, не изменяя опубликованную версию.

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

## Health

- `GET /health/live` → `200 {"status":"ok"}`;
- `GET /health/ready` → `200`, только если PostgreSQL и Redis отвечают;
- при отказе зависимости readiness → `503` с безопасными статусами
  `postgres`/`redis`, без внутреннего текста подключения.
