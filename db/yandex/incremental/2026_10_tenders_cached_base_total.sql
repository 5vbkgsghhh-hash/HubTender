-- =============================================================================
-- 2026_10_tenders_cached_base_total.sql — хранимая сумма прямых затрат тендера.
--
-- SCOPE: добавляет public.tenders.cached_base_total = SUM(boq_items.total_amount)
-- и заполняет её для существующих тендеров.
--
-- ЗАЧЕМ: GET /api/v1/tenders считал base_total коррелированным подзапросом по
-- boq_items для каждого тендера (~700 тыс. строк на один вызов, 1,3–3 с). Список
-- зовут все страницы и на каждое realtime-событие `tenders`, поэтому серия
-- записей в ВОР забивала CPU базы копиями этого запроса (13.10.2026 API лежал
-- 14 минут). Теперь список читает колонку, а пишет её единственный writer
-- итогов тендера — Go helper RecalculateTenderGrandTotalTx
-- (backend/internal/repository/tender_recalc.go), в том же UPDATE, что и
-- cached_grand_total.
--
-- Каноничный аналог — колонка cached_base_total в db/yandex/sql/03_tables.sql.
--
-- ИДЕМПОТЕНТНО И ПЕРЕЗАПУСКАЕМО: ADD COLUMN IF NOT EXISTS; блок заполнения
-- обновляет только строки, где значение расходится с живой суммой.
--
-- ПОРЯДОК ВЫКАТКИ: миграция → бэкенд → повторный прогон этого файла (закрывает
-- правки ВОР, сделанные старым бэкендом между первыми двумя шагами).
-- Применяется к production только после явного подтверждения пользователя.
--
-- БЛОКИРОВКИ: суммы считаются во временную таблицу до блокировки tenders;
-- UPDATE ~200 строк короткий. Триггеры updated_at и realtime-уведомления на
-- время заполнения выключены: иначе все тендеры получили бы updated_at = now()
-- и все клиенты разом перезапросили бы список.
-- =============================================================================

ALTER TABLE public.tenders
    ADD COLUMN IF NOT EXISTS cached_base_total numeric NOT NULL DEFAULT 0;

COMMENT ON COLUMN public.tenders.cached_base_total IS
    'Прямые затраты тендера: SUM(boq_items.total_amount). Пишет только Go helper RecalculateTenderGrandTotalTx; читает список тендеров (base_total).';

BEGIN;

CREATE TEMP TABLE _tender_base_total ON COMMIT DROP AS
    SELECT tender_id, SUM(total_amount) AS s
    FROM public.boq_items
    GROUP BY tender_id;

ALTER TABLE public.tenders DISABLE TRIGGER update_tenders_updated_at;
ALTER TABLE public.tenders DISABLE TRIGGER trg_notify_row_change_tenders;

UPDATE public.tenders t
   SET cached_base_total = COALESCE(b.s, 0)
  FROM public.tenders t2
  LEFT JOIN _tender_base_total b ON b.tender_id = t2.id
 WHERE t2.id = t.id
   AND t.cached_base_total IS DISTINCT FROM COALESCE(b.s, 0);

ALTER TABLE public.tenders ENABLE TRIGGER update_tenders_updated_at;
ALTER TABLE public.tenders ENABLE TRIGGER trg_notify_row_change_tenders;

COMMIT;
