package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Бот «Перечня тендеров»: запись в хронологию, очередь напоминаний, запросы записи.
//
//	HUBTENDER_TEST_DATABASE_URL=… go test ./internal/repository/ -run TenderBotIntegration -v

var botChatSeq atomic.Int64

// botChat — уникальный chat_id (telegram_links.chat_id UNIQUE).
func botChat() int64 { return time.Now().UnixNano()%1_000_000_000_000 + botChatSeq.Add(1) }

func botRegistryRow(t *testing.T, pool *pgxpool.Pool, title string, chronology *string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO public.tender_registry (title, client_name, sort_order, chronology_items)
		VALUES ($1, 'Заказчик itest', 1, $2::jsonb) RETURNING id::text`, title, chronology).Scan(&id); err != nil {
		t.Fatalf("registry: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.tender_registry WHERE id = $1`, id)
	})
	return id
}

func botChronology(t *testing.T, pool *pgxpool.Pool, id string) []ChronologyEntry {
	t.Helper()
	var raw []byte
	if err := pool.QueryRow(context.Background(),
		`SELECT chronology_items FROM public.tender_registry WHERE id = $1`, id).Scan(&raw); err != nil {
		t.Fatalf("chronology: %v", err)
	}
	var items []ChronologyEntry
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatalf("chronology %s: %v", raw, err)
	}
	return items
}

func TestTenderBotIntegration_AppendChronology(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	entry := ChronologyEntry{Date: "2026-09-30T07:00:00.000Z", Text: "Позвонили", Type: "call_follow_up"}

	for _, c := range []struct {
		name   string
		init   *string
		before int
	}{
		{"NULL", nil, 0},
		{"jsonb null", strPtr("null"), 0},
		{"объект", strPtr(`{"a":1}`), 0},
		{"пустой массив", strPtr("[]"), 0},
		{"старые записи", strPtr(`[{"date":null,"text":"старое"}]`), 1},
	} {
		id := botRegistryRow(t, pool, "itest append "+c.name, c.init)
		title, err := appendChronology(ctx, pool, id, entry)
		if err != nil || title != "itest append "+c.name {
			t.Fatalf("%s: %q %v", c.name, title, err)
		}
		items := botChronology(t, pool, id)
		if len(items) != c.before+1 || items[len(items)-1] != entry {
			t.Fatalf("%s: %+v", c.name, items)
		}
	}

	id := botRegistryRow(t, pool, "itest append parallel", strPtr("[]"))
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := appendChronology(ctx, pool, id, ChronologyEntry{Date: entry.Date, Text: fmt.Sprint(i), Type: "default"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if items := botChronology(t, pool, id); len(items) != 10 {
		t.Fatalf("параллельные записи потерялись: %d из 10", len(items))
	}

	if _, err := appendChronology(ctx, pool, "00000000-0000-0000-0000-000000000000", entry); !errors.Is(err, ErrTenderRegistryNotFound) {
		t.Fatalf("нет строки: %v", err)
	}
}

func TestTenderBotIntegration_RemindersQueue(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewTenderBotRepo(pool)
	chat := -botChat()
	reg := botRegistryRow(t, pool, "itest queue", strPtr("[]"))

	call := ReminderKey{ChatID: chat, RegistryID: reg, Kind: ReminderKindCall, DueOn: "2026-09-30"}
	pd := ReminderKey{ChatID: chat, RegistryID: reg, Kind: ReminderKindWaitingPD, DueOn: "2026-09-15"}
	if n, err := repo.EnqueueReminders(ctx, []ReminderKey{call, pd}); err != nil || n != 2 {
		t.Fatalf("enqueue: %d %v", n, err)
	}
	if n, err := repo.EnqueueReminders(ctx, []ReminderKey{call, pd}); err != nil || n != 0 {
		t.Fatalf("повторная постановка не должна дублировать: %d %v", n, err)
	}

	mine := func(claimed []ClaimedReminder) map[ReminderKey]int {
		out := map[ReminderKey]int{}
		for _, c := range claimed {
			if c.RegistryID == reg {
				out[c.ReminderKey] = c.Attempts
			}
		}
		return out
	}
	claimed, err := repo.ClaimReminders(ctx, 100, time.Minute)
	if got := mine(claimed); err != nil || len(got) != 2 || got[call] != 1 || got[pd] != 1 {
		t.Fatalf("claim: %+v %v", got, err)
	}
	if claimed, _ := repo.ClaimReminders(ctx, 100, time.Minute); len(mine(claimed)) != 0 {
		t.Fatal("арендованные напоминания взяты повторно")
	}

	if err := repo.FinishReminder(ctx, call, "sent", 42, ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeferReminder(ctx, pd, "сеть", 0); err != nil {
		t.Fatal(err)
	}
	claimed, _ = repo.ClaimReminders(ctx, 100, time.Minute)
	if got := mine(claimed); len(got) != 1 || got[pd] != 2 {
		t.Fatalf("после отсрочки берётся только ПД, попытка 2: %+v", got)
	}
	if err := repo.FinishReminder(ctx, pd, "pending", 0, ""); err == nil {
		t.Fatal("недопустимый статус принят")
	}

	var status string
	var msgID *int64
	if err := pool.QueryRow(ctx, `
		SELECT status, telegram_message_id FROM public.telegram_tender_reminders
		WHERE chat_id = $1 AND registry_id = $2 AND kind = 'call'`, chat, reg).Scan(&status, &msgID); err != nil ||
		status != "sent" || msgID == nil || *msgID != 42 {
		t.Fatalf("итог отправки: %q %v %v", status, msgID, err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM public.tender_registry WHERE id = $1`, reg); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.telegram_tender_reminders WHERE registry_id = $1`, reg).
		Scan(&left); err != nil || left != 0 {
		t.Fatalf("напоминания удалённого тендера остались: %d %v", left, err)
	}
}

func TestTenderBotIntegration_Prompts(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewTenderBotRepo(pool)
	chat := -botChat()
	reg := botRegistryRow(t, pool, "itest prompt", strPtr("null"))
	const date = "2026-09-30T07:00:00.000Z"

	if err := repo.InsertPrompt(ctx, chat, 10, reg, "call_follow_up", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := repo.InsertPrompt(ctx, chat, 11, reg, "default", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ConsumePrompt(ctx, chat, 99, "x", date); !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("ответ не на запрос: %v", err)
	}
	if _, err := repo.ConsumePrompt(ctx, -chat, 10, "x", date); !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("запрос из другого чата: %v", err)
	}
	res, err := repo.ConsumePrompt(ctx, chat, 10, "Позвонили, ждут ПД", date)
	if err != nil || res.Title != "itest prompt" || res.EntryType != "call_follow_up" {
		t.Fatalf("запись: %+v %v", res, err)
	}
	if _, err := repo.ConsumePrompt(ctx, chat, 10, "x", date); !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("второй ответ записал второй раз: %v", err)
	}
	if res, err := repo.ConsumePrompt(ctx, chat, 11, "Прислали ПД", date); err != nil || res.EntryType != "default" {
		t.Fatalf("второй запрос того же чата: %+v %v", res, err)
	}
	items := botChronology(t, pool, reg)
	if len(items) != 2 || items[0].Text != "Позвонили, ждут ПД" || items[0].Type != "call_follow_up" || items[1].Type != "default" {
		t.Fatalf("хронология: %+v", items)
	}

	if err := repo.InsertPrompt(ctx, chat, 20, reg, "default", -time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ConsumePrompt(ctx, chat, 20, "x", date); !errors.Is(err, ErrNoPrompt) {
		t.Fatalf("истёкший запрос: %v", err)
	}
	if _, err := repo.PruneReminders(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.telegram_tender_prompts WHERE chat_id = $1`, chat).
		Scan(&left); err != nil || left != 0 {
		t.Fatalf("истёкший запрос не вычищен: %d %v", left, err)
	}
}

func TestTenderBotIntegration_Offset(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewTenderBotRepo(pool)
	if err := repo.SetOffset(ctx, 123); err != nil {
		t.Fatal(err)
	}
	if off, err := repo.GetOffset(ctx); err != nil || off != 123 {
		t.Fatalf("offset: %d %v", off, err)
	}
	if err := repo.SetOffset(ctx, 124); err != nil {
		t.Fatal(err)
	}
	if off, _ := repo.GetOffset(ctx); off != 124 {
		t.Fatalf("offset не обновился: %d", off)
	}
}

func TestTenderBotIntegration_ReadTender(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewTenderBotRepo(pool)
	reg := botRegistryRow(t, pool, "itest read", strPtr(`[{"date":"2026-09-20T09:00:00.000Z","text":"x"}]`))
	var statusID string
	if err := pool.QueryRow(ctx, `INSERT INTO public.tender_statuses (name) VALUES ('Направлено itest ' || gen_random_uuid())
		RETURNING id::text`).Scan(&statusID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.tender_statuses WHERE id = $1`, statusID)
	})
	if _, err := pool.Exec(ctx, `
		UPDATE public.tender_registry
		SET status_id = $2, tender_number = 'itest-' || gen_random_uuid(), manual_total_cost = 123.45, area = 10.5,
		    submission_date = '2026-09-22 12:00:00+03', dashboard_status = 'sent'
		WHERE id = $1`, reg, statusID); err != nil {
		t.Fatal(err)
	}

	got, err := repo.GetTender(ctx, reg)
	if err != nil {
		t.Fatal(err)
	}
	wantSubmission := time.Date(2026, 9, 22, 9, 0, 0, 0, time.UTC)
	if got.StatusName == nil || got.DashboardStatus == nil || *got.DashboardStatus != "sent" || got.TotalCost == nil ||
		*got.TotalCost != 123.45 || got.Area == nil || *got.Area != 10.5 || got.SubmissionDate == nil ||
		!got.SubmissionDate.Equal(wantSubmission) || got.CreatedAt == nil || len(got.Chronology) == 0 {
		t.Fatalf("строка перечня: %+v", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.tender_registry SET manual_total_cost = NULL WHERE id = $1`, reg); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.GetTender(ctx, reg); err != nil || got.TotalCost != nil {
		t.Fatalf("без ручной стоимости и без тендера в системе стоимость пустая: %+v %v", got, err)
	}
	if _, err := repo.GetTender(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrTenderRegistryNotFound) {
		t.Fatalf("нет строки: %v", err)
	}
	all, err := repo.ListTenders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tr := range all {
		found = found || tr.ID == reg
	}
	if !found {
		t.Fatal("строки нет в перечне")
	}
}
