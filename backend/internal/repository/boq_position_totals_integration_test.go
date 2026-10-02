package repository

import (
	"context"
	"math"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgreSQL integration: создание, правка и удаление строки, а также пересчёт
// привязанных материалов обновляют total_material / total_works позиции в той же
// транзакции — без отдельного вызова recompute-totals с фронта.
// COMPILED + SKIPPED без HUBTENDER_TEST_DATABASE_URL.

func assertPositionTotals(t *testing.T, pool *pgxpool.Pool, posID string, wantMat, wantWork float64, step string) {
	t.Helper()
	var mat, work float64
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(total_material, 0)::float8, COALESCE(total_works, 0)::float8
		FROM public.client_positions WHERE id = $1::uuid`, posID).Scan(&mat, &work); err != nil {
		t.Fatalf("%s: read totals: %v", step, err)
	}
	if math.Abs(mat-wantMat) > 0.001 || math.Abs(work-wantWork) > 0.001 {
		t.Fatalf("%s: totals мат/раб = %.2f/%.2f, want %.2f/%.2f", step, mat, work, wantMat, wantWork)
	}
}

func TestBoqPositionTotalsIntegration_RowWritesKeepTotals(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenderID, posID := seedSourceTender(t, pool, "PT-A")
	workNameID, matNameID := ensureTestNames(t, pool)
	repo := NewBoqRepo(pool)

	work, err := repo.CreateBoqItem(ctx, CreateBoqItemInput{
		TenderID: tenderID, ClientPositionID: posID, BoqItemType: "раб",
		WorkNameID: &workNameID, UnitCode: sptr("м2"),
		Quantity: fptr(10), UnitRate: fptr(100), CurrencyType: sptr("RUB"),
		CreatedBy: rbActor,
	})
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	assertPositionTotals(t, pool, posID, 0, 1000, "create work")

	// Материал, привязанный к работе: 10 × conversion(1) × consumption(2) = 20 шт по 5.
	mat, err := repo.CreateBoqItem(ctx, CreateBoqItemInput{
		TenderID: tenderID, ClientPositionID: posID, BoqItemType: "мат",
		MaterialType: sptr("основн."), MaterialNameID: &matNameID, UnitCode: sptr("шт"),
		Quantity: fptr(20), UnitRate: fptr(5), CurrencyType: sptr("RUB"),
		DeliveryPriceType: sptr("в цене"), ConversionCoefficient: fptr(1),
		ConsumptionCoefficient: fptr(2), ParentWorkItemID: &work.ID,
		CreatedBy: rbActor,
	})
	if err != nil {
		t.Fatalf("create material: %v", err)
	}
	assertPositionTotals(t, pool, posID, 100, 1000, "create material")

	if _, err := repo.UpdateBoqItem(ctx, work.ID, BoqItemPatch{Quantity: fptr(20), ChangedBy: rbActor}); err != nil {
		t.Fatalf("update work: %v", err)
	}
	assertPositionTotals(t, pool, posID, 100, 2000, "update work quantity")

	// Пересчёт привязанных материалов: 20 × 1 × 2 = 40 шт по 5.
	if _, err := repo.RecomputeLinkedMaterialsForWork(ctx, work.ID, rbActor); err != nil {
		t.Fatalf("recompute linked materials: %v", err)
	}
	assertPositionTotals(t, pool, posID, 200, 2000, "recompute linked materials")

	// Правка без денег (только описание) не переписывает позицию.
	var before string
	if err := pool.QueryRow(ctx, `SELECT updated_at::text FROM public.client_positions WHERE id = $1::uuid`, posID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpdateBoqItem(ctx, mat.ID, BoqItemPatch{Description: sptr("только текст"), ChangedBy: rbActor}); err != nil {
		t.Fatalf("update description: %v", err)
	}
	var after string
	if err := pool.QueryRow(ctx, `SELECT updated_at::text FROM public.client_positions WHERE id = $1::uuid`, posID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("money-neutral patch rewrote the position: updated_at %s → %s", before, after)
	}

	if _, err := repo.DeleteBoqItem(ctx, mat.ID, rbActor); err != nil {
		t.Fatalf("delete material: %v", err)
	}
	assertPositionTotals(t, pool, posID, 0, 2000, "delete material")

	if _, err := repo.DeleteBoqItem(ctx, work.ID, rbActor); err != nil {
		t.Fatalf("delete work: %v", err)
	}
	assertPositionTotals(t, pool, posID, 0, 0, "delete work")
}
