# Runtime environment

Какие переменные нужны для запуска и где они живут. Реальные значения
**не в git** — только шаблоны `.env.example`.

## Frontend (Vite)

Бандл собирается из `.env.production.yandex` через
`npx vite build --mode production.yandex`.

| Переменная | Значение (prod) | Где |
|---|---|---|
| `VITE_API_URL` | `https://tender.su10.ru` | inline в bundle |
| `VITE_API_MODE` | `go` | inline (Vite constant-fold) |
| `VITE_API_REALTIME_ENABLED` | `true` | inline |
| `VITE_API_<DOMAIN>_ENABLED` × 18 | все `true` | inline |
| `VITE_SENTRY_DSN` / `VITE_SENTRY_ENVIRONMENT` / `VITE_SENTRY_RELEASE` | Sentry frontend error tracking | inline |

**Никаких VITE_*-секретов.** Vite встраивает `VITE_*` в клиентский бандл —
сервисные ключи там нельзя.

Supabase runtime удалён целиком — нет ни `supabase.auth.*`, ни
`supabase.from()`, ни `@supabase/supabase-js` в bundle. Соответствующие
env-переменные (`VITE_AUTH_MODE`, `VITE_SUPABASE_URL`,
`VITE_SUPABASE_PUBLISHABLE_KEY`) удалены.

Шаблон: `.env.production.yandex.example` или `.env.example`.

## Backend (Go BFF)

Production env-файл живёт на prod-сервере: `/srv/sites/tender.su10.ru/server/.env.prod`
(`chmod 600`, **вне git**). Local dev — `.env` в корне репо (тоже не в git).

| Переменная | Назначение | Production value (masked) |
|---|---|---|
| `DATABASE_URL` | DSN Yandex Managed PG | `postgres://…@<cluster>.mdb.yandexcloud.net:6432/HubTender?sslmode=verify-full&sslrootcert=/certs/yandex-ca.pem` |
| `APP_JWT_ISSUER` | `iss`-claim, ставится на access tokens; равен публичному origin | `https://tender.su10.ru` |
| `APP_JWT_AUDIENCE` | optional `aud`-claim | `hubtender-web` |
| `APP_JWT_KEY_ID` | `kid` в JWKS | opaque string (rotate together with key) |
| `APP_JWT_PRIVATE_KEY_PATH` или `_B64` | RSA private key для подписи RS256 JWT | mounted PEM file / base64 |
| `APP_ACCESS_TOKEN_TTL_MINUTES` | TTL access-token | `15` |
| `APP_REFRESH_TOKEN_TTL_DAYS` | TTL refresh-token | `30` |
| `APP_ENV` | `production` / `staging` / `development` | `production` |
| `APP_BASE_URL` | публичный origin для password-reset ссылок | `https://tender.su10.ru` |
| `SMTP_HOST` / `SMTP_PORT` / `SMTP_USER` / `SMTP_PASSWORD` / `SMTP_FROM` | password-recovery mailer; пустой `SMTP_HOST` → NoopMailer | currently unset → /forgot-password возвращает 503 `email_provider_not_configured` в проде |
| `CORS_ORIGINS` | Allowed origins (comma-separated) | `https://tender.su10.ru` |
| `PORT` | Bind port (внутри контейнера) | `3005` (наружу через nginx → `127.0.0.1:3006`) |
| `BIND_HOST` | Bind address | `0.0.0.0` в контейнере, `127.0.0.1` извне |
| `LOG_LEVEL` | trace/debug/info/warn/error | `info` |
| `DB_MAX_CONNS` | pgxpool MaxConns | `20` |
| `DB_MIN_CONNS` | pgxpool MinConns | `2` |
| `DB_MAX_CONN_IDLE_TIME` | Go duration string | `5m` |
| `JWT_CLOCK_SKEW_SECONDS` | leeway для exp/iat | unset (`0`, strict). Включать только для dev/Windows |
| `SENTRY_DSN` / `SENTRY_ENVIRONMENT` / `SENTRY_RELEASE` | error tracking; пустой DSN → no-op | set in prod |
| `TELEGRAM_BOT_TOKEN` | **секрет**. Токен бота рассылки замечаний проверки (@BotFather). Пусто → бот не стартует, привязка и рассылка отвечают «не настроен» | unset |
| `TELEGRAM_BOT_USERNAME` | имя бота без `@` — для ссылки привязки `t.me/<бот>?start=…` | unset |
| `TELEGRAM_PROXY_URL` | необязательный HTTP(S)-прокси до `api.telegram.org`, если прод-контур его не видит напрямую | unset |
| `TELEGRAM_API_BASE_URL` | необязательный адрес Bot API (свой relay); по умолчанию `https://api.telegram.org` | unset |
| `VERIFICATION_AUTO_RUN_ENABLED` | `false` — выключить фоновый прогон проверки данных после пересчёта | unset (включено) |
| `VERIFICATION_RETENTION_ENABLED` | `false` — не чистить прогоны проверки старше 30 дней | unset (включено) |

### Бот «Перечня тендеров»

Отдельной настройки на бэкенде нет. Напоминания, карточки тендеров и запись в
хронологию отдаёт API по машинному ключу с областями `registry:*`; сообщения в
чат команды отправляет внешний бот (@SU10TenderNotes_BOT). Контракт и порядок
подключения — [api/REGISTRY.md](../api/REGISTRY.md).

### TLS root CA для Yandex

`/certs/yandex-ca.pem` — read-only mount в контейнер `hubtender-bff`.
Скачивается с `https://storage.yandexcloud.net/cloud-certs/CA.pem`.
Путь жёстко прописан в `DATABASE_URL` через query-param `sslrootcert=`.

## Что НЕ нужно для runtime (удалено)

| Переменная | Когда использовалась | Статус |
|---|---|---|
| `AUTH_MODE` (backend) | dual-issuer cutover (Supabase + app JWT) | удалена; backend принимает только app JWT |
| `SUPABASE_JWKS_URL` | JWKS для верификации Supabase JWT | удалена; backend больше не верифицирует Supabase JWT |
| `SUPABASE_JWT_ISSUER` | `iss`-проверка Supabase JWT | удалена |
| `SUPABASE_JWT_SECRET` | HS256-вариант (никогда не активировался в проде) | удалена |
| `VITE_AUTH_MODE` (frontend) | переключатель supabase/app для UI | удалена; UI всегда использует app-auth |
| `VITE_SUPABASE_URL` / `VITE_SUPABASE_PUBLISHABLE_KEY` (frontend) | конфиг supabase-js SDK | удалены вместе с SDK (см. doc 43) |
| `SUPABASE_SERVICE_ROLE_KEY` | dev-скрипты, миграционные ETL | удалена вместе с миграционным архивом |

Миграционные переменные (`OLD_*`, `DUAL_RUN_*`, `ALLOW_*` и пр.) — см.
git-историю (`archive/migrations/2026-05-db-cutover/`, удалено из рабочего дерева).

## Local development

```bash
# Frontend
npm install
npm run dev                    # http://localhost:5185

# Backend (через Docker)
docker build -t hubtender-api:local ./backend
docker run --rm --env-file .env -p 3005:3005 hubtender-api:local

# Backend (native Go, требует Go 1.23+)
cd backend && go build -o /tmp/hubtender ./cmd/server
DATABASE_URL=... APP_JWT_ISSUER=... APP_JWT_PRIVATE_KEY_PATH=... CORS_ORIGINS=... /tmp/hubtender
```

## AI / LLM (OpenRouter либо LLM-прокси)

Раздел отсутствовал: переменные читались кодом и жили в `.env.prod`, но нигде не
были описаны. Транспорт выбирается `AI_PROVIDER_MODE` и **только** server env —
из request/frontend ни режим, ни base URL, ни модель не принимаются.

| Переменная | Обяз. | Default | Назначение |
|---|---|---|---|
| `AI_PROVIDER_MODE` | нет | `openrouter` | `openrouter` \| `proxy_llm`. Опечатка **валит старт** — молчаливый откат означал бы вызовы в сеть, которой у прод-хоста нет |
| `OPENROUTER_API_KEY` | нет | пусто | ❌ секрет. Пустой = `not_configured`, приложение работает |
| `OPENROUTER_API_BASE` | нет | официальный | В production вне allowlist **игнорируется** |
| `OPENROUTER_TIMEOUT_SECONDS` | нет | `60` | Таймаут вызова в режиме `openrouter` |
| `AI_HTTPS_PROXY_URL` | нет | — | HTTP(S)-прокси только для запросов к модели (CONNECT, TLS до провайдера не разрывается), когда у хоста нет выхода к openrouter.ai. Прод: tinyproxy на nl3 (`http://194.37.81.38:18443`, в фильтре `openrouter.ai`) |
| `OPENROUTER_HTTP_REFERER` / `OPENROUTER_APP_TITLE` | нет | пусто | Маркетинговые заголовки OpenRouter; в режиме прокси не отправляются |
| `OPENROUTER_LIVE_TEST` | нет | `false` | Разрешает live evaluation |
| `PROXY_LLM_BASE_URL` | да при `proxy_llm` | — | **ORIGIN** прокси (`/healthz` живёт вне `/api/v1`). В production обязателен https |
| `PROXY_LLM_TOKEN` | да при `proxy_llm` | — | ❌ секрет. Ровно 64 hex-символа; ключ `sk-or-…` сюда не подходит |
| `PROXY_LLM_TIMEOUT_SECONDS` | нет | `200` | Обязан быть **> 190** — иначе 504 `deadline_exceeded` прокси недостижим |
| `PROXY_LLM_ACK_NO_PROVIDER_POLICY` | да при `proxy_llm` | `false` | Подтверждение, что прокси вырезает `provider` и privacy-политика делегирована его оператору. Без него транспорт **отключён** (`not_configured`), процесс не падает |
| `AI_ROLLOUT_MAINTENANCE_ENABLED` / `_SCAN_INTERVAL` | нет | `true` | Recovery резерваций + retention |

**Что теряется в режиме `proxy_llm`** (осознанный размен, не баг): прокси
вырезает объект `provider`, поэтому `require_zdr`, `data_collection=deny`,
`require_parameters` и запрет provider-fallback на стороне провайдера **не
применяются** — они остаются договорённостью с оператором прокси. Админка
показывает это явным красным баннером, а `provider_policy_version` меняется на
`proxy-llm-policy-v1`, что автоматически снимает активацию и требует
перезапустить model test.

Каталога моделей и статуса ключа у прокси нет: цена модели неизвестна, поэтому
месячный бюджет в USD вырождается в счётчик запросов по плоскому резерву
(`budget_kind = reservation_units`). Измеримый потолок в этом режиме —
`monthly_token_budget`.

### Выбор модели в режиме `proxy_llm`

По умолчанию действует **вариант A**: модель выбирает прокси, в поле `model`
уходит заглушка `proxy`, каталог в админке состоит из одной синтетической
записи. Fallback-цепочка прокси при этом работает.

Чтобы выбирать модель самому (**вариант B/C**), в админке есть ручной ввод
слага — списка моделей взять неоткуда, эндпоинта каталога у прокси нет.
Порядок и цена решения:

1. Оператор прокси обязан разрешить выбор вашему `clientId`
   (`allowedModels: ["*"]` или белый список). **Пока там пусто, присланный слаг
   молча игнорируется** — UI будет выглядеть рабочим, а маршрутизация не
   изменится.
2. Явный выбор **отключает fallback-цепочку** прокси: недоступен провайдер —
   придёт ошибка, а не переезд на резервную модель.
3. Существование слага не проверяет никто: формат валидируем мы, реальность —
   только model test. Опечатка вернётся ошибкой OpenRouter через прокси.
4. Цена, длина контекста и supported_parameters остаются неизвестными и в этом
   варианте — они не приходят ниоткуда.

Смена слага меняет config hash, поэтому автоматически сбрасывает model test и
выключает rollout. Откат к варианту A — выбрать в каталоге строку `proxy`.

Единственная защита от подмены модели на стороне оператора — **протухание
теста**: `ai_feature_settings.model_test_max_age_hours` (по умолчанию 168 ч).
Устаревший тест блокирует живые вызовы и активацию пилота. Работает только в
режиме `proxy_llm`: в прямом режиме модель пришпилена config hash'ем.

## Что под секретом, что нет

| Категория | Можно ли в логи/git | Пример |
|---|---|---|
| `DATABASE_URL` (с паролем) | ❌ никогда | prod Yandex DSN |
| `APP_JWT_PRIVATE_KEY_*` | ❌ никогда — компрометация = полный auth bypass | mounted PEM или base64 в `.env.prod` |
| `APP_JWT_ISSUER` / `APP_JWT_AUDIENCE` / `APP_JWT_KEY_ID` | ✅ public (kid появляется в JWKS) | `https://tender.su10.ru` / `hubtender-web` |
| `SMTP_PASSWORD` / `SMTP_USER` | ❌ никогда | provider creds |
| Yandex cluster ID (`rc1d-…` / `c-…`) | ⚠ внутренний идентификатор, не credential | в docs допустимо как reference |
| Yandex CA PEM | ✅ public (downloads from Yandex docs) | `/certs/yandex-ca.pem` |
| `OPENROUTER_API_KEY` / `PROXY_LLM_TOKEN` | ❌ никогда — в логи не пишутся, наружу отдаётся только суффикс | `sk-or-…` / 64 hex |
| `PROXY_LLM_BASE_URL` | ✅ адрес, не credential | `https://proxy.example.com` |
