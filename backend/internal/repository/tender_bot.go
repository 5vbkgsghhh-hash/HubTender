package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TenderBotRepo — «Перечень тендеров» для Telegram-бота чата команды: чтение
// реестра, очередь напоминаний, запросы текста записи, запись в хронологию и
// offset long polling.
type TenderBotRepo struct {
	pool *pgxpool.Pool
}

func NewTenderBotRepo(pool *pgxpool.Pool) *TenderBotRepo {
	return &TenderBotRepo{pool: pool}
}

// Виды напоминаний.
const (
	ReminderKindCall      = "call"
	ReminderKindWaitingPD = "waiting_pd"
)

// ErrTenderRegistryNotFound — строки перечня нет (удалили).
var ErrTenderRegistryNotFound = errors.New("tender registry: строка не найдена")

// BotTender — строка перечня для бота. Даты сканируются в time.Time: сессия БД
// живёт в Europe/Moscow, а to_char(…'Z') подписал бы московское время как UTC.
type BotTender struct {
	ID                string
	Title             string
	ClientName        string
	TenderNumber      *string
	StatusName        *string
	DashboardStatus   *string
	ScopeName         *string
	ObjectAddress     *string
	IsArchived        bool
	Area              *float64
	TotalCost         *float64 // manual_total_cost, иначе cached_grand_total последней версии тендера
	SubmissionDate    *time.Time
	InvitationDate    *time.Time
	ConstructionStart *time.Time
	CommissionDate    *time.Time
	CreatedAt         *time.Time
	Chronology        []byte
	TenderPackage     []byte
}

// Стоимость КП — как на странице (useTenderData): ручная, иначе итог последней
// версии тендера с тем же номером.
const botTenderSelect = `
	SELECT tr.id::text, tr.title, tr.client_name, tr.tender_number, ts.name, tr.dashboard_status, cs.name,
	       tr.object_address, tr.is_archived, tr.area,
	       COALESCE(tr.manual_total_cost, (
	           SELECT t.cached_grand_total FROM public.tenders t
	           WHERE tr.tender_number <> '' AND t.tender_number = tr.tender_number
	           ORDER BY t.version DESC NULLS LAST
	           LIMIT 1)),
	       tr.submission_date, tr.invitation_date, tr.construction_start_date, tr.commission_date,
	       tr.created_at, tr.chronology_items, tr.tender_package_items
	FROM public.tender_registry tr
	LEFT JOIN public.tender_statuses ts ON ts.id = tr.status_id
	LEFT JOIN public.construction_scopes cs ON cs.id = tr.construction_scope_id`

func scanBotTender(row pgx.Row) (BotTender, error) {
	var t BotTender
	err := row.Scan(&t.ID, &t.Title, &t.ClientName, &t.TenderNumber, &t.StatusName, &t.DashboardStatus, &t.ScopeName,
		&t.ObjectAddress, &t.IsArchived, &t.Area, &t.TotalCost,
		&t.SubmissionDate, &t.InvitationDate, &t.ConstructionStart, &t.CommissionDate,
		&t.CreatedAt, &t.Chronology, &t.TenderPackage)
	return t, err
}

// ListTenders — весь перечень в порядке страницы.
func (r *TenderBotRepo) ListTenders(ctx context.Context) ([]BotTender, error) {
	rows, err := r.pool.Query(ctx, botTenderSelect+` ORDER BY tr.sort_order, tr.created_at`)
	if err != nil {
		return nil, fmt.Errorf("tenderBotRepo.ListTenders: %w", err)
	}
	defer rows.Close()
	var out []BotTender
	for rows.Next() {
		t, err := scanBotTender(rows)
		if err != nil {
			return nil, fmt.Errorf("tenderBotRepo.ListTenders: scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTender — одна строка перечня; ErrTenderRegistryNotFound, если её нет.
func (r *TenderBotRepo) GetTender(ctx context.Context, id string) (*BotTender, error) {
	t, err := scanBotTender(r.pool.QueryRow(ctx, botTenderSelect+` WHERE tr.id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTenderRegistryNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("tenderBotRepo.GetTender: %w", err)
	}
	return &t, nil
}

// ReminderKey — одно напоминание: чат × тендер × вид × дата срока (YYYY-MM-DD).
type ReminderKey struct {
	ChatID     int64
	RegistryID string
	Kind       string
	DueOn      string
}

// ClaimedReminder — напоминание, взятое на отправку.
type ClaimedReminder struct {
	ReminderKey
	Attempts int
}

// EnqueueReminders ставит напоминания в очередь. Уже известные (в любом статусе)
// пропускаются — так одно напоминание не уйдёт дважды. Возвращает число новых.
func (r *TenderBotRepo) EnqueueReminders(ctx context.Context, keys []ReminderKey) (int64, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	chats := make([]int64, len(keys))
	regs, kinds, dues := make([]string, len(keys)), make([]string, len(keys)), make([]string, len(keys))
	for i, k := range keys {
		chats[i], regs[i], kinds[i], dues[i] = k.ChatID, k.RegistryID, k.Kind, k.DueOn
	}
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO public.telegram_tender_reminders (chat_id, registry_id, kind, due_on)
		SELECT x.c, x.g::uuid, x.k, x.d::date
		FROM unnest($1::bigint[], $2::text[], $3::text[], $4::text[]) AS x(c, g, k, d)
		ON CONFLICT DO NOTHING`, chats, regs, kinds, dues)
	if err != nil {
		return 0, fmt.Errorf("tenderBotRepo.EnqueueReminders: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ClaimReminders берёт до limit ожидающих напоминаний и арендует их на lease:
// второй экземпляр бэкенда (или этот же после рестарта) их не возьмёт.
func (r *TenderBotRepo) ClaimReminders(ctx context.Context, limit int, lease time.Duration) ([]ClaimedReminder, error) {
	rows, err := r.pool.Query(ctx, `
		UPDATE public.telegram_tender_reminders t
		SET attempts = t.attempts + 1, claimed_until = now() + make_interval(secs => $2)
		FROM (
			SELECT chat_id, registry_id, kind, due_on
			FROM public.telegram_tender_reminders
			WHERE status = 'pending' AND (claimed_until IS NULL OR claimed_until <= now())
			ORDER BY created_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		) c
		WHERE t.chat_id = c.chat_id AND t.registry_id = c.registry_id AND t.kind = c.kind AND t.due_on = c.due_on
		RETURNING t.chat_id, t.registry_id::text, t.kind, to_char(t.due_on, 'YYYY-MM-DD'), t.attempts`,
		limit, lease.Seconds())
	if err != nil {
		return nil, fmt.Errorf("tenderBotRepo.ClaimReminders: %w", err)
	}
	defer rows.Close()
	var out []ClaimedReminder
	for rows.Next() {
		var c ClaimedReminder
		if err := rows.Scan(&c.ChatID, &c.RegistryID, &c.Kind, &c.DueOn, &c.Attempts); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const reminderKeyWhere = `chat_id = $1 AND registry_id = $2::uuid AND kind = $3 AND due_on = $4::date`

// FinishReminder закрывает напоминание: sent (с id сообщения), failed или skipped.
func (r *TenderBotRepo) FinishReminder(ctx context.Context, k ReminderKey, status string, messageID int64, reason string) error {
	if status != "sent" && status != "failed" && status != "skipped" {
		return fmt.Errorf("tenderBotRepo.FinishReminder: статус %q", status)
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE public.telegram_tender_reminders
		SET status = $5, telegram_message_id = NULLIF($6::bigint, 0), last_error = NULLIF($7, ''),
		    sent_at = CASE WHEN $5 = 'sent' THEN now() END, claimed_until = NULL
		WHERE `+reminderKeyWhere, k.ChatID, k.RegistryID, k.Kind, k.DueOn, status, messageID, reason)
	if err != nil {
		return fmt.Errorf("tenderBotRepo.FinishReminder: %w", err)
	}
	return nil
}

// DeferReminder — временная ошибка: следующая попытка не раньше чем через delay.
func (r *TenderBotRepo) DeferReminder(ctx context.Context, k ReminderKey, reason string, delay time.Duration) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE public.telegram_tender_reminders
		SET last_error = $5, claimed_until = now() + make_interval(secs => $6)
		WHERE `+reminderKeyWhere, k.ChatID, k.RegistryID, k.Kind, k.DueOn, reason, delay.Seconds())
	if err != nil {
		return fmt.Errorf("tenderBotRepo.DeferReminder: %w", err)
	}
	return nil
}

// PruneReminders удаляет закрытые напоминания старше keep и истёкшие запросы записи.
func (r *TenderBotRepo) PruneReminders(ctx context.Context, keep time.Duration) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM public.telegram_tender_reminders
		WHERE status <> 'pending' AND created_at < now() - make_interval(secs => $1)`, keep.Seconds())
	if err != nil {
		return 0, fmt.Errorf("tenderBotRepo.PruneReminders: %w", err)
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM public.telegram_tender_prompts WHERE expires_at < now()`); err != nil {
		return 0, fmt.Errorf("tenderBotRepo.PruneReminders: prompts: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ChronologyEntry — запись хронологии в формате страницы (src/lib/types/types/tenders.ts).
type ChronologyEntry struct {
	Date string `json:"date"`
	Text string `json:"text"`
	Type string `json:"type"`
}

// appendChronology дописывает запись в конец chronology_items одной командой,
// не читая массив: параллельные записи бота и страницы не теряются. Не-массив
// (jsonb null у старых строк) заменяется пустым массивом. Возвращает название.
func appendChronology(ctx context.Context, q Querier, registryID string, e ChronologyEntry) (string, error) {
	item, err := json.Marshal(e)
	if err != nil {
		return "", fmt.Errorf("appendChronology: marshal: %w", err)
	}
	var title string
	err = q.QueryRow(ctx, `
		UPDATE public.tender_registry
		SET chronology_items = (CASE WHEN jsonb_typeof(chronology_items) = 'array'
		                             THEN chronology_items ELSE '[]'::jsonb END) || jsonb_build_array($2::jsonb)
		WHERE id = $1::uuid
		RETURNING title`, registryID, string(item)).Scan(&title)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrTenderRegistryNotFound
	}
	if err != nil {
		return "", fmt.Errorf("appendChronology: %w", err)
	}
	return title, nil
}
