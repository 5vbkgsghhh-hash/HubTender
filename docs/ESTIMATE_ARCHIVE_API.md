# Архив смет — API

Машинный доступ к историческим сметам: поиск позиций заказчика по всем прошлым тендерам,
подбор аналогов и сборка новых позиций целевого тендера на их основе. UI нет — домен
рассчитан на вызовы из кода.

- Спецификация: [`backend/internal/handlers/openapi/archive.yaml`](../backend/internal/handlers/openapi/archive.yaml),
  она же отдаётся живым сервисом по `GET /api/v1/archive/openapi.yaml`.
  Клиент генерируется любым `openapi-generator` — это и заменяет автодоки FastAPI.
- Инварианты расчёта: [`CALCULATION_SOURCE_OF_TRUTH.md`](CALCULATION_SOURCE_OF_TRUTH.md), §7b-bis.
- Подключение из Cursor: [`ESTIMATE_ARCHIVE_CURSOR.md`](ESTIMATE_ARCHIVE_CURSOR.md).
- Просмотр тендеров и смет любым помощником (Cursor, Codex, Claude Code, curl): [`api/README.md`](../api/README.md).

## Почему не отдельный Python-сервис

Все инварианты расчёта (`total_amount`, коммерческие колонки, `cached_grand_total`,
финансовые ревизии, audit-триггер) реализованы в Go BFF. Писать в БД мимо них — гарантированный
рассинхрон тендера. Плюс прод не использует docker-compose (Go BFF под systemd, образ собирается
на самом проде, реестра образов нет), а ингресс-nginx лежит вне репозитория. Отдельный рантайм
дал бы только автодокументацию — её закрывает OpenAPI-спека.

## Авторизация

Два способа. Для внешнего кода — **ключ**, для человека в браузере — обычный JWT портала.

### Ключ (машинный доступ)

Выпускается в UI: **Настройки → Доступ к API → Ключи → Выпустить ключ**. Секрет
показывается один раз; в БД лежит только его SHA-256 хеш, восстановить нельзя.

```bash
curl -s "$BASE/api/v1/archive/positions/search?q=стяжка" -H "X-API-Key: thk_..."
```

Ключ действует **от имени выпустившего пользователя** — правки в сметах подписываются им
(аудит `boq_items_audit.changed_by` ссылается на реального пользователя, анонимной записи
быть не может). Ограничения ключа:

| Ограничение | Поведение при нарушении |
|---|---|
| Область `archive:read` — поиск, подбор, чтение позиции архива | 403 |
| Область `archive:write` — `compose` | 403 |
| Область `tenders:read` — список тендеров, шапка, позиции с итогами, строки сметы | 403 |
| Область `tenders:write` — запись строк BOQ в позиции тендера | 403 |
| Области `verification:read` / `verification:write` — «Проверка данных» | 403 |
| Области `registry:read` / `registry:write` — «Перечень тендеров»; только по ключу и без списка тендеров | 403 |
| Список разрешённых тендеров (пусто = все) | 403 при чужом тендере |
| Срок действия, отзыв | 401 (просроченный и отозванный не находятся вовсе) |
| Лимит запросов в минуту | 429 |

### JWT (человек)

```bash
TOKEN=$(curl -s -X POST "$BASE/api/v1/auth/login"   -H 'Content-Type: application/json'   -d '{"email":"service@example.com","password":"..."}' | jq -r .access_token)

curl -s "$BASE/api/v1/archive/positions/search?q=..." -H "Authorization: Bearer $TOKEN"
```

Заголовок `X-API-Key` имеет приоритет: если он есть, JWT не рассматривается — смешивать
двух принципалов в одном запросе нельзя, иначе непонятно, чьи ограничения применять.
К JWT области не применяются: у человека прав ровно столько, сколько даёт роль и список
страниц. Per-tender ACL для JWT в BFF по-прежнему нет — авторизованный пользователь видит
позиции любого тендера.

## Управление доступом (UI)

Страница **Настройки → Доступ к API** (`/admin/api-access`), доступна ролям
`administrator` и `developer` — гейт серверный (`RequireRoles` на `/api/v1/admin/api-access/*`),
а не только пункт меню.

Три вкладки:

- **Ключи** — выпуск, отзыв, удаление. Видно префикс, права, срок, число вызовов за сутки
  и кто выпустил. Отзыв действует немедленно.
- **Выдача API** — тумблеры каждого эндпоинта (выключенный отвечает 503) и потолки:
  максимум строк в выдаче, кандидатов префильтра, запросов в батче, запросов в минуту на
  ключ, срок хранения журнала. Тумблеры применяются и к вызовам по JWT: это кнопка
  экстренной остановки домена, кэш настроек живёт 10 секунд.
- **Журнал вызовов** — время, источник (ключ или браузер), запрос, статус, код ошибки,
  число затронутых строк, признак `dry_run`, длительность. Только метаданные: ни тел
  запросов, ни секретов. Чистится по сроку хранения.

Админские эндпоинты: `GET|POST /api/v1/admin/api-access/keys`,
`POST /api/v1/admin/api-access/keys/{id}/revoke`, `DELETE .../keys/{id}`,
`GET|PUT .../settings`, `GET .../calls`.

## Эндпоинты, сценарий сборки, коды ошибок

Контракт для потребителя вынесен в каталог `api/` — здесь он не дублируется:

- [`api/ENDPOINTS.md`](../api/ENDPOINTS.md) — все 34 операции машинного доступа с областями;
- [`api/ARCHIVE.md`](../api/ARCHIVE.md) — параметры поиска, как считается похожесть, сценарий
  «найти аналог → `dry_run` → запись», тело `compose`, коды `ARCHIVE_*`;
- [`api/VERIFICATION.md`](../api/VERIFICATION.md), [`api/REGISTRY.md`](../api/REGISTRY.md) —
  «Проверка данных» и «Перечень тендеров».

Скоринг живёт в `internal/analytics/estimatearchive` (веса: название 50, `item_no` 30,
единица 10, объём 10; отсечка по умолчанию 0.35), значащие токены — из
`internal/ai/nomenclature`. Связь материал → работа не пересекает позицию из-за FK
`boq_items_parent_scope_fkey` — отсюда правило «одна группа = одна целевая позиция».

## Производительность поиска

Префильтр использует `ILIKE '%токен%'` — индекса под это нет. Схема в `db/yandex/sql/`
принципиально не содержит `CREATE EXTENSION` (расширения включаются в консоли Yandex MDB),
поэтому `pg_trgm` в первой версии не вводится. Ограничители: `candidate_limit` (без параметра —
потолок из настроек, по умолчанию 4000), `only_latest_version` (одна версия на `tender_number`), `period_months`,
`with_boq_only`. Если поиск станет узким местом — включить `pg_trgm` в консоли, добавить
GIN-индекс `client_positions USING gin (work_name gin_trgm_ops)` и заменить `ILIKE ANY` на
`%` / `similarity()`: точка подмены изолирована в `LoadCandidates`
(`backend/internal/repository/archive_search.go`).

## Код

| Слой | Файлы |
|---|---|
| Скоринг (pure) | `backend/internal/analytics/estimatearchive/` |
| Репозиторий | `backend/internal/repository/archive_search.go`, `archive_read.go`, `archive_compose*.go` |
| Сервис | `backend/internal/services/archive.go` |
| Хендлеры | `backend/internal/handlers/archive_search.go`, `archive.go`, `archive_openapi.go` |
| Ошибки | `backend/pkg/apierr/archive.go` |
| Ключи, настройки, журнал | `backend/internal/apikey/`, `backend/internal/repository/api_access*.go`, `backend/internal/services/api_access.go`, `backend/internal/handlers/api_access.go`, `backend/internal/middleware/apikey.go` |
| UI управления доступом | `src/pages/AdminApiAccess/`, `src/lib/api/apiAccess.ts` |
| CLI и правило для Cursor | `scripts/archive-api.mjs`, `.cursor/rules/estimate-archive-api.mdc` |
| Схема | `db/yandex/incremental/`: `2026_08_api_access_control.sql`, `2026_08_api_scope_tenders_read.sql`, `2026_08_api_scope_tenders_write.sql`, `2026_09_api_scope_verification.sql`, `2026_10_tender_registry_api.sql` |
| Маршруты | машинные — `backend/cmd/server/routes_machine.go`; админские `/api/v1/admin/api-access/*` — `routes.go` |
