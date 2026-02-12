# Order System — Полная спецификация бэкенда для разработки фронтенда

## Цель документа

Этот файл содержит исчерпывающее описание бэкенд-системы управления заказами. По этому описанию необходимо создать полноценный фронтенд (React SPA), который корректно взаимодействует со всеми API-эндпоинтами, обрабатывает все статусы и ошибки, и соответствует бизнес-логике бэкенда.

---

## 1. Архитектура системы (высокоуровневый обзор)

```
┌─────────────────────────────────────────────────────────────┐
│                      FRONTEND (SPA)                         │
│              React + TypeScript приложение                   │
│         Общается ТОЛЬКО с API Gateway по REST               │
└──────────────────────┬──────────────────────────────────────┘
                       │ HTTP REST (JSON)
                       │ Base URL: http://localhost:8080
                       ▼
┌─────────────────────────────────────────────────────────────┐
│                    API GATEWAY (Go/Gin)                      │
│                    Порт: 8080                                │
│  ┌─────────────┐ ┌──────────────┐ ┌───────────────────┐    │
│  │ Auth Routes  │ │ User Routes  │ │  Order Routes     │    │
│  │ (публичные)  │ │ (защищённые) │ │  (защищённые)     │    │
│  └──────┬───────┘ └──────┬───────┘ └───────┬───────────┘    │
│         │                │                  │                │
│  JWT Middleware    Rate Limiting      Ownership Check        │
└─────────┼────────────────┼──────────────────┼───────────────┘
          │ gRPC           │ gRPC             │ gRPC
          ▼                ▼                  ▼
┌──────────────┐  ┌──────────────┐  ┌──────────────────┐
│ User Service │  │ User Service │  │  Order Service   │
│ :50051       │  │ :50051       │  │  :50052          │
│              │  │              │  │                  │
│ MySQL + Redis│  │ MySQL + Redis│  │ MySQL + Kafka    │
└──────────────┘  └──────────────┘  └────────┬─────────┘
                                             │ Kafka (Saga)
                                             ▼
                                   ┌──────────────────┐
                                   │ Payment Service  │
                                   │ :50053           │
                                   │                  │
                                   │ MySQL + Redis    │
                                   └──────────────────┘
```

**Важно для фронтенда:**
- Фронтенд общается **ТОЛЬКО** с API Gateway по адресу `http://localhost:8080`
- Все запросы — REST/JSON
- Аутентификация — JWT Bearer Token в заголовке `Authorization`
- WebSocket / SSE **НЕТ** — обновления получаем через polling

---

## 2. Аутентификация и авторизация

### 2.1 Механизм

- **JWT RS256** (асимметричная криптография)
- Access Token — живёт **15 минут**
- Refresh Token — живёт **7 дней** (168 часов)
- Tokens можно отозвать через logout (blacklist на Redis)

### 2.2 Как работает авторизация

1. Пользователь регистрируется (`POST /api/v1/auth/register`)
2. Пользователь логинится (`POST /api/v1/auth/login`) → получает `access_token` и `refresh_token`
3. Все защищённые запросы отправляются с заголовком: `Authorization: Bearer <access_token>`
4. Gateway валидирует токен через User Service (проверяет подпись + blacklist)
5. Если токен невалиден → `401 Unauthorized`
6. При выходе (`POST /api/v1/auth/logout`) токен добавляется в blacklist

### 2.3 Хранение токенов на фронтенде

- `access_token` — хранить в памяти (state/context) или в httpOnly cookie
- `refresh_token` — хранить надёжно (httpOnly cookie или localStorage)
- При получении 401 — перенаправить на страницу логина
- **ПРИМЕЧАНИЕ:** Бэкенд пока НЕ реализует endpoint для refresh token. При истечении access_token нужно повторный логин. (Можно реализовать на фронте перелогин или показывать уведомление)

### 2.4 Rate Limiting

- API Gateway применяет rate limiting по IP-адресу
- Лимит: 100 запросов в минуту (настраивается)
- При превышении: HTTP `429 Too Many Requests` с заголовком `Retry-After`

---

## 3. Все REST API эндпоинты

### Базовый URL: `http://localhost:8080`

---

### 3.1 Health Check (без авторизации)

#### `GET /health`
Проверка работоспособности (legacy).

**Ответ (200):**
```json
{
  "status": "ok",
  "service": "api-gateway"
}
```

#### `GET /healthz`
Liveness probe (для Kubernetes).

**Ответ (200):**
```json
{
  "status": "alive"
}
```

#### `GET /readyz`
Readiness probe (зависимости доступны).

**Ответ (200):**
```json
{
  "status": "ready"
}
```

**Ответ (503) — если зависимости недоступны:**
```json
{
  "status": "not_ready"
}
```

---

### 3.2 Аутентификация — `/api/v1/auth`

Эти эндпоинты **НЕ требуют** JWT токен (публичные), но на них действует rate limiting.

---

#### `POST /api/v1/auth/register`

Регистрация нового пользователя.

**Request Body:**
```json
{
  "email": "user@example.com",
  "password": "securepass123",
  "name": "Иван Иванов"
}
```

**Валидация полей:**
| Поле | Тип | Обязательное | Правила |
|------|-----|:---:|---------|
| `email` | string | да | Формат email (RFC 5322) |
| `password` | string | да | Минимум 8 символов |
| `name` | string | да | Минимум 2 символа |

**Успешный ответ (201 Created):**
```json
{
  "user_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

**Возможные ошибки:**

| HTTP код | error | message | Когда |
|----------|-------|---------|-------|
| 400 | `invalid_request` | Невалидные данные запроса | Невалидный JSON / не прошла валидация |
| 400 | `invalid_argument` | Пароль должен содержать минимум 8 символов | Слабый пароль |
| 400 | `invalid_argument` | Невалидный формат email | Неправильный email |
| 409 | `already_exists` | Пользователь с таким email уже существует | Email уже занят |
| 503 | `service_unavailable` | User Service недоступен | Бэкенд сервис упал |

---

#### `POST /api/v1/auth/login`

Аутентификация пользователя.

**Request Body:**
```json
{
  "email": "user@example.com",
  "password": "securepass123"
}
```

**Валидация полей:**
| Поле | Тип | Обязательное | Правила |
|------|-----|:---:|---------|
| `email` | string | да | Формат email |
| `password` | string | да | Не пустой |

**Успешный ответ (200 OK):**
```json
{
  "access_token": "eyJhbGciOiJSUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJSUzI1NiIs...",
  "expires_at": 1707234567
}
```

| Поле | Тип | Описание |
|------|-----|----------|
| `access_token` | string | JWT для авторизации запросов. Время жизни: 15 мин |
| `refresh_token` | string | JWT для обновления. Время жизни: 7 дней |
| `expires_at` | int64 | Unix timestamp истечения access_token |

**Возможные ошибки:**

| HTTP код | error | message | Когда |
|----------|-------|---------|-------|
| 400 | `invalid_request` | Невалидные данные запроса | Невалидный JSON |
| 401 | `unauthenticated` | Неверный email или пароль | Неправильные credentials |
| 503 | `service_unavailable` | User Service недоступен | Сервис недоступен |

---

#### `POST /api/v1/auth/logout`

Выход из системы (инвалидация токена).

**Headers:**
```
Authorization: Bearer <access_token>
```

**Request Body:** нет (пустой)

**Успешный ответ (200 OK):**
```json
{
  "success": true
}
```

**Возможные ошибки:**

| HTTP код | error | message | Когда |
|----------|-------|---------|-------|
| 401 | `unauthorized` | Отсутствует токен авторизации | Нет заголовка Authorization |

**Примечание:** Logout НЕ проходит через JWT middleware — токен извлекается напрямую из заголовка. Даже если токен уже истёк, logout пройдёт (добавит в blacklist).

---

### 3.3 Пользователи — `/api/v1/users`

Все эндпоинты **ТРЕБУЮТ** авторизацию (JWT Bearer Token).

---

#### `GET /api/v1/users/me`

Получить информацию о текущем авторизованном пользователе.

**Headers:**
```
Authorization: Bearer <access_token>
```

**Успешный ответ (200 OK):**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "email": "user@example.com",
  "name": "Иван Иванов",
  "created_at": 1707234567,
  "updated_at": 1707234567
}
```

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | string (UUID) | Уникальный идентификатор пользователя |
| `email` | string | Email пользователя |
| `name` | string | Имя пользователя |
| `created_at` | int64 | Unix timestamp создания аккаунта |
| `updated_at` | int64 | Unix timestamp последнего обновления |

**Возможные ошибки:**

| HTTP код | error | message |
|----------|-------|---------|
| 401 | `unauthorized` | Требуется авторизация / Невалидный токен |
| 404 | `not_found` | Пользователь не найден |

---

#### `GET /api/v1/users/:id`

Получить информацию о пользователе по ID.

**Headers:**
```
Authorization: Bearer <access_token>
```

**URL параметры:**
- `:id` — UUID пользователя

**Успешный ответ (200 OK):**
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "email": "user@example.com",
  "name": "Иван Иванов",
  "created_at": 1707234567,
  "updated_at": 1707234567
}
```

**Возможные ошибки:**

| HTTP код | error | message |
|----------|-------|---------|
| 400 | `invalid_request` | ID пользователя обязателен |
| 401 | `unauthorized` | Требуется авторизация |
| 404 | `not_found` | Пользователь не найден |

---

### 3.4 Заказы — `/api/v1/orders`

Все эндпоинты **ТРЕБУЮТ** авторизацию (JWT Bearer Token).
Пользователь видит **ТОЛЬКО СВОИ** заказы. Доступ к чужим заказам возвращает `403 Forbidden`.

---

#### `POST /api/v1/orders`

Создание нового заказа. Запускает процесс оплаты через Saga-паттерн.

**Headers:**
```
Authorization: Bearer <access_token>
```

**Request Body:**
```json
{
  "items": [
    {
      "product_id": "550e8400-e29b-41d4-a716-446655440000",
      "product_name": "Ноутбук ASUS",
      "quantity": 1,
      "unit_price": {
        "amount": 7999900,
        "currency": "RUB"
      }
    },
    {
      "product_id": "550e8400-e29b-41d4-a716-446655440001",
      "product_name": "Мышь Logitech",
      "quantity": 2,
      "unit_price": {
        "amount": 299900,
        "currency": "RUB"
      }
    }
  ],
  "idempotency_key": "unique-order-key-12345"
}
```

**Валидация полей:**

| Поле | Тип | Обязательное | Правила |
|------|-----|:---:|---------|
| `items` | array | да | Минимум 1 элемент |
| `items[].product_id` | string | да | Формат UUID |
| `items[].product_name` | string | да | Минимум 1 символ |
| `items[].quantity` | int32 | да | Минимум 1 |
| `items[].unit_price.amount` | int64 | да | Минимум 1 (в копейках/центах) |
| `items[].unit_price.currency` | string | да | Ровно 3 символа (ISO 4217: RUB, USD, EUR) |
| `idempotency_key` | string | да | Уникальный ключ для предотвращения дубликатов |

**ВАЖНО о деньгах:**
- `amount` указывается в **минимальных единицах валюты** (копейках для RUB, центах для USD)
- 79999.00 RUB = `amount: 7999900`
- 2999.00 RUB = `amount: 299900`
- На фронтенде нужно конвертировать: `отображаемая_цена = amount / 100`

**ВАЖНО об idempotency_key:**
- Генерируется на фронтенде (UUID v4 подойдёт)
- Гарантирует, что при повторной отправке того же запроса не создастся дубликат заказа
- Если заказ с таким ключом уже существует, вернётся `409 Conflict`

**Успешный ответ (201 Created):**
```json
{
  "order_id": "660e8400-e29b-41d4-a716-446655440000",
  "status": "PENDING"
}
```

**Что происходит после создания:**
1. Заказ создаётся со статусом `PENDING`
2. Автоматически запускается Saga-оркестрация
3. Order Service отправляет команду `PROCESS_PAYMENT` в Payment Service через Kafka
4. Payment Service обрабатывает платёж
5. Заказ переходит в `CONFIRMED` (успех) или `FAILED` (ошибка)
6. Фронтенд должен **поллить** статус заказа, чтобы узнать результат

**Возможные ошибки:**

| HTTP код | error | message | Когда |
|----------|-------|---------|-------|
| 400 | `invalid_request` | Невалидные данные запроса | Валидация не прошла |
| 400 | `invalid_argument` | ... | Бизнес-валидация (пустые items и т.д.) |
| 401 | `unauthorized` | Требуется авторизация | Нет/невалидный токен |
| 409 | `already_exists` | Заказ с таким idempotency_key уже существует | Дубликат запроса |
| 503 | `service_unavailable` | Order Service недоступен | Сервис упал |

---

#### `GET /api/v1/orders`

Список заказов текущего пользователя с пагинацией и фильтрацией.

**Headers:**
```
Authorization: Bearer <access_token>
```

**Query параметры:**

| Параметр | Тип | По умолчанию | Описание |
|----------|-----|:---:|----------|
| `page` | int | 1 | Номер страницы (начиная с 1) |
| `page_size` | int | 20 | Кол-во элементов на странице (макс. 100) |
| `status` | string | — | Фильтр по статусу: `PENDING`, `CONFIRMED`, `CANCELLED`, `FAILED` |

**Примеры запросов:**
```
GET /api/v1/orders
GET /api/v1/orders?page=2&page_size=10
GET /api/v1/orders?status=PENDING
GET /api/v1/orders?page=1&page_size=50&status=CONFIRMED
```

**Успешный ответ (200 OK):**
```json
{
  "orders": [
    {
      "id": "660e8400-e29b-41d4-a716-446655440000",
      "user_id": "550e8400-e29b-41d4-a716-446655440000",
      "items": [
        {
          "product_id": "550e8400-e29b-41d4-a716-446655440000",
          "product_name": "Ноутбук ASUS",
          "quantity": 1,
          "unit_price": {
            "amount": 7999900,
            "currency": "RUB"
          }
        }
      ],
      "total_amount": {
        "amount": 7999900,
        "currency": "RUB"
      },
      "status": "CONFIRMED",
      "payment_id": "770e8400-e29b-41d4-a716-446655440000",
      "created_at": 1707234567,
      "updated_at": 1707234590
    },
    {
      "id": "660e8400-e29b-41d4-a716-446655440001",
      "user_id": "550e8400-e29b-41d4-a716-446655440000",
      "items": [
        {
          "product_id": "550e8400-e29b-41d4-a716-446655440001",
          "product_name": "Мышь Logitech",
          "quantity": 2,
          "unit_price": {
            "amount": 299900,
            "currency": "RUB"
          }
        }
      ],
      "total_amount": {
        "amount": 599800,
        "currency": "RUB"
      },
      "status": "PENDING",
      "created_at": 1707234600,
      "updated_at": 1707234600
    }
  ],
  "pagination": {
    "current_page": 1,
    "page_size": 20,
    "total_items": 2,
    "total_pages": 1
  }
}
```

**Структура объекта заказа:**

| Поле | Тип | Описание |
|------|-----|----------|
| `id` | string (UUID) | ID заказа |
| `user_id` | string (UUID) | ID владельца заказа |
| `items` | array | Позиции заказа |
| `items[].product_id` | string (UUID) | ID продукта |
| `items[].product_name` | string | Название продукта |
| `items[].quantity` | int32 | Количество |
| `items[].unit_price` | Money | Цена за единицу |
| `total_amount` | Money | Общая сумма заказа |
| `total_amount.amount` | int64 | Сумма в копейках |
| `total_amount.currency` | string | Валюта (ISO 4217) |
| `status` | string | Статус заказа (см. раздел 4) |
| `payment_id` | string или null | ID платежа (появляется после успешной оплаты) |
| `failure_reason` | string или null | Причина ошибки (только для статуса FAILED) |
| `created_at` | int64 | Unix timestamp создания |
| `updated_at` | int64 | Unix timestamp обновления |

**Структура пагинации:**

| Поле | Тип | Описание |
|------|-----|----------|
| `current_page` | int | Текущая страница |
| `page_size` | int | Размер страницы |
| `total_items` | int64 | Всего заказов |
| `total_pages` | int | Всего страниц |

**Возможные ошибки:**

| HTTP код | error | message |
|----------|-------|---------|
| 400 | `invalid_request` | Невалидный статус: допустимые значения PENDING, CONFIRMED, CANCELLED, FAILED |
| 401 | `unauthorized` | Требуется авторизация |

---

#### `GET /api/v1/orders/:id`

Получить конкретный заказ по ID.

**Headers:**
```
Authorization: Bearer <access_token>
```

**URL параметры:**
- `:id` — UUID заказа

**Успешный ответ (200 OK):**
```json
{
  "order": {
    "id": "660e8400-e29b-41d4-a716-446655440000",
    "user_id": "550e8400-e29b-41d4-a716-446655440000",
    "items": [
      {
        "product_id": "550e8400-e29b-41d4-a716-446655440000",
        "product_name": "Ноутбук ASUS",
        "quantity": 1,
        "unit_price": {
          "amount": 7999900,
          "currency": "RUB"
        }
      }
    ],
    "total_amount": {
      "amount": 7999900,
      "currency": "RUB"
    },
    "status": "CONFIRMED",
    "payment_id": "770e8400-e29b-41d4-a716-446655440000",
    "created_at": 1707234567,
    "updated_at": 1707234590
  }
}
```

**Примечание:** Ответ обёрнут в объект `{ "order": {...} }` — в отличие от списка, где заказы в массиве.

**Возможные ошибки:**

| HTTP код | error | message | Когда |
|----------|-------|---------|-------|
| 400 | `invalid_request` | ID заказа обязателен | Пустой ID |
| 401 | `unauthorized` | Требуется авторизация | Нет токена |
| 403 | `forbidden` | Доступ к заказу запрещён | Заказ принадлежит другому пользователю |
| 404 | `not_found` | Заказ не найден | Заказ не существует |

---

#### `DELETE /api/v1/orders/:id`

Отмена заказа. Можно отменить ТОЛЬКО заказы в статусе `PENDING`.

**Headers:**
```
Authorization: Bearer <access_token>
```

**URL параметры:**
- `:id` — UUID заказа

**Request Body:** нет (DELETE без тела)

**Успешный ответ (200 OK):**
```json
{
  "success": true,
  "message": "Заказ отменён"
}
```

**Возможные ошибки:**

| HTTP код | error | message | Когда |
|----------|-------|---------|-------|
| 400 | `invalid_request` | ID заказа обязателен | Пустой ID |
| 401 | `unauthorized` | Требуется авторизация | Нет токена |
| 403 | `forbidden` | Доступ к заказу запрещён | Чужой заказ |
| 404 | `not_found` | Заказ не найден | Не существует |
| 409 | `failed_precondition` | Заказ не может быть отменён | Статус не PENDING |

---

## 4. Статусы заказов (State Machine)

### 4.1 Диаграмма переходов

```
                    ┌───────────┐
                    │  PENDING  │ ← начальный статус при создании
                    └─────┬─────┘
                          │
              ┌───────────┼───────────┐
              │           │           │
              ▼           ▼           ▼
       ┌───────────┐ ┌──────────┐ ┌────────┐
       │ CONFIRMED │ │ CANCELLED│ │ FAILED │
       │ (оплачен) │ │(отменён) │ │(ошибка)│
       └───────────┘ └──────────┘ └────────┘
       (финальный)    (финальный)  (финальный)
```

### 4.2 Описание статусов

| Статус | Описание | Как попадает | Действия пользователя |
|--------|----------|-------------|----------------------|
| `PENDING` | Заказ создан, ожидает обработки платежа | Сразу после `POST /orders` | Может отменить (DELETE) |
| `CONFIRMED` | Платёж успешен, заказ подтверждён | Автоматически после оплаты Saga | Только просмотр |
| `CANCELLED` | Заказ отменён пользователем | После `DELETE /orders/:id` | Только просмотр |
| `FAILED` | Ошибка при обработке платежа | Автоматически при ошибке Saga | Только просмотр, видна причина ошибки |

### 4.3 Поведение на фронтенде по статусам

| Статус | Цвет / Badge | Показывать кнопку "Отменить" | Дополнительно |
|--------|:---:|:---:|---------|
| `PENDING` | Жёлтый / Warning | ДА | Показывать индикатор загрузки "Обработка платежа..." |
| `CONFIRMED` | Зелёный / Success | НЕТ | Показывать `payment_id` |
| `CANCELLED` | Серый / Default | НЕТ | — |
| `FAILED` | Красный / Error | НЕТ | Показывать `failure_reason` |

### 4.4 Polling стратегия для PENDING заказов

Так как нет WebSocket, фронтенд должен поллить статус:

```
Создание заказа → Получаем order_id + статус PENDING
→ Начинаем polling: GET /api/v1/orders/:id каждые 2 секунды
→ Если статус изменился (не PENDING) → прекращаем polling
→ Максимум 30 попыток (60 секунд), после чего показываем "Обработка занимает больше времени..."
```

Рекомендуемые параметры:
- Интервал: 2 секунды
- Максимум попыток: 30 (60 секунд)
- Backoff: нет (фиксированный интервал)

---

## 5. Формат ошибок

### 5.1 Стандартный формат ошибки

Все ошибки API возвращаются в едином формате:

```json
{
  "error": "error_code",
  "message": "Описание ошибки для пользователя"
}
```

| Поле | Тип | Описание |
|------|-----|----------|
| `error` | string | Машиночитаемый код ошибки (для логики на фронтенде) |
| `message` | string | Человекочитаемое описание (можно показать пользователю) |

### 5.2 Коды ошибок

| `error` | HTTP код | Описание | Действие на фронтенде |
|---------|----------|----------|----------------------|
| `invalid_request` | 400 | Невалидный JSON или валидация | Показать ошибки валидации |
| `invalid_argument` | 400 | Бизнес-валидация (слабый пароль и т.д.) | Показать message |
| `unauthorized` | 401 | Нет токена | Редирект на логин |
| `unauthenticated` | 401 | Неверные credentials | Показать ошибку |
| `forbidden` | 403 | Нет доступа | Показать "Доступ запрещён" |
| `not_found` | 404 | Ресурс не найден | Показать 404 страницу |
| `already_exists` | 409 | Ресурс уже существует | Показать message |
| `failed_precondition` | 409 | Невозможная операция (отмена оплаченного) | Показать message |
| `permission_denied` | 403 | Нет прав | Показать message |
| `service_unavailable` | 503 | Сервис недоступен | Показать "Сервис временно недоступен" |
| `internal_error` | 500 | Внутренняя ошибка | Показать "Произошла ошибка, попробуйте позже" |

### 5.3 Ошибка валидации (400)

При невалидном JSON или нарушении правил валидации:
```json
{
  "error": "invalid_request",
  "message": "Невалидные данные запроса"
}
```

**Примечание:** Бэкенд НЕ возвращает детальные ошибки по полям. Валидацию полей нужно делать на фронтенде.

---

## 6. Модель данных (TypeScript типы для фронтенда)

```typescript
// ==================== AUTH ====================

interface RegisterRequest {
  email: string;      // формат email, обязательное
  password: string;   // минимум 8 символов, обязательное
  name: string;       // минимум 2 символа, обязательное
}

interface RegisterResponse {
  user_id: string;    // UUID
}

interface LoginRequest {
  email: string;      // формат email, обязательное
  password: string;   // не пустой, обязательное
}

interface LoginResponse {
  access_token: string;   // JWT token
  refresh_token: string;  // JWT refresh token
  expires_at: number;     // Unix timestamp (секунды)
}

interface LogoutResponse {
  success: boolean;
}

// ==================== USER ====================

interface User {
  id: string;          // UUID
  email: string;
  name: string;
  created_at: number;  // Unix timestamp (секунды)
  updated_at: number;  // Unix timestamp (секунды)
}

// ==================== MONEY ====================

interface Money {
  amount: number;      // В минимальных единицах (копейки/центы). int64
  currency: string;    // ISO 4217: "RUB", "USD", "EUR"
}

// ==================== ORDER ====================

type OrderStatus = "PENDING" | "CONFIRMED" | "CANCELLED" | "FAILED";

interface OrderItem {
  product_id: string;     // UUID
  product_name: string;
  quantity: number;        // int32, >= 1
  unit_price: Money;
}

interface Order {
  id: string;              // UUID
  user_id: string;         // UUID
  items: OrderItem[];
  total_amount: Money;
  status: OrderStatus;
  payment_id?: string;     // UUID, присутствует только при CONFIRMED
  failure_reason?: string; // присутствует только при FAILED
  created_at: number;      // Unix timestamp
  updated_at: number;      // Unix timestamp
}

// ==================== REQUEST DTOs ====================

interface CreateOrderItemRequest {
  product_id: string;    // UUID формат
  product_name: string;  // минимум 1 символ
  quantity: number;      // >= 1
  unit_price: Money;     // amount >= 1, currency ровно 3 символа
}

interface CreateOrderRequest {
  items: CreateOrderItemRequest[];  // минимум 1 элемент
  idempotency_key: string;          // уникальный ключ (UUID v4)
}

interface CreateOrderResponse {
  order_id: string;   // UUID
  status: string;     // "PENDING"
}

// ==================== LIST ORDERS ====================

interface PaginationResponse {
  current_page: number;
  page_size: number;
  total_items: number;   // int64
  total_pages: number;
}

interface ListOrdersResponse {
  orders: Order[];
  pagination: PaginationResponse;
}

// ==================== GET ORDER ====================

interface GetOrderResponse {
  order: Order;
}

// ==================== CANCEL ORDER ====================

interface CancelOrderResponse {
  success: boolean;
  message: string;
}

// ==================== ERROR ====================

interface ApiError {
  error: string;    // машиночитаемый код
  message: string;  // человекочитаемое описание
}
```

---

## 7. Бизнес-правила

### 7.1 Регистрация
- Email уникален в системе
- Пароль минимум 8 символов (хранится bcrypt hash, cost=12)
- Имя минимум 2 символа
- После регистрации нужно залогиниться отдельно

### 7.2 Заказы
- Пользователь видит **только свои** заказы
- Попытка доступа к чужому заказу → `403 Forbidden`
- Отменить можно ТОЛЬКО заказ в статусе `PENDING`
- `idempotency_key` предотвращает создание дубликатов (при повторной отправке формы)
- Деньги хранятся в копейках (`amount: 7999900` = `79999.00 RUB`)
- Валюта — 3-символьный ISO 4217 код (RUB, USD, EUR)
- Заказ должен содержать минимум 1 позицию
- Каждая позиция: quantity >= 1, amount >= 1

### 7.3 Оплата (Saga)
- Оплата происходит автоматически после создания заказа
- Процесс асинхронный (через Kafka)
- Типичное время обработки: 1-5 секунд
- При ошибке оплаты запускается компенсация (refund)
- Заказ с `FAILED` содержит `failure_reason` с описанием причины

### 7.4 Тестовое поведение оплаты
- **Оплата ВСЕГДА успешна**, кроме случая когда `total_amount % 666 == 0`
- Это сделано для тестирования failure flow
- Т.е. если сумма заказа делится на 666 (напр. 66600 копеек = 666.00 RUB) — оплата упадёт

---

## 8. Рекомендации для фронтенда

### 8.1 Структура страниц

| Страница | URL | Описание | Авторизация |
|----------|-----|----------|:-----------:|
| Логин | `/login` | Форма входа | Нет |
| Регистрация | `/register` | Форма регистрации | Нет |
| Дашборд / Список заказов | `/orders` | Главная страница с заказами | Да |
| Детали заказа | `/orders/:id` | Подробности конкретного заказа | Да |
| Создание заказа | `/orders/new` | Форма создания заказа | Да |
| Профиль | `/profile` | Информация о пользователе | Да |

### 8.2 Навигация

```
┌──────────────────────────────────────────────────────────────┐
│  Logo / Название      [Заказы]  [Новый заказ]  [Профиль]    │
│                                                   [Выйти]   │
└──────────────────────────────────────────────────────────────┘
```

### 8.3 Страница логина

- Поля: email, пароль
- Кнопка "Войти"
- Ссылка "Нет аккаунта? Зарегистрироваться"
- При ошибке 401: "Неверный email или пароль"
- После успешного логина: сохранить токены, перенаправить на `/orders`

### 8.4 Страница регистрации

- Поля: имя, email, пароль, подтверждение пароля
- Валидация на фронте:
  - Имя: минимум 2 символа
  - Email: формат email
  - Пароль: минимум 8 символов
  - Подтверждение пароля: совпадает с паролем
- При ошибке 409: "Пользователь с таким email уже существует"
- После успешной регистрации: перенаправить на `/login` с сообщением "Регистрация успешна"

### 8.5 Список заказов

- Таблица / Карточки с заказами
- Фильтр по статусу (выпадающий список / табы: Все, Ожидают, Подтверждены, Отменены, Ошибки)
- Пагинация (кнопки страниц или infinite scroll)
- Для каждого заказа показывать:
  - Номер (ID или короткий ID)
  - Статус (цветной badge)
  - Сумма (форматированная: `79 999,00 ₽`)
  - Дата создания
  - Кнопка "Подробнее"
  - Кнопка "Отменить" (только для PENDING)
- Пустое состояние: "У вас пока нет заказов"

### 8.6 Детали заказа

- Полная информация о заказе
- Список позиций (таблица):
  - Название товара
  - Количество
  - Цена за единицу
  - Сумма (quantity × unit_price)
- Итого (total_amount)
- Статус с описанием
- Для PENDING: индикатор "Обрабатывается..." + автоматический polling
- Для FAILED: показать `failure_reason` красным
- Для CONFIRMED: показать `payment_id`
- Кнопка "Отменить" (только для PENDING)

### 8.7 Форма создания заказа

- Динамическая форма добавления позиций
- Для каждой позиции:
  - Product ID (текстовое поле или генерация UUID)
  - Название товара (текст)
  - Количество (число, >= 1)
  - Цена за единицу (число, вводится в рублях, конвертируется в копейки)
  - Валюта (выпадающий список: RUB, USD, EUR)
- Кнопка "Добавить позицию"
- Кнопка "Удалить позицию" (для каждой)
- Автоматический расчёт итого
- Кнопка "Создать заказ"
- `idempotency_key` генерируется автоматически (UUID v4)
- После создания: перенаправить на `/orders/:id` (страницу деталей) с polling

### 8.8 Профиль пользователя

- Отображение: имя, email, дата регистрации
- Кнопка "Выйти"

### 8.9 Форматирование денег

```typescript
function formatMoney(money: Money): string {
  const value = money.amount / 100;

  const currencyMap: Record<string, string> = {
    'RUB': '₽',
    'USD': '$',
    'EUR': '€',
  };

  const symbol = currencyMap[money.currency] || money.currency;

  return new Intl.NumberFormat('ru-RU', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(value) + ' ' + symbol;
}

// Примеры:
// { amount: 7999900, currency: "RUB" } → "79 999,00 ₽"
// { amount: 299900, currency: "RUB" }  → "2 999,00 ₽"
// { amount: 100, currency: "USD" }     → "1,00 $"
```

### 8.10 Форматирование дат

```typescript
function formatDate(unixTimestamp: number): string {
  return new Date(unixTimestamp * 1000).toLocaleString('ru-RU', {
    day: '2-digit',
    month: '2-digit',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

// Пример: 1707234567 → "06.02.2024, 15:29"
```

---

## 9. Пример полного flow (для тестирования)

### 9.1 Регистрация и вход

```
1. POST /api/v1/auth/register
   Body: { "email": "test@test.com", "password": "password123", "name": "Test User" }
   → 201: { "user_id": "..." }

2. POST /api/v1/auth/login
   Body: { "email": "test@test.com", "password": "password123" }
   → 200: { "access_token": "...", "refresh_token": "...", "expires_at": ... }

3. Сохраняем access_token
```

### 9.2 Создание и отслеживание заказа

```
4. POST /api/v1/orders
   Headers: Authorization: Bearer <access_token>
   Body: {
     "items": [{
       "product_id": "11111111-1111-1111-1111-111111111111",
       "product_name": "Тестовый товар",
       "quantity": 1,
       "unit_price": { "amount": 10000, "currency": "RUB" }
     }],
     "idempotency_key": "test-key-001"
   }
   → 201: { "order_id": "...", "status": "PENDING" }

5. GET /api/v1/orders/<order_id>  (polling каждые 2 секунды)
   → 200: { "order": { ..., "status": "PENDING" } }     // ещё обрабатывается
   → 200: { "order": { ..., "status": "CONFIRMED" } }   // оплата прошла!
```

### 9.3 Получение списка и отмена

```
6. GET /api/v1/orders?page=1&page_size=10
   → 200: { "orders": [...], "pagination": {...} }

7. DELETE /api/v1/orders/<order_id>  (только если PENDING)
   → 200: { "success": true, "message": "Заказ отменён" }
```

### 9.4 Профиль и выход

```
8. GET /api/v1/users/me
   → 200: { "id": "...", "email": "test@test.com", "name": "Test User", ... }

9. POST /api/v1/auth/logout
   Headers: Authorization: Bearer <access_token>
   → 200: { "success": true }
```

---

## 10. CORS и настройка запросов

### 10.1 Headers для каждого запроса

```
Content-Type: application/json
Authorization: Bearer <access_token>   // для защищённых эндпоинтов
```

### 10.2 Обработка 401

При получении ответа с кодом `401` на любом защищённом эндпоинте:
1. Очистить сохранённые токены
2. Перенаправить на `/login`
3. Показать сообщение "Сессия истекла, войдите снова"

### 10.3 Обработка 429 (Rate Limit)

При получении `429 Too Many Requests`:
1. Прочитать заголовок `Retry-After` (секунды)
2. Показать сообщение "Слишком много запросов, подождите X секунд"
3. Заблокировать UI на указанное время

### 10.4 Обработка сетевых ошибок

При ошибке сети (нет подключения, timeout):
1. Показать "Нет подключения к серверу"
2. Не перенаправлять на логин (это не 401)
3. Предложить "Попробовать снова"

---

## 11. Сводная таблица всех эндпоинтов

| Метод | URL | Auth | Описание |
|-------|-----|:----:|----------|
| GET | `/health` | - | Health check (legacy) |
| GET | `/healthz` | - | Liveness probe |
| GET | `/readyz` | - | Readiness probe |
| POST | `/api/v1/auth/register` | - | Регистрация |
| POST | `/api/v1/auth/login` | - | Вход |
| POST | `/api/v1/auth/logout` | Bearer* | Выход |
| GET | `/api/v1/users/me` | Bearer | Текущий пользователь |
| GET | `/api/v1/users/:id` | Bearer | Пользователь по ID |
| POST | `/api/v1/orders` | Bearer | Создание заказа |
| GET | `/api/v1/orders` | Bearer | Список заказов (с пагинацией + фильтром) |
| GET | `/api/v1/orders/:id` | Bearer | Заказ по ID |
| DELETE | `/api/v1/orders/:id` | Bearer | Отмена заказа |

*Logout не проходит через JWT middleware, но требует токен в Authorization header.

---

## 12. Технический стек бэкенда (для справки)

- **Язык**: Go 1.24
- **HTTP Framework**: Gin
- **Межсервисная связь**: gRPC + Protobuf
- **База данных**: MySQL 8 (GORM ORM)
- **Кеш**: Redis
- **Очереди**: Apache Kafka (Saga-паттерн)
- **Аутентификация**: JWT RS256
- **Наблюдаемость**: Prometheus + Grafana + Jaeger (OpenTelemetry)
- **Деплой**: Docker + Kubernetes (k3s)

---

## Конец спецификации

Этого документа должно быть достаточно для создания полноценного фронтенд-приложения, которое корректно взаимодействует с данным бэкендом. Все endpoint'ы, форматы запросов/ответов, коды ошибок, бизнес-правила и рекомендации по UX описаны выше.
