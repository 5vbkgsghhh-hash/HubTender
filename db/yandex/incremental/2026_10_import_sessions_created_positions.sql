-- =============================================================================
-- 2026_10_import_sessions_created_positions.sql — ДОП из импорта Excel.
--
-- SCOPE: добавляет public.import_sessions.created_positions (jsonb) — список
-- ДОП-позиций, созданных строками «доп» при импорте работ/материалов:
--   [{"id": "<uuid>", "parent_position_id": "<uuid>",
--     "position_number": 5.1, "work_name": "..."}]
-- Отмена импорта (POST /api/v1/import-sessions/{id}/cancel) удаляет эти ДОП,
-- если в них не осталось элементов; журнал импортов показывает их число.
-- У старых сессий — '[]' (ДОП они не создавали).
--
-- Каноничный аналог — колонка created_positions в db/yandex/sql/03_tables.sql.
-- Идемпотентно: ADD COLUMN IF NOT EXISTS.
--
-- ВНИМАНИЕ: НЕ применять к production вручную из кода. Применяет пользователь.
-- Порядок выкатки: миграция → бэкенд → фронтенд.
-- =============================================================================

BEGIN;

ALTER TABLE public.import_sessions
    ADD COLUMN IF NOT EXISTS created_positions jsonb NOT NULL DEFAULT '[]'::jsonb;

COMMENT ON COLUMN public.import_sessions.created_positions IS
    'ДОП-позиции, созданные строками «доп» импорта: [{id, parent_position_id, position_number, work_name}]. Удаляются при отмене импорта, если пусты.';

COMMIT;
