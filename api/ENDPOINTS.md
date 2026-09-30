# Справочник эндпоинтов машинного доступа

Авторизация — заголовок `X-API-Key: thk_…`. `Authorization: Bearer <JWT>` — это путь
человека с сессией портала; ключ в этом заголовке не работает и даёт
`401 invalid or expired token` (разбор — в [README.md](README.md#ошибки-и-диагностика)).
Конверт ответа — `{"data": …}`, у постраничных списков ещё `next_cursor`.
Ошибки — RFC 7807 с полем `code`. Точные схемы — в OpenAPI:
`GET /api/v1/archive/openapi.yaml`.

**Чего у ключа нет.** Открыты только перечисленные ниже маршруты и маршруты из
[VERIFICATION.md](VERIFICATION.md) («Проверка данных») и [REGISTRY.md](REGISTRY.md)
(«Перечень тендеров» для Telegram-бота). В частности,
`GET /api/v1/tenders` (список тендеров для UI) ключу **недоступен** — он под JWT;
машинный список это `GET /api/v1/tenders/brief`. Админские
`/api/v1/admin/api-access/*` и остальные эндпоинты портала ключу тоже закрыты.

## Чтение тендеров и смет — область `tenders:read`

### `GET /api/v1/tenders/brief` — список тендеров

Выбор цели. Без страниц (до 1000 строк), порядок — номер тендера, свежая версия первой.
Ключ с ограничением по тендерам видит только их.

| Параметр | |
|---|---|
| `search` | подстрока без учёта регистра по названию, заказчику и номеру |
| `is_archived` | `true` / `false` |

Строка: `id, tender_number, title, client_name, version, is_archived, housing_class,
construction_scope, submission_deadline, updated_at`.

`construction_scope` (`генподряд` / `коробка` / `монолит`) — объём строительства
тендера, не раздел сметы.

### `GET /api/v1/tenders/{id}/overview` — шапка

`id, tender_number, title, client_name, housing_class, construction_scope, is_archived,
cached_grand_total` (итог КП), `usd_rate, eur_rate, cny_rate, position_count,
boq_item_count, created_at, updated_at`.

### `GET /api/v1/tenders/{id}/positions/with-costs` — позиции с итогами и КП

Весь тендер одним массивом (без страниц; ~10 МБ на 11 тыс. позиций, gzip), порядок
`position_number`. Кэш 30 с, сбрасывается заголовком `Cache-Control: no-cache`.

Поля позиции: `id, tender_id, position_number, item_no, work_name, client_note,
unit_code, volume, manual_volume, manual_note, hierarchy_level, is_additional,
parent_position_id, total_material, total_works, material_cost_per_unit,
work_cost_per_unit, total_commercial_material, total_commercial_work,
total_commercial_material_per_unit, total_commercial_work_per_unit, rich_runs,
created_at, updated_at`.

Агрегаты по строкам: `base_total` (себестоимость), `commercial_total` (КП),
`material_cost_total, work_cost_total, markup_percentage, items_count`.

### `GET /api/v1/tenders/{id}/positions` — позиции с признаками раздела

Постранично: `limit` (1–200, по умолчанию 50), `cursor` из `next_cursor`. Порядок
`updated_at DESC` — для иерархии заберите все страницы и отсортируйте по
`position_number` (CLI `positions` делает это сам).

Поля: те же, что у позиции выше, плюс `section_number, position_name` и два
производных:

- `is_section` — заголовок раздела, а не исполняемая позиция (следующая
  не-дополнительная позиция лежит глубже по `hierarchy_level`);
- `cost_category_id`, `cost_category_name` — раздел сметы по строкам позиции: самая
  частая категория затрат (`МОНОЛИТНЫЕ РАБОТЫ`, `КРОВЛЯ`, …); `null`, пока позиция не
  расценена. Именно по нему отбирают «монолит».

### `GET /api/v1/tenders/{id}/boq-items-full` — все строки сметы

Самый тяжёлый маршрут: все `boq_items` тендера без страниц и кэша, порядок
`sort_number, id`. Для одной позиции — `GET /api/v1/positions/{id}/boq-items-full`
(тот же формат).

Строка: `id, tender_id, client_position_id, sort_number, boq_item_type`
(`мат`/`суб-мат`/`мат-комп.`/`раб`/`суб-раб`/`раб-комп.`), `material_type`
(`основн.`/`вспомогат.`), `description, unit_code, quantity, base_quantity,
consumption_coefficient, conversion_coefficient, unit_rate, currency_type,
delivery_price_type, delivery_amount, total_amount, commercial_markup,
total_commercial_material_cost, total_commercial_work_cost, detail_cost_category_id,
material_name_id, work_name_id, parent_work_item_id, quote_link, import_session_id,
created_at, updated_at`.

Вложенные справочники (могут быть `null`): `work_names {name, unit}`,
`material_names {name, unit}`, `parent_work {work_names {name}}` (работа, к которой
привязан материал), `detail_cost_categories {name, location, cost_categories {name}}`.

### `GET /api/v1/tenders/{id}/positions/{posId}/items` — сырые строки позиции

Без страниц, порядок `sort_number`. Те же скаляры, что выше, без вложенных имён и
коммерческих стоимостей, плюс `quote_price_date, quote_valid_until`. Нужен, чтобы
перед записью увидеть, что уже есть.

### `GET /api/v1/items/{id}` — одна строка BOQ

Сырая строка и заголовок `ETag` (для `If-Match` при PATCH). Тендер для ограничения
ключа определяется по строке в базе.

## Архив смет — области `archive:read` / `archive:write`

| Метод | Путь | Область | Назначение |
|---|---|---|---|
| GET | `/api/v1/archive/positions/search?q=…` | `archive:read` | похожие исторические позиции (`unit_code, volume, item_no, limit, min_score, exclude_tender_id, period_months`) |
| POST | `/api/v1/archive/positions/suggest` | `archive:read` | батч-подбор до 100 запросов |
| GET | `/api/v1/archive/positions/{id}` | `archive:read` | историческая позиция + тендер + строки |
| POST | `/api/v1/archive/compose` | `archive:write` | сборка позиций целевого тендера (`dry_run` по умолчанию) |
| GET | `/api/v1/archive/openapi.yaml` | — | спецификация |

Подробно — [`docs/ESTIMATE_ARCHIVE_API.md`](../docs/ESTIMATE_ARCHIVE_API.md).

## Запись строк — область `tenders:write`

| Метод | Путь | Заметки |
|---|---|---|
| POST | `/api/v1/tenders/{id}/positions/{posId}/items` | обязателен `boq_item_type`; обычно `description, unit_code, quantity, unit_rate, currency_type`; `work_name_id`/`material_name_id` — опционально. Ответ 201 + `ETag` |
| PATCH | `/api/v1/items/{id}` | обязателен `If-Match` с ETag; при расхождении 412 с актуальной строкой |
| POST | `/api/v1/positions/{id}/recompute-totals` | после серии записей |

Идемпотентности нет: перед повтором читайте строки позиции. Суммы и коммерческие
стоимости считает сервер. Имена номенклатуры сервер не сопоставляет — либо текст в
`description`, либо готовые id.

## Лимиты и поведение

- Лимит запросов — `rate_limit_per_minute` на ключ (по умолчанию 120, окно — минута);
  превышение → 429.
- Ограничение ключа по тендерам: для маршрутов с `{id}` тендера в URL проверяется гейтом,
  для `/positions/{id}/…` и `/items/{id}` — по позиции/строке в базе, для
  `/tenders/brief` — фильтром выборки.
- Таймаут сервера — 5 мин на запрос; ответы с `ETag` поддерживают `If-None-Match` → 304.
- Каждый вызов по ключу пишется в журнал (метаданные, без тел).
