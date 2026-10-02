package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgreSQL integration tests: строки «доп» импорта Excel (новые ДОП-позиции
// в транзакции импорта) и отмена такого импорта из журнала. Reuses
// newTestPool / seedRollbackFixture (COMPILED + SKIPPED without a test DB).
//
//	HUBTENDER_TEST_DATABASE_URL='postgres://…/hubtender_test?sslmode=disable' \
//	  go test ./internal/repository/ -run 'AdditionalImport' -v

func dopSpec(tempID, parentID, name string, row int) ImportAdditionalPosition {
	return ImportAdditionalPosition{
		RowIndex:         iptr(row),
		TempID:           tempID,
		ParentPositionID: parentID,
		WorkName:         name,
		UnitCode:         sptr("м2"),
		ManualVolume:     fptr(12),
		ManualNote:       sptr("примечание ДОП"),
	}
}

func dopItem(t *testing.T, pool *pgxpool.Pool, tempID, itemType string, qty, rate float64) ImportBoqItem {
	t.Helper()
	item := importItem(t, pool, "", itemType, qty, rate, nil, nil)
	item.ClientPositionTempID = sptr(tempID)
	return item
}

type dopRow struct {
	id            string
	number        float64
	name          string
	level         *int
	manualVolume  *float64
	totalWorks    float64
	totalMaterial float64
}

func loadDops(t *testing.T, pool *pgxpool.Pool, parentID string) []dopRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT id::text, position_number, work_name, hierarchy_level, manual_volume,
		       COALESCE(total_works, 0), COALESCE(total_material, 0)
		FROM public.client_positions
		WHERE parent_position_id = $1::uuid AND is_additional = true
		ORDER BY position_number`, parentID)
	if err != nil {
		t.Fatalf("load dops: %v", err)
	}
	defer rows.Close()
	var out []dopRow
	for rows.Next() {
		var d dopRow
		if err := rows.Scan(&d.id, &d.number, &d.name, &d.level, &d.manualVolume, &d.totalWorks, &d.totalMaterial); err != nil {
			t.Fatalf("scan dop: %v", err)
		}
		out = append(out, d)
	}
	return out
}

func countPositionItems(t *testing.T, pool *pgxpool.Pool, positionID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM public.boq_items WHERE client_position_id = $1::uuid`, positionID).Scan(&n); err != nil {
		t.Fatalf("count items: %v", err)
	}
	return n
}

func wantBulkImportError(t *testing.T, err error, substr string) {
	t.Helper()
	var be *ErrBulkImport
	if !errors.As(err, &be) {
		t.Fatalf("want ErrBulkImport containing %q, got %v", substr, err)
	}
	if !strings.Contains(be.Message, substr) {
		t.Fatalf("error %q does not contain %q", be.Message, substr)
	}
}

func TestAdditionalImportIntegration_CreatesDopWithItems(t *testing.T) {
	pool := newTestPool(t)
	f := seedRollbackFixture(t, pool, "DOPA", nil)
	ctx := context.Background()

	res, err := NewImportRepo(pool).BulkImport(ctx, ImportInput{
		TenderID: f.tenderID,
		FileName: "dop.xlsx",
		UserID:   rbActor,
		Items: []ImportBoqItem{
			importItem(t, pool, f.posID, "раб", 10, 100, nil, nil),
			dopItem(t, pool, "dop_7", "раб", 2, 50),
			dopItem(t, pool, "dop_7", "мат", 3, 10),
		},
		AdditionalPositions: []ImportAdditionalPosition{dopSpec("dop_7", f.posID, "  Устройство   перемычек ", 7)},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.InsertedItemsCount != 3 {
		t.Fatalf("inserted = %d, want 3", res.InsertedItemsCount)
	}
	if len(res.CreatedAdditionalPositions) != 1 || res.CreatedAdditionalPositions[0].PositionNumber != 1.1 {
		t.Fatalf("created = %+v, want one ДОП 1.1", res.CreatedAdditionalPositions)
	}

	dops := loadDops(t, pool, f.posID)
	if len(dops) != 1 {
		t.Fatalf("dops = %d, want 1", len(dops))
	}
	d := dops[0]
	if d.number != 1.1 || d.name != "Устройство перемычек" {
		t.Fatalf("dop = %+v, want 1.1 «Устройство перемычек»", d)
	}
	var parentLevel *int
	if err := pool.QueryRow(ctx, `SELECT hierarchy_level FROM public.client_positions WHERE id = $1::uuid`,
		f.posID).Scan(&parentLevel); err != nil {
		t.Fatalf("parent level: %v", err)
	}
	wantLevel := 1
	if parentLevel != nil {
		wantLevel = *parentLevel + 1
	}
	if d.level == nil || *d.level != wantLevel {
		t.Fatalf("dop level = %v, want %d", d.level, wantLevel)
	}
	if d.manualVolume == nil || *d.manualVolume != 12 {
		t.Fatalf("dop manual_volume = %v, want 12", d.manualVolume)
	}
	if got := countPositionItems(t, pool, d.id); got != 2 {
		t.Fatalf("items in dop = %d, want 2", got)
	}
	if got := countPositionItems(t, pool, f.posID); got != 1 {
		t.Fatalf("items in parent = %d, want 1", got)
	}
	if d.totalWorks != 100 || d.totalMaterial <= 0 {
		t.Fatalf("dop totals works=%v material=%v, want 100 / > 0", d.totalWorks, d.totalMaterial)
	}

	var created string
	if err := pool.QueryRow(ctx, `SELECT created_positions::text FROM public.import_sessions WHERE id = $1::uuid`,
		*res.ImportSessionID).Scan(&created); err != nil {
		t.Fatalf("session: %v", err)
	}
	if !strings.Contains(created, d.id) {
		t.Fatalf("import_sessions.created_positions = %s, want ДОП %s", created, d.id)
	}
}

func TestAdditionalImportIntegration_RejectsDuplicateDop(t *testing.T) {
	pool := newTestPool(t)
	f := seedRollbackFixture(t, pool, "DOPB", nil)
	ctx := context.Background()
	repo := NewImportRepo(pool)

	input := func() ImportInput {
		return ImportInput{
			TenderID:            f.tenderID,
			FileName:            "dop.xlsx",
			UserID:              rbActor,
			Items:               []ImportBoqItem{dopItem(t, pool, "dop_3", "раб", 1, 10)},
			AdditionalPositions: []ImportAdditionalPosition{dopSpec("dop_3", f.posID, "Перемычки", 3)},
		}
	}
	if _, err := repo.BulkImport(ctx, input()); err != nil {
		t.Fatalf("first import: %v", err)
	}

	// Тот же файл второй раз — запрещено, ничего не вставлено.
	_, err := repo.BulkImport(ctx, input())
	wantBulkImportError(t, err, "уже есть ДОП 1.1")
	if dops := loadDops(t, pool, f.posID); len(dops) != 1 || countPositionItems(t, pool, dops[0].id) != 1 {
		t.Fatalf("repeat import changed data: %+v", dops)
	}

	// Повтор внутри файла (регистр/пробелы не важны) — тоже отказ, атомарно.
	_, err = repo.BulkImport(ctx, ImportInput{
		TenderID: f.tenderID,
		FileName: "dop2.xlsx",
		UserID:   rbActor,
		AdditionalPositions: []ImportAdditionalPosition{
			dopSpec("a", f.posID, "Армирование", 4),
			dopSpec("b", f.posID, "  АРМИРОВАНИЕ ", 9),
		},
	})
	wantBulkImportError(t, err, "Строка 9")
	if dops := loadDops(t, pool, f.posID); len(dops) != 1 {
		t.Fatalf("failed import left %d dops, want 1", len(dops))
	}
}

func TestAdditionalImportIntegration_NumberingAfterNine(t *testing.T) {
	pool := newTestPool(t)
	f := seedRollbackFixture(t, pool, "DOPC", nil)
	ctx := context.Background()

	posRepo := NewPositionRepo(pool)
	for i := 1; i <= 9; i++ {
		if _, err := posRepo.CreateAdditionalPosition(ctx, CreateAdditionalPositionInput{
			ParentPositionID: f.posID, TenderID: f.tenderID, WorkName: fmt.Sprintf("доп %d", i), UnitCode: sptr("м2"),
		}); err != nil {
			t.Fatalf("create dop %d: %v", i, err)
		}
	}
	res, err := NewImportRepo(pool).BulkImport(ctx, ImportInput{
		TenderID:            f.tenderID,
		FileName:            "dop10.xlsx",
		UserID:              rbActor,
		AdditionalPositions: []ImportAdditionalPosition{dopSpec("dop_10", f.posID, "доп 10", 10)},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if got := res.CreatedAdditionalPositions[0].PositionNumber; got != 1.91 {
		t.Fatalf("10th ДОП number = %v, want 1.91 (not 2 — the next position)", got)
	}
	var atTwo int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.client_positions
		WHERE tender_id = $1::uuid AND position_number = 2`, f.tenderID).Scan(&atTwo); err != nil {
		t.Fatalf("count: %v", err)
	}
	if atTwo != 0 {
		t.Fatalf("a ДОП took number 2")
	}
}

func TestAdditionalImportIntegration_RejectsBadParent(t *testing.T) {
	pool := newTestPool(t)
	f := seedRollbackFixture(t, pool, "DOPD", nil)
	ctx := context.Background()
	repo := NewImportRepo(pool)

	// Позиция другого тендера (без общего markup-фикстура).
	var otherTenderID, otherPosID string
	if err := pool.QueryRow(ctx, `INSERT INTO public.tenders (title, client_name, tender_number, version)
		VALUES ('itest-dop-other', 'itest-client', 'ITEST-DOP-OTHER', 1) RETURNING id::text`).Scan(&otherTenderID); err != nil {
		t.Fatalf("other tender: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.client_positions WHERE tender_id = $1::uuid`, otherTenderID)
		_, _ = pool.Exec(ctx, `DELETE FROM public.tenders WHERE id = $1::uuid`, otherTenderID)
	})
	if err := pool.QueryRow(ctx, `INSERT INTO public.client_positions (tender_id, position_number, work_name)
		VALUES ($1::uuid, 1, 'itest-dop-other-pos') RETURNING id::text`, otherTenderID).Scan(&otherPosID); err != nil {
		t.Fatalf("other position: %v", err)
	}

	dopID, err := NewPositionRepo(pool).CreateAdditionalPosition(ctx, CreateAdditionalPositionInput{
		ParentPositionID: f.posID, TenderID: f.tenderID, WorkName: "ручная ДОП", UnitCode: sptr("м2"),
	})
	if err != nil {
		t.Fatalf("create dop: %v", err)
	}
	_, err = repo.BulkImport(ctx, ImportInput{
		TenderID: f.tenderID, FileName: "x.xlsx", UserID: rbActor,
		AdditionalPositions: []ImportAdditionalPosition{dopSpec("d", dopID, "ДОП к ДОП", 5)},
	})
	wantBulkImportError(t, err, "к ДОП-работе нельзя добавить ДОП")

	_, err = repo.BulkImport(ctx, ImportInput{
		TenderID: f.tenderID, FileName: "x.xlsx", UserID: rbActor,
		AdditionalPositions: []ImportAdditionalPosition{dopSpec("d", otherPosID, "чужая позиция", 6)},
	})
	wantBulkImportError(t, err, "не найдена в тендере")
}

func TestAdditionalImportIntegration_CancelRemovesDop(t *testing.T) {
	pool := newTestPool(t)
	f := seedRollbackFixture(t, pool, "DOPE", nil)
	ctx := context.Background()

	res, err := NewImportRepo(pool).BulkImport(ctx, ImportInput{
		TenderID: f.tenderID,
		FileName: "dop.xlsx",
		UserID:   rbActor,
		Items: []ImportBoqItem{
			importItem(t, pool, f.posID, "раб", 10, 100, nil, nil),
			dopItem(t, pool, "dop_7", "раб", 2, 50),
		},
		AdditionalPositions: []ImportAdditionalPosition{dopSpec("dop_7", f.posID, "Перемычки", 7)},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	var revBefore int64
	if err := pool.QueryRow(ctx, `SELECT financial_input_revision FROM public.tenders WHERE id = $1::uuid`,
		f.tenderID).Scan(&revBefore); err != nil {
		t.Fatalf("revision: %v", err)
	}

	logRepo := NewImportLogRepo(pool)
	cr, err := logRepo.CancelSession(ctx, *res.ImportSessionID, rbActor, false)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cr.BoqDeleted != 2 || cr.AdditionalDeleted != 1 || cr.AdditionalKept != 0 {
		t.Fatalf("cancel result = %+v, want 2 items / 1 ДОП deleted", cr)
	}
	if dops := loadDops(t, pool, f.posID); len(dops) != 0 {
		t.Fatalf("ДОП survived the cancel: %+v", dops)
	}
	var totalWorks float64
	var revAfter int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(cp.total_works, 0), t.financial_input_revision
		FROM public.client_positions cp JOIN public.tenders t ON t.id = cp.tender_id
		WHERE cp.id = $1::uuid`, f.posID).Scan(&totalWorks, &revAfter); err != nil {
		t.Fatalf("after cancel: %v", err)
	}
	if totalWorks != 0 {
		t.Fatalf("parent total_works = %v after cancel, want 0", totalWorks)
	}
	if revAfter <= revBefore {
		t.Fatalf("financial_input_revision not bumped by cancel: %d → %d", revBefore, revAfter)
	}

	_, err = logRepo.CancelSession(ctx, *res.ImportSessionID, rbActor, false)
	if !errors.Is(err, ErrImportSessionAlreadyCancelled) {
		t.Fatalf("second cancel: want ErrImportSessionAlreadyCancelled, got %v", err)
	}
}

func TestAdditionalImportIntegration_CancelKeepsDopWithForeignItems(t *testing.T) {
	pool := newTestPool(t)
	f := seedRollbackFixture(t, pool, "DOPF", nil)
	ctx := context.Background()

	res, err := NewImportRepo(pool).BulkImport(ctx, ImportInput{
		TenderID:            f.tenderID,
		FileName:            "dop.xlsx",
		UserID:              rbActor,
		Items:               []ImportBoqItem{dopItem(t, pool, "dop_7", "раб", 2, 50)},
		AdditionalPositions: []ImportAdditionalPosition{dopSpec("dop_7", f.posID, "Перемычки", 7)},
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	dopID := res.CreatedAdditionalPositions[0].ID

	// Элемент, добавленный в ДОП вручную после импорта.
	workID, _ := ensureTestNames(t, pool)
	if _, err := pool.Exec(ctx, `
		INSERT INTO public.boq_items
		  (client_position_id, tender_id, boq_item_type, quantity, unit_rate, currency_type,
		   work_name_id, total_amount)
		VALUES ($1::uuid, $2::uuid, 'раб', 1, 10, 'RUB', $3::uuid, 10)`,
		dopID, f.tenderID, workID); err != nil {
		t.Fatalf("manual item: %v", err)
	}

	cr, err := NewImportLogRepo(pool).CancelSession(ctx, *res.ImportSessionID, rbActor, false)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cr.BoqDeleted != 1 || cr.AdditionalDeleted != 0 || cr.AdditionalKept != 1 {
		t.Fatalf("cancel result = %+v, want 1 item deleted, ДОП kept", cr)
	}
	dops := loadDops(t, pool, f.posID)
	if len(dops) != 1 || countPositionItems(t, pool, dopID) != 1 || dops[0].totalWorks != 10 {
		t.Fatalf("kept ДОП = %+v, want 1 manual item with total 10", dops)
	}
}
