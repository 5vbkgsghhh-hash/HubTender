# Controlled AI Rollout (этап 2.6|0) — runbook

Контролируемое включение OpenRouter-модели (настроенной в
[OPENROUTER_AI_ADMINISTRATION.md](OPENROUTER_AI_ADMINISTRATION.md)) для
ограниченной пилотной группы Smart Import. После 2.6|0 функциональная
дорожная карта завершена: дальнейшие действия — только операционные
процедуры этого runbook'а.

## 1. Цель

Live AI-подбор номенклатуры для allowlisted-пилотов: по явному нажатию,
с ручным подтверждением каждого предложения, под квотами, бюджетом,
circuit breaker'ом и мгновенным kill switch. AI не выбирает номенклатуру
автоматически, не выполняет импорт, не создаёт справочник, не меняет
финансовые данные.

## 2. Rollout modes

`off → evaluation → pilot_individual → pilot_bulk`, любой → `off`
немедленно. Режимов `all`/`public`/general availability НЕ существует.
Off: user-вызовы запрещены; deterministic/aliases/manual работают; admin
test/evaluation разрешены. Evaluation: + admin live evaluation.
Pilot_individual: live AI только пилотам, каждое предложение — ручное
подтверждение, bulk запрещён. Pilot_bulk: + bulk-подтверждение
server-marked high-confidence строк (импорт не запускается автоматически).
Mode хранится в БД (`ai_feature_settings.rollout_mode`, default `off`);
frontend не может подменить mode/пилотство/bulk/модель.

## 3. Transition gates

- **off→evaluation**: ключ настроен; connection=connected; модель выбрана;
  тест пройден; config hash совпадает; модель в каталоге и не истекла;
  privacy policy 2.5 не изменена.
- **evaluation→pilot_individual**: live evaluation EXECUTED+PASS и
  актуальна (hash/model); dataset ≥ 15 eligible; allowlist не пуст;
  monthly budget > 0; квоты валидны; circuit closed; remaining limit ключа
  не исчерпан.
- **pilot_individual→pilot_bulk**: ≥ 50 успешных pilot row outcomes;
  high-confidence changed-rate ≤ 2%; critical hard-negative FP = 0;
  invalid/hallucinated accepted = 0; fallback 100%; бюджет/ключ здоровы;
  подтверждение админа (фраза = имя режима).
- Любое значимое изменение (model ID/prompt/schema/policy/adapter) →
  автоматически `off` + инвалидация live evaluation и bulk-гейта.

Подтверждения пользователей — **прокси-сигнал качества, не доказанная
точность** (см. §15).

## 4. Pilot allowlist

`ai_pilot_users`: только существующие активные пользователи через admin
users API (free-text UUID нет); самодобавление запрещено; membership
server-side (active + expires_at); удаление действует немедленно (включая
in-flight — см. §11). Non-pilot видит deterministic/manual flow;
capability = `not_allowed`.

## 5. Квоты пользователей

Requests/день и rows/день (UTC), default 20/400, override per-user.
Проверка и резервирование — атомарно (advisory lock по feature): два
параллельных запроса не проходят за последний слот. Исчерпание → статус
`user_quota_exhausted`/`row_quota_exhausted`, deterministic candidates
возвращаются, импорт не блокируется.

## 6. Месячный бюджет

`monthly_budget_usd` (UTC-месяц). Учёт: completed по
`COALESCE(actual, estimated, reservation)`, reserved/failed — по
reservation. Превышение → `budget_exhausted` (деградация, не блокер).
Вся денежная арифметика — exact decimal (`numeric` + `big.Rat`), бюджет
для учёта читается как `numeric::text`.

## 7. OpenRouter key limit

Перед вызовом — свежий (≤ 5 мин, `aiKeyStatusMaxAge`) GET /key:
remaining < reservation → `key_limit_exhausted`. Статус недоступен и кэш
старше 5 мин → fail-safe отказ (`provider_unavailable`), 402 не
превращается в retry-storm (retry-политика клиента 2.5: без retry 402).

## 8. Usage/cost единицы

Ledger `ai_usage_requests`: `actual_provider_cost` — официальное поле
ответа `usage.cost` (**кредиты OpenRouter; по официальной документации
GET /key usage деноминирован в USD**); `estimated_cost` — catalog-оценка
(токены × цены); `cost_source` = `provider_reported | catalog_estimate`.
Единица подписана в UI/API: «USD (кредиты OpenRouter)».

## 9. Reservation / reconciliation

До вызова — консервативная резервация: `prompt_price×(rows×500+800) +
completion_price×(max_output×батчи)`, ×2 safety, cap
`request_max_reserved_cost` (default 0.05). После ответа — reconciliation:
токены, provider cost, `reservation_underestimate=true` при actual >
reserved (превышение не скрывается). Ошибка провайдера → `failed` с
фактическим usage; crash → просроченные reservations освобождает
maintenance-воркер (startup + каждые 60 с; multi-instance-safe идемпотентный
UPDATE).

## 10. Circuit breaker

`ai_circuit_state` (строка БД — multi-instance-safe): closed → open после
3 подряд отказов (timeout/transport/5xx/повторный invalid/429), cooldown
5 мин → ровно ОДИН half-open probe (атомарный CAS), успех закрывает,
отказ снова открывает. НЕ отказ: valid abstain, low confidence, user
rejection, fingerprint mismatch, локальные ошибки, quota exhausted.
Open → `circuit_open`, deterministic/manual работают. Admin: состояние +
reset; emergency off сильнее reset.

## 11. Emergency kill switch

«Экстренно отключить AI-подбор»: атомарно `rollout_mode=off` +
`rollout_config_version+1`; не требует OpenRouter; ничего не удаляет;
audit-лог (actor, old mode, reason). In-flight вызов может физически
завершиться, но перед возвратом suggestion backend повторно проверяет
mode/config version/hash/membership — при изменении AI-результат
**отбрасывается** (`stale_discarded`), пользователь получает deterministic
candidates, usage/cost учитываются, circuit валидный ответ отказом не
считает.

## 12. Live evaluation

`go run ./cmd/ai-nomenclature-eval --mode live --dataset synthetic
--confirm-live-provider-cost --save-summary` (или admin-endpoint
`POST /admin/ai/nomenclature/evaluate`). Live требует ОДНОВРЕМЕННО:
`OPENROUTER_LIVE_TEST=true`, `OPENROUTER_API_KEY`, сохранённую
протестированную модель, rollout=evaluation, явное подтверждение стоимости.
Команда read-only (BOQ/aliases/pilot/rollout не меняются), raw
prompt/response не сохраняются; `--save-summary` пишет только безопасный
агрегат (`ai_evaluation_summaries`).

## 13. Dataset privacy

Обязательный dataset — synthetic curated (материалы/работы/марки/классы/
диаметры/сечения, hard negatives, ambiguous, no-match, injection; ≥15
eligible). Approved-aliases dataset — только при явном
`AI_NOMENCLATURE_EVAL_APPROVED_ALIASES=true`, обезличенный экспорт (без
user/tender/workbook/цен/quantity/totals/заказчика/URL/дат/AI-history);
без разрешения aliases не отправляются. Dataset имеет стабильный hash; raw
dataset в summary не сохраняется.

## 14. Quality metrics

recall@20, top-1/top-3, abstention rate/correctness, high-confidence
coverage/precision/FP, critical hard-negative FP, hallucinated-ID
rejections, invalid responses, timeouts, p50/p95, tokens, cost.
Непонижаемые гейты: critical FP = 0; unknown ID accepted = 0; membership
validation 100%; local structured validation 100%; manual fallback 100%;
injection не покидает candidate set; budget не превышен. Safe defaults
pilot_individual: synthetic critical suite 100%, ≥15 eligible, top-3 ≥ 85%,
invalid = 0. Admin UI не может ослабить hard-гейты (critical FP > 0,
unknown ID > 0, fallback < 100% — не настраиваются).

## 15. Acceptance rate ≠ accuracy

Подтверждение пользователя зависит от его внимательности, спешки и
доверия к подсказке — это **recommendation acceptance rate** (прокси), а
не измеренная точность. Используемые термины: recommendation
acceptance/change rate, high-confidence change rate, abstention rate,
manual fallback rate. Это отражено в UI (дисклеймер пилота) и здесь.

## 16-17. Pilot individual / bulk

Individual: кнопка «Подобрать номенклатуру» (live только пилоту при
status=available), model label, остатки квот, disclosure; каждое
предложение подтверждается вручную; bulk скрыт. Bulk: дополнительно
bulk-подтверждение ТОЛЬКО server-marked high-confidence строк через
диалог; импорт по-прежнему запускает только пользователь.

## 18. Feedback linking

Suggest возвращает `ai_request_id` + per-row `feedback_tokens`
(`sha256(request_id|row_reference)` — без internal DB ID и raw-текста).
Execute (после повторного parse/validate и УСПЕШНОГО импорта) финализирует
outcomes: `accepted | changed | manual | abstained | unresolved` +
`imported_successfully`. Сбой персистенса feedback НЕ откатывает импорт
(safe warning `ai_feedback_warning`); повторная финализация не задваивает.

## 19. Data retention

Ledger и row-feedback — 90 дней (`AIMaintenanceConfig.UsageRetention`),
удаление батчами (500), активные reservations и evaluation summaries не
трогаются; aliases/memory/BOQ/import sessions не затрагиваются. Raw
content не хранится никогда. Cleanup — тот же maintenance-воркер
(multi-instance-safe), env: `AI_ROLLOUT_MAINTENANCE_ENABLED`,
`AI_ROLLOUT_MAINTENANCE_SCAN_INTERVAL`.

## 20-21. Observability / health

`GET /health/ai` (redacted): rollout mode, safe model ID, тест/eval
статусы, pilot count, circuit, активные reservations и их возраст,
day/month счётчики, cost (обе метрики + единица), key status age,
maintenance-статистика. Логи — только safe-поля (operation, provider,
model, prompt_version, hash-префикс, rollout_mode, outcome, счётчики);
без user/tender/catalog ID в метках, без raw prompt/response.

## 22. Incident procedure

1) «Экстренно отключить AI-подбор» (админка или
`POST /api/v1/admin/ai/nomenclature/rollout/emergency-off`). 2) Убедиться:
capability = rollout_off; ручной импорт работает. 3) Снять `/health/ai`,
usage и circuit-состояние. 4) Расследовать по safe-кодам ledger
(`provider_outcome`). 5) Возврат — только полным прохождением гейтов
(evaluation → pilot).

## 23. Model/config change

Смена модели/prompt/policy автоматически: rollout=off, тест сброшен, live
evaluation инвалидирована. Порядок восстановления: тест модели → переход
evaluation → live evaluation → pilot_individual → (метрики) → pilot_bulk.

## 24. Key rotation

Заменить `OPENROUTER_API_KEY` в server env → перезапуск backend →
«Проверить подключение». БД не участвует. При компрометации: сначала
emergency off, затем ротация.

## 25. Deployment procedure

Стандартный порядок [PRODUCTION_READINESS.md](PRODUCTION_READINESS.md):
миграции (`2026_07_ai_rollout_controlled.sql` idempotent) → backend →
frontend. Rollout после деплоя остаётся `off` — деплой 2.6 не меняет
поведение для пользователей.

## 26. Staging smoke (перед production pilot)

На disposable/staging БД: rollout=evaluation → synthetic admin evaluation →
pilot_individual для тестового пользователя → один synthetic Smart Import
suggestion → ручное подтверждение → импорт в тестовый тендер → проверка
usage/cost ledger → emergency off → проверка manual fallback → rollout=off.
Production workbook не используется.

Полный сценарий автоматизирован: `LIVE_GATE_OUT=<каталог-артефактов>
bash scripts/readiness/run-live-ai-gate.sh` (ключ читается из `.env`
локально и не печатается; БД — одноразовый docker-postgres). Результаты
последнего прогона — [AI_PILOT_ACCEPTANCE_REPORT.md](AI_PILOT_ACCEPTANCE_REPORT.md).

Выбор модели: политика (`zdr` + `deny` + `require_parameters` без fallback'ов)
доступна не у всех моделей каталога — см. «Находки live-гейта» в отчёте
приёмки (рекомендована `google/gemini-2.5-flash`; медленные
грамматика-endpoint'ы рискуют таймаутами).

## 27. Production pilot checklist (ручные шаги владельца)

1) Прод-деплой по §25; rollout=off. 2) Выбрать/подтвердить модель (тест).
3) Прогнать live evaluation (PASS). 4) Назначить пилотов (2-3 инженера),
бюджет и квоты. 5) Переход pilot_individual в рабочее время. 6) Daily
review (§28). 7) pilot_bulk — только после накопленных метрик.

## 28. Daily pilot review

Ежедневно: requests/rows, provider/estimated cost vs бюджет, remaining
limit ключа, circuit-события, timeout/rate-limit/invalid, stale_discarded,
acceptance/change/manual/abstain rates, high-confidence change rate,
жалобы пилотов.

## 29. Stop conditions

Немедленный emergency off при: любом подозрении на неверные
high-confidence подсказки с критичными марками; росте invalid/timeout;
неожиданном расходе бюджета/ключа; жалобах на подмену выбора; инцидентах
безопасности; недоступности OpenRouter, мешающей работе (хотя ручной путь
не зависит от него).

## 30. Rollback

`rollout=off` (kill switch) — единственный необходимый откат:
deterministic/manual Smart Import продолжает работать, финансовых данных
AI не менял — финансовый rollback не требуется. Ledger/feedback/summaries
сохраняются для анализа.

## 31. Ограничения

Нет general availability; один провайдер (OpenRouter) без fallback-моделей;
no auto-confirm/auto-import; AI не участвует в финансовой логике; alias
dataset — только по явному разрешению; квоты/бюджет — MVP-пилотные.

## Проверки

Guard: `scripts/checks/controlledAiRolloutSafety.check.mjs` (30 правил +
12 негативных self-check); focused:
`scripts/checks/aiRolloutFrontendPolicy.check.mjs`; тесты:
`internal/services/ai_rollout*`, `internal/ai/aieval`,
`internal/handlers/ai_rollout_test.go`,
`internal/repository/ai_rollout_integration_test.go`; E2E:
`tests/readiness/ai-pilot.spec.ts`. Приёмка live-гейта:
[AI_PILOT_ACCEPTANCE_REPORT.md](AI_PILOT_ACCEPTANCE_REPORT.md).
