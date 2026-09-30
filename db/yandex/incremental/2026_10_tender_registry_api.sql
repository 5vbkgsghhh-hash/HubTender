-- «Перечень тендеров» по машинному ключу для внешнего Telegram-бота
-- (@SU10TenderNotes_BOT, api/REGISTRY.md) вместо встроенного бота @TendersIN_bot.
--
--   registry:read  — GET /api/v1/tender-registry/search, /tender-registry/{id}/card;
--   registry:write — POST /api/v1/tender-registry/reminders/claim (выдача
--                    положенных напоминаний) и /tender-registry/{id}/chronology.
--
-- Что меняется:
--   * telegram_tender_prompts и telegram_tender_bot_state (из
--     2026_10_telegram_tender_bot.sql) удаляются — встроенный бот убран, таблицы
--     пустые;
--   * telegram_tender_reminders остаётся журналом выданных через API
--     напоминаний (чат × тендер × вид × дата срока); строки не в статусе sent —
--     остатки очереди встроенного бота — чистятся;
--   * CHECK api_keys_scopes_chk перечисляет области явно — без этой миграции
--     ключ с registry:* не вставится в api_keys.
--
-- Инварианты:
--   * ключ, ограниченный списком тендеров, к перечню не допускается: у
--     tender_registry нет связи с tenders (гейт маршрута отвечает 403);
--   * миграция идемпотентна.
--
-- ВНИМАНИЕ: НЕ применять к production вручную из кода. Применяет пользователь.
-- Порядок выкатки: миграция → бэкенд → фронтенд.

BEGIN;

DROP TABLE IF EXISTS public.telegram_tender_prompts;
DROP TABLE IF EXISTS public.telegram_tender_bot_state;

DELETE FROM public.telegram_tender_reminders WHERE status <> 'sent';

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'api_keys_scopes_chk') THEN
        ALTER TABLE public.api_keys DROP CONSTRAINT api_keys_scopes_chk;
    END IF;

    ALTER TABLE public.api_keys
        ADD CONSTRAINT api_keys_scopes_chk
        CHECK (cardinality(scopes) > 0
               AND scopes <@ ARRAY['archive:read', 'archive:write', 'tenders:read', 'tenders:write',
                                   'verification:read', 'verification:write',
                                   'registry:read', 'registry:write']::text[]);
END $$;

COMMIT;
