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

// «Перечень тендеров» для внешнего бота: запись в хронологию, выдача напоминаний, чтение.
//
//	HUBTENDER_TEST_DATABASE_URL=… go test ./internal/repository/ -run TenderRegistryBotIntegration -v

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

func TestTenderRegistryBotIntegration_AppendChronology(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewTenderRegistryBotRepo(pool)
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
		title, created, err := repo.AppendChronology(ctx, id, entry)
		if err != nil || !created || title != "itest append "+c.name {
			t.Fatalf("%s: %q %v %v", c.name, title, created, err)
		}
		items := botChronology(t, pool, id)
		if len(items) != c.before+1 || items[len(items)-1] != entry {
			t.Fatalf("%s: %+v", c.name, items)
		}
		// Повтор того же запроса второй записи не даёт.
		if title, created, err := repo.AppendChronology(ctx, id, entry); err != nil || created || title == "" {
			t.Fatalf("%s: повтор: %q %v %v", c.name, title, created, err)
		}
		if items := botChronology(t, pool, id); len(items) != c.before+1 {
			t.Fatalf("%s: повтор задвоил запись: %+v", c.name, items)
		}
	}

	// Одинаковые параллельные запросы — одна запись, разные — все.
	same := botRegistryRow(t, pool, "itest append same", strPtr("[]"))
	diff := botRegistryRow(t, pool, "itest append diff", strPtr("[]"))
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, _, err := repo.AppendChronology(ctx, same, entry); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, _, err := repo.AppendChronology(ctx, diff, ChronologyEntry{Date: entry.Date, Text: fmt.Sprint(i), Type: "default"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := len(botChronology(t, pool, same)); n != 1 {
		t.Fatalf("одинаковые параллельные запросы: %d записей вместо 1", n)
	}
	if n := len(botChronology(t, pool, diff)); n != 10 {
		t.Fatalf("разные параллельные запросы: %d записей вместо 10", n)
	}

	if _, _, err := repo.AppendChronology(ctx, "00000000-0000-0000-0000-000000000000", entry); !errors.Is(err, ErrTenderRegistryNotFound) {
		t.Fatalf("нет строки: %v", err)
	}
}

func TestTenderRegistryBotIntegration_ClaimReminders(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewTenderRegistryBotRepo(pool)
	chat := -botChat()
	reg := botRegistryRow(t, pool, "itest claim", strPtr("[]"))
	gone := botRegistryRow(t, pool, "itest claim gone", strPtr("[]"))
	if _, err := pool.Exec(ctx, `DELETE FROM public.tender_registry WHERE id = $1`, gone); err != nil {
		t.Fatal(err)
	}

	call := ReminderKey{RegistryID: reg, Kind: ReminderKindCall, DueOn: "2026-09-30"}
	pd := ReminderKey{RegistryID: reg, Kind: ReminderKindWaitingPD, DueOn: "2026-09-15"}
	deleted := ReminderKey{RegistryID: gone, Kind: ReminderKindCall, DueOn: "2026-09-30"}
	got, err := repo.ClaimReminders(ctx, chat, []ReminderKey{call, pd, deleted}, "2026-07-01")
	if err != nil || len(got) != 2 {
		t.Fatalf("выдача: %+v %v", got, err)
	}
	if again, err := repo.ClaimReminders(ctx, chat, []ReminderKey{call, pd}, "2026-07-01"); err != nil || len(again) != 0 {
		t.Fatalf("повторная выдача тому же чату: %+v %v", again, err)
	}
	if other, err := repo.ClaimReminders(ctx, chat-1, []ReminderKey{call}, "2026-07-01"); err != nil || len(other) != 1 || other[0] != call {
		t.Fatalf("другой чат: %+v %v", other, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.telegram_tender_reminders WHERE chat_id IN ($1, $2)`, chat, chat-1)
	})

	var status string
	var attempts int
	var sent bool
	if err := pool.QueryRow(ctx, `
		SELECT status, attempts, sent_at IS NOT NULL FROM public.telegram_tender_reminders
		WHERE chat_id = $1 AND registry_id = $2 AND kind = 'call'`, chat, reg).Scan(&status, &attempts, &sent); err != nil ||
		status != "sent" || attempts != 1 || !sent {
		t.Fatalf("строка журнала: %q %d %v %v", status, attempts, sent, err)
	}

	// Очистка: всё со сроком раньше pruneBefore уходит, в том числе из других чатов.
	if _, err := repo.ClaimReminders(ctx, chat, nil, "2026-09-20"); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.telegram_tender_reminders WHERE chat_id = $1`, chat).
		Scan(&left); err != nil || left != 1 {
		t.Fatalf("после очистки должен остаться только звонок от 30.09: %d %v", left, err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM public.tender_registry WHERE id = $1`, reg); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.telegram_tender_reminders WHERE registry_id = $1`, reg).
		Scan(&left); err != nil || left != 0 {
		t.Fatalf("журнал удалённого тендера остался: %d %v", left, err)
	}
}

func TestTenderRegistryBotIntegration_ReadTender(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	repo := NewTenderRegistryBotRepo(pool)
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
