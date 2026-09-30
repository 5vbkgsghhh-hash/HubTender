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

// TenderRegistryBotRepo — «Перечень тендеров» для внешнего Telegram-бота по
// машинному ключу (api/REGISTRY.md): чтение реестра, журнал выданных
// напоминаний и запись в хронологию.
type TenderRegistryBotRepo struct {
	pool *pgxpool.Pool
}

func NewTenderRegistryBotRepo(pool *pgxpool.Pool) *TenderRegistryBotRepo {
	return &TenderRegistryBotRepo{pool: pool}
}

// Виды напоминаний.
const (
	ReminderKindCall      = "call"
	ReminderKindWaitingPD = "waiting_pd"
)

// ErrTenderRegistryNotFound — строки перечня нет (удалили).
var ErrTenderRegistryNotFound = errors.New("tender registry: строка не найдена")

// RegistryTender — строка перечня. Даты сканируются в time.Time: сессия БД
// живёт в Europe/Moscow, а to_char(…'Z') подписал бы московское время как UTC.
type RegistryTender struct {
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
const registryTenderSelect = `
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

func scanRegistryTender(row pgx.Row) (RegistryTender, error) {
	var t RegistryTender
	err := row.Scan(&t.ID, &t.Title, &t.ClientName, &t.TenderNumber, &t.StatusName, &t.DashboardStatus, &t.ScopeName,
		&t.ObjectAddress, &t.IsArchived, &t.Area, &t.TotalCost,
		&t.SubmissionDate, &t.InvitationDate, &t.ConstructionStart, &t.CommissionDate,
		&t.CreatedAt, &t.Chronology, &t.TenderPackage)
	return t, err
}

// ListTenders — весь перечень в порядке страницы.
func (r *TenderRegistryBotRepo) ListTenders(ctx context.Context) ([]RegistryTender, error) {
	rows, err := r.pool.Query(ctx, registryTenderSelect+` ORDER BY tr.sort_order, tr.created_at`)
	if err != nil {
		return nil, fmt.Errorf("tenderRegistryBotRepo.ListTenders: %w", err)
	}
	defer rows.Close()
	var out []RegistryTender
	for rows.Next() {
		t, err := scanRegistryTender(rows)
		if err != nil {
			return nil, fmt.Errorf("tenderRegistryBotRepo.ListTenders: scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTender — одна строка перечня; ErrTenderRegistryNotFound, если её нет.
func (r *TenderRegistryBotRepo) GetTender(ctx context.Context, id string) (*RegistryTender, error) {
	t, err := scanRegistryTender(r.pool.QueryRow(ctx, registryTenderSelect+` WHERE tr.id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTenderRegistryNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("tenderRegistryBotRepo.GetTender: %w", err)
	}
	return &t, nil
}

// ReminderKey — напоминание: тендер × вид × дата срока (YYYY-MM-DD).
type ReminderKey struct {
	RegistryID string
	Kind       string
	DueOn      string
}

// ClaimReminders записывает напоминания как выданные в чат chatID и
// возвращает только новые — каждое напоминание выдаётся чату не больше одного
// раза, в том числе при параллельных вызовах. Строки перечня, удалённые
// между расчётом и записью, пропускаются. Заодно чистит журнал старше pruneBefore.
func (r *TenderRegistryBotRepo) ClaimReminders(ctx context.Context, chatID int64, keys []ReminderKey,
	pruneBefore string) ([]ReminderKey, error) {
	if _, err := r.pool.Exec(ctx, `
		DELETE FROM public.telegram_tender_reminders WHERE due_on < $1::date`, pruneBefore); err != nil {
		return nil, fmt.Errorf("tenderRegistryBotRepo.ClaimReminders: prune: %w", err)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	regs, kinds, dues := make([]string, len(keys)), make([]string, len(keys)), make([]string, len(keys))
	for i, k := range keys {
		regs[i], kinds[i], dues[i] = k.RegistryID, k.Kind, k.DueOn
	}
	rows, err := r.pool.Query(ctx, `
		INSERT INTO public.telegram_tender_reminders
		       (chat_id, registry_id, kind, due_on, status, attempts, sent_at)
		SELECT $1, tr.id, x.k, x.d::date, 'sent', 1, now()
		FROM unnest($2::text[], $3::text[], $4::text[]) AS x(g, k, d)
		JOIN public.tender_registry tr ON tr.id = x.g::uuid
		ON CONFLICT DO NOTHING
		RETURNING registry_id::text, kind, to_char(due_on, 'YYYY-MM-DD')`, chatID, regs, kinds, dues)
	if err != nil {
		return nil, fmt.Errorf("tenderRegistryBotRepo.ClaimReminders: %w", err)
	}
	defer rows.Close()
	var out []ReminderKey
	for rows.Next() {
		var k ReminderKey
		if err := rows.Scan(&k.RegistryID, &k.Kind, &k.DueOn); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// ChronologyEntry — запись хронологии в формате страницы (src/lib/types/types/tenders.ts).
type ChronologyEntry struct {
	Date string `json:"date"`
	Text string `json:"text"`
	Type string `json:"type"`
}

// chronologyArray — chronology_items как массив: jsonb null и объект у старых
// строк считаются пустым массивом.
const chronologyArray = `(CASE WHEN jsonb_typeof(chronology_items) = 'array' THEN chronology_items ELSE '[]'::jsonb END)`

// AppendChronology дописывает запись в конец chronology_items одной командой,
// не читая массив: параллельные записи бота и страницы не теряются. Повтор той
// же записи ({date, text, type} уже в массиве) ничего не добавляет —
// created=false; так бот может безопасно повторить запрос.
func (r *TenderRegistryBotRepo) AppendChronology(ctx context.Context, registryID string, e ChronologyEntry) (string, bool, error) {
	item, err := json.Marshal(e)
	if err != nil {
		return "", false, fmt.Errorf("tenderRegistryBotRepo.AppendChronology: marshal: %w", err)
	}
	var title string
	err = r.pool.QueryRow(ctx, `
		UPDATE public.tender_registry
		SET chronology_items = `+chronologyArray+` || jsonb_build_array($2::jsonb)
		WHERE id = $1::uuid AND NOT (`+chronologyArray+` @> jsonb_build_array($2::jsonb))
		RETURNING title`, registryID, string(item)).Scan(&title)
	if err == nil {
		return title, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("tenderRegistryBotRepo.AppendChronology: %w", err)
	}
	err = r.pool.QueryRow(ctx, `SELECT title FROM public.tender_registry WHERE id = $1::uuid`, registryID).Scan(&title)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, ErrTenderRegistryNotFound
	}
	if err != nil {
		return "", false, fmt.Errorf("tenderRegistryBotRepo.AppendChronology: title: %w", err)
	}
	return title, false, nil
}
