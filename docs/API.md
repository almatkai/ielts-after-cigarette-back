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

## Auth

### `POST /auth/register`

```json
{
  "name": "Ada Lovelace",
  "email": "ada@example.com",
  "password": "correct horse battery staple",
  "confirmPassword": "correct horse battery staple",
  "acceptedTerms": true
}
```

`confirmPassword` необязателен для API-клиента, но если передан, обязан
совпадать. `acceptedTerms` обязателен. Ответ `201`:

```json
{
  "accessToken": "<jwt>",
  "tokenType": "Bearer",
  "expiresIn": 900,
  "user": {
    "id": "2c6eea74-968f-4af4-9f30-929bbf47bc45",
    "email": "ada@example.com",
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

Duplicate normalized email возвращает `409 EMAIL_ALREADY_EXISTS`.

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
