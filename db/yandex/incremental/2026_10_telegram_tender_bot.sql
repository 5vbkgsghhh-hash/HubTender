-- Telegram-бот «Перечня тендеров» для чата команды: напоминания, карточка
-- тендера по запросу и запись звонка/события в хронологию прямо из чата.
--
-- Отдельный бот (TENDER_BOT_TOKEN), не бот замечаний проверки. Работает только
-- в одном групповом чате (TENDER_BOT_CHAT_ID), в личных сообщениях не отвечает.
--   · «Направлено» — напоминание каждый день, пока с подачи КП или последнего
--     звонка прошло больше 7 дней;
--   · «Ожидание ПД» — раз в месяц от дня последней записи хронологии.
-- Кнопки «Звонок» / «Событие» → ответ на сообщение бота → запись в
-- tender_registry.chronology_items.
--
-- telegram_tender_reminders — журнал и очередь напоминаний: чат × тендер × вид ×
--                             дата срока. PK не даёт отправить одно напоминание
--                             дважды, в том числе при двух экземплярах бэкенда.
--                             Текст не хранится — собирается при отправке.
-- telegram_tender_prompts   — запросы текста записи: сообщение бота, на которое
--                             нужно ответить; живут сутки.
-- telegram_tender_bot_state — offset long polling getUpdates этого бота (одна строка).
--
-- Первые внешние ключи на tender_registry (ON DELETE CASCADE): удаление строки
-- перечня удаляет её напоминания и незавершённые запросы.
--
-- Идемпотентно: повторный запуск безопасен.
--
-- ВНИМАНИЕ: НЕ применять к production вручную из кода. Применяет пользователь.
-- Порядок выкатки: миграция → бэкенд → фронтенд.

BEGIN;

CREATE TABLE IF NOT EXISTS public.telegram_tender_reminders (
    chat_id             bigint      NOT NULL,
    registry_id         uuid        NOT NULL,
    kind                text        NOT NULL,
    due_on              date        NOT NULL,
    status              text        NOT NULL DEFAULT 'pending',
    attempts            integer     NOT NULL DEFAULT 0,
    claimed_until       timestamptz,
    last_error          text,
    telegram_message_id bigint,
    created_at          timestamptz NOT NULL DEFAULT now(),
    sent_at             timestamptz,
    CONSTRAINT telegram_tender_reminders_pkey
        PRIMARY KEY (chat_id, registry_id, kind, due_on),
    CONSTRAINT telegram_tender_reminders_kind_check
        CHECK (kind IN ('call', 'waiting_pd')),
    CONSTRAINT telegram_tender_reminders_status_check
        CHECK (status IN ('pending', 'sent', 'failed', 'skipped')),
    CONSTRAINT telegram_tender_reminders_registry_fkey
        FOREIGN KEY (registry_id) REFERENCES public.tender_registry(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS telegram_tender_reminders_pending_idx
    ON public.telegram_tender_reminders (created_at)
    WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS telegram_tender_reminders_registry_idx
    ON public.telegram_tender_reminders (registry_id);

CREATE TABLE IF NOT EXISTS public.telegram_tender_prompts (
    chat_id           bigint      NOT NULL,
    prompt_message_id bigint      NOT NULL,
    registry_id       uuid        NOT NULL,
    entry_type        text        NOT NULL,
    expires_at        timestamptz NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT telegram_tender_prompts_pkey PRIMARY KEY (chat_id, prompt_message_id),
    CONSTRAINT telegram_tender_prompts_entry_type_check
        CHECK (entry_type IN ('call_follow_up', 'default')),
    CONSTRAINT telegram_tender_prompts_registry_fkey
        FOREIGN KEY (registry_id) REFERENCES public.tender_registry(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS public.telegram_tender_bot_state (
    id            smallint    NOT NULL DEFAULT 1,
    update_offset bigint      NOT NULL DEFAULT 0,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT telegram_tender_bot_state_pkey PRIMARY KEY (id),
    CONSTRAINT telegram_tender_bot_state_single_check CHECK (id = 1)
);

COMMIT;
