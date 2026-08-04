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

Сначала выполните phone verification с purpose `waitlist`, затем передайте
одноразовый token:

```json
{
  "firstName": "Ada",
  "lastName": "Lovelace",
  "email": "ada@example.com",
  "phone": "+77001234567",
  "source": "landing",
  "verificationToken": "<single-use-token>"
}
```

`firstName`, `lastName`, `email` обязательны (каждое имя — от 2 до 100
символов), `source` необязателен. Ответ `201` содержит созданную заявку:

```json
{
  "id": "2fffd066-e824-4adc-b099-c7c266b7513a",
  "phone": "+77001234567",
  "email": "ada@example.com",
  "firstName": "Ada",
  "lastName": "Lovelace",
  "source": "landing",
  "status": "WAITING",
  "phoneVerifiedAt": "2026-08-02T12:00:30Z",
  "createdAt": "2026-08-02T12:00:35Z"
}
```

Повторный номер даёт `409 WAITLIST_ENTRY_EXISTS`; неверный proof token —
`422 PHONE_NOT_VERIFIED`; отсутствующие или некорректные поля — `422
VALIDATION_ERROR` с деталями по `firstName`, `lastName`, `email`.

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
