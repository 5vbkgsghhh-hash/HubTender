# Architecture — TenderHUB

Текущая runtime-архитектура после переезда БД на Yandex Managed PostgreSQL
и завершения Phase 5 (frontend Supabase business migration).

## Topology

```
┌─────────────────────────────────────────────────────────────┐
│  React 18 + Ant Design + Vite frontend                      │
│  https://tender.su10.ru                                     │
│                                                             │
│  ├─ App-auth client (src/lib/auth/) — POST /api/v1/auth/    │
│  │   login → RS256 JWT (issued by Go BFF). Stored under     │
│  │   localStorage prefix `hubtender_app_auth_*`. Refresh    │
│  │   token rotation, cross-tab Web Locks coordination.      │
│  │                                                          │
│  └─ Все вызовы → fetch('/api/v1/*', Bearer JWT)             │
└────────────────┬────────────────────────────────────────────┘
                 │ HTTPS + Bearer JWT (RS256, kid in JWKS)
                 ▼
┌─────────────────────────────────────────────────────────────┐
│  nginx (tender.su10.ru)                                     │
│  /api/   →  127.0.0.1:3006     (Go BFF)                     │
│  /api/v1/ws  →  upgrade WebSocket                           │
│  /         →  SPA fallback (index.html)                     │
└────────────────┬────────────────────────────────────────────┘
                 │
                 ▼
┌─────────────────────────────────────────────────────────────┐
│  Go BFF (chi + pgx/v5 + coder/websocket)                    │
│  container `hubtender-bff` (systemd, multi-stage distroless)│
│                                                             │
│  ├─ App-auth service (issuer + repository + handlers)       │
│  │   POST /api/v1/auth/{login,register,refresh,logout,me,   │
│  │   forgot-password,reset-password,change-password}        │
│  │   GET  /.well-known/jwks.json   (RS256 public key)       │
│  │                                                          │
│  ├─ JWT verify middleware — only the local issuer is        │
│  │   accepted. APP_JWT_ISSUER, APP_JWT_AUDIENCE,            │
│  │   APP_JWT_PRIVATE_KEY_* configure signing and verify.    │
│  │                                                          │
│  ├─ HTTP handlers /api/v1/* (≈111 уникальных путей)         │
│  ├─ Native WS hub (Postgres LISTEN/NOTIFY → topics)         │
│  ├─ pgxpool: session-mode pooler                            │
│  └─ Audit-in-same-tx pattern для BOQ-мутаций                │
└────────────────┬────────────────────────────────────────────┘
                 │ pgx TLS (sslmode=verify-full)
                 ▼
┌─────────────────────────────────────────────────────────────┐
│  Yandex Managed PostgreSQL 17                               │
│  c-c9qmbgvs6rit4qfe0dni.rw.mdb.yandexcloud.net:6432         │
│  sslrootcert: /certs/yandex-ca.pem                          │
│                                                             │
│  ├─ 41 public-таблица (schema из db/yandex/sql/)            │
│  ├─ auth.users + auth.identities (legacy user_id FK; auth   │
│  │   credentials are managed by app_auth.password_hashes)   │
│  ├─ app_auth.{password_hashes, refresh_tokens,              │
│  │            password_reset_tokens, auth_events}           │
│  ├─ Триггеры trg_notify_row_change_* на 6 таблицах          │
│  │   (notifications, boq_items, client_positions,           │
│  │    cost_redistribution_results, construction_cost_volumes│
│  │    tenders)                                              │
│  └─ Audit log: public.boq_items_audit (~408k rows)          │
└─────────────────────────────────────────────────────────────┘
```

## Что где живёт

| Слой | Где | Описание |
|---|---|---|
| Frontend SPA | `src/` | React/Vite, vendor chunks ~ 1.3 MB gzip |
| API client | `src/lib/api/*.ts` | один файл на домен, все вызовы через `apiFetch(/api/v1/...)` |
| Realtime client | `src/lib/realtime/useRealtimeTopic.ts` | подписка на WS-топики Go BFF |
| App-auth client | `src/lib/auth/client.ts` | login/refresh/me/logout/forgot/reset/change, cross-tab Web Locks |
| TS types | `src/lib/types/types.ts` + `database.types.ts` | TypeScript type definitions + access-control хелперы (`hasPageAccess`, `canManageUsers`). Supabase SDK удалён |
| Go BFF entrypoint | `backend/cmd/server/` | main.go (bootstrap) + wire.go (DI) + routes.go (chi router) |
| Go BFF слои | `backend/internal/{handlers,services,repository}/` | 3-layer |
| App-auth backend | `backend/internal/auth/` | issuer + repository + handlers + JWKS |
| Pricing/markup calc | `backend/internal/calc/` | port TS-ядра (юнит-тесты) |
| Yandex schema baseline | `db/yandex/sql/` | 00..90 SQL — текущий applied schema |
| Static deploy | `dist/` → `/srv/sites/tender.su10.ru/public/` | rsync на nginx |
| Production env | `/srv/sites/tender.su10.ru/server/.env.prod` на prod-сервере | `chmod 600`, **вне git** |

## Auth model

App-auth only. Supabase Auth runtime полностью удалён.

1. Пользователь логинится через `POST /api/v1/auth/login` (Go BFF).
2. Go BFF проверяет bcrypt-хэш в `app_auth.password_hashes` и выдаёт пару
   токенов: RS256 access JWT (TTL = `APP_ACCESS_TOKEN_TTL_MINUTES`, default
   15 min) + opaque refresh token (SHA-256 hash в
   `app_auth.refresh_tokens`, TTL = `APP_REFRESH_TOKEN_TTL_DAYS`, default
   30 days).
3. Frontend хранит сессию под префиксом `localStorage.hubtender_app_auth_*`
   и кладёт access JWT в `Authorization: Bearer …` для каждого `/api/v1/*`.
4. Go BFF верифицирует JWT через локально загруженный публичный ключ
   (`APP_JWT_*` env). `iss`-claim должен совпадать с `APP_JWT_ISSUER`.
5. На 401 frontend выполняет одну попытку refresh — `POST /api/v1/auth/refresh`
   принимает opaque refresh-токен, выдаёт новую пару (token-family rotation
   с reuse-detection: переиспользование старого refresh-токена ревокает
   всю семью).
6. JWKS — `GET /.well-known/jwks.json` (опубликованный публичный ключ;
   `kid` — JWK-thumbprint публичного ключа, вычисляется при старте).
7. Sub-claim JWT = `auth.users.id` = `public.users.id`.
8. Password recovery — `forgot-password` / `reset-password` /
   `change-password` через `app_auth.password_reset_tokens` + SMTP mailer.

## Realtime

- Postgres → `LISTEN/NOTIFY` (channel `rowchange`)
- Go BFF слушает + публикует в native WS hub
- Frontend подключается к `/api/v1/ws` (с JWT в query param `?token=`)
- Топики: `notifications:<user_id>`, `tender:<tender_id>`, `tenders`
- Debounce 200 ms на топик, slow clients drop'аются с логом

## Что НЕ используется в runtime

- ❌ Supabase runtime удалён целиком — Auth + Data + SDK
  (`@supabase/supabase-js` снят из `package.json`, bundle не содержит
  `supabase.co`, `/auth/v1/*`, `/rest/v1/*`, `GoTrueClient`).
- ❌ Supabase Auth JWT verify на backend — `SUPABASE_JWKS_URL`,
  `SUPABASE_JWT_ISSUER`, `AUTH_MODE` env-переменные удалены.
- ❌ Supabase Realtime каналы — заменены WS hub Go BFF.
- ❌ Yandex Identity / IAM — не интегрирован, БД через DSN с паролем.

## Внешние зависимости (runtime)

| Service | Назначение | Что произойдёт если упадёт |
|---|---|---|
| Yandex Managed PostgreSQL | Source-of-truth БД + app_auth.* credential storage | Go BFF не сможет читать/писать → 5xx на бизнес-запросы и login |
| SMTP provider | Только password recovery (forgot-password) | `/forgot-password` уже отдаёт 503 в проде если SMTP не настроен; нет регрессии до настройки |
| nginx | TLS termination + reverse proxy | Frontend недоступен извне |
| systemd `hubtender-bff` | Go BFF process supervisor | Авто-перезапуск при крэше |
| OpenRouter **либо** LLM-прокси | AI-подбор номенклатуры в Smart Import (пилот) | AI-подбор отключается, детерминированный подбор и ручной поиск работают без регрессии |
| cbr.ru | Курсы валют для тендеров | Курсы не автозаполняются, вводятся вручную |

## См. также

- `docs/RUNTIME_ENV.md` — переменные окружения и где они живут
- миграционная история 2026-05 db-cutover — в git-истории (`archive/migrations/2026-05-db-cutover/`, удалено из рабочего дерева)
- `CLAUDE.md` — гайд для разработки (стек, команды, паттерны)
- `BRANDING.md` — дизайн-система
