package repository

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgreSQL integration tests for stage 2.4 (§6/§18.8-12): tri-state PATCH
// реально пишет NULL, parent clear пересчитывает total по standalone-семантике,
// metadata-clear не двигает финансовую ревизию.
// COMPILED + SKIPPED без HUBTENDER_TEST_DATABASE_URL.

// seedPatchFixture: позиция + работа + материал, привязанный к работе.
func seedPatchFixture(t *testing.T, pool *pgxpool.Pool, tag string) (tenderID, workID, matID string) {
	t.Helper()
	ctx := context.Background()
	tenderID, posID := seedSourceTender(t, pool, tag)
	seedTestPricingOwner(t, pool, posID)
	workNameID, matNameID := ensureTestNames(t, pool)

	if err := pool.QueryRow(ctx, `
		INSERT INTO public.boq_items
			(tender_id, client_position_id, boq_item_type, description, unit_code,
			 quantity, unit_rate, currency_type, work_name_id)
		VALUES ($1::uuid, $2::uuid, 'раб', 'работа для patch', 'м2', 10, 100, 'RUB', $3::uuid)
		RETURNING id::text`, tenderID, posID, workNameID).Scan(&workID); err != nil {
		t.Fatal(err)
	}
	// Материал, привязанный к работе: quantity = work.qty × consumption(2) = 20.
	if err := pool.QueryRow(ctx, `
		INSERT INTO public.boq_items
			(tender_id, client_position_id, boq_item_type, material_type, description,
			 unit_code, quantity, unit_rate, currency_type, consumption_coefficient,
			 conversion_coefficient, parent_work_item_id, material_name_id)
		VALUES ($1::uuid, $2::uuid, 'мат', 'основн.', 'материал для patch',
			 'шт', 20, 5, 'RUB', 2, 1, $3::uuid, $4::uuid)
		RETURNING id::text`, tenderID, posID, workID, matNameID).Scan(&matID); err != nil {
		t.Fatal(err)
	}
	return tenderID, workID, matID
}

// §18.8-10: absent не трогает, явный null пишет NULL, значение пишется.
func TestBoqPatchIntegration_TriStateNull(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	_, workID, matID := seedPatchFixture(t, pool, "TP-A")
	repo := NewBoqRepo(pool)

	// absent: PATCH без tri-state полей не меняет parent.
	d := "описание без очистки"
	if _, err := repo.UpdateBoqItem(ctx, matID, BoqItemPatch{Description: &d, ChangedBy: rbActor}); err != nil {
		t.Fatalf("absent patch: %v", err)
	}
	var parent *string
	_ = pool.QueryRow(ctx, `SELECT parent_work_item_id::text FROM public.boq_items WHERE id=$1::uuid`, matID).Scan(&parent)
	if parent == nil || *parent != workID {
		t.Fatalf("absent must keep parent: %v", parent)
	}

	// явный null: parent + conversion очищаются, реально NULL в БД (§18.9).
	patch := BoqItemPatch{ChangedBy: rbActor}
	patch.ParentWorkItemID.SetNull()
	patch.ConversionCoefficient.SetNull()
	patch.BaseQuantity.SetValue(20) // standalone-семантика: количество ГП
	if _, err := repo.UpdateBoqItem(ctx, matID, patch); err != nil {
		t.Fatalf("clear patch: %v", err)
	}
	var conv *float64
	_ = pool.QueryRow(ctx, `
		SELECT parent_work_item_id::text, conversion_coefficient
		FROM public.boq_items WHERE id=$1::uuid`, matID).Scan(&parent, &conv)
	if parent != nil {
		t.Fatalf("explicit null must write NULL parent, got %v", *parent)
	}
	if conv != nil {
		t.Fatalf("explicit null must write NULL conversion, got %v", *conv)
	}

	// значение: parent возвращается (§18.10/§18.17: валидный parent разрешён).
	patch2 := BoqItemPatch{ChangedBy: rbActor}
	patch2.ParentWorkItemID.SetValue(workID)
	if _, err := repo.UpdateBoqItem(ctx, matID, patch2); err != nil {
		t.Fatalf("set patch: %v", err)
	}
	_ = pool.QueryRow(ctx, `SELECT parent_work_item_id::text FROM public.boq_items WHERE id=$1::uuid`, matID).Scan(&parent)
	if parent == nil || *parent != workID {
		t.Fatalf("value must be written: %v", parent)
	}
}

// §18.11: parent clear пересчитывает total_amount (standalone) и двигает
// финансовую ревизию ровно на 1; approval снимается.
func TestBoqPatchIntegration_ParentClearRecalculates(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenderID, _, matID := seedPatchFixture(t, pool, "TP-B")
	repo := NewBoqRepo(pool)
	approveDirect(t, pool, tenderID)
	before := readFinState(t, pool, tenderID)

	var totalBefore float64
	_ = pool.QueryRow(ctx, `SELECT total_amount FROM public.boq_items WHERE id=$1::uuid`, matID).Scan(&totalBefore)

	patch := BoqItemPatch{ChangedBy: rbActor}
	patch.ParentWorkItemID.SetNull()
	patch.ConversionCoefficient.SetNull()
	patch.BaseQuantity.SetValue(7) // standalone: 7 × consumption 2 × rate 5 = 70
	q := 7.0
	patch.Quantity = &q
	updated, err := repo.UpdateBoqItem(ctx, matID, patch)
	if err != nil {
		t.Fatalf("clear: %v", err)
	}
	if updated.TotalAmount == nil || *updated.TotalAmount != 70 {
		t.Fatalf("standalone total=%v, want 70 (7×2×5)", updated.TotalAmount)
	}
	after := readFinState(t, pool, tenderID)
	if after.inputRev != before.inputRev+1 {
		t.Fatalf("revision must bump exactly once: %d → %d", before.inputRev, after.inputRev)
	}
	if after.approved {
		t.Fatal("approval must be invalidated by financial clear")
	}
	if after.status != "stale" {
		t.Fatalf("status=%s, want stale", after.status)
	}
}

// §18.12: metadata-only очистка даты НЕ двигает финансовую ревизию.
func TestBoqPatchIntegration_MetadataClearNoRevisionBump(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenderID, _, matID := seedPatchFixture(t, pool, "TP-C")
	repo := NewBoqRepo(pool)

	// Проставить дату, затем очистить "" — оба metadata-only.
	d1 := "2026-07-01"
	if _, err := repo.UpdateBoqItem(ctx, matID, BoqItemPatch{QuotePriceDate: &d1, ChangedBy: rbActor}); err != nil {
		t.Fatalf("set date: %v", err)
	}
	before := readFinState(t, pool, tenderID)
	empty := ""
	if _, err := repo.UpdateBoqItem(ctx, matID, BoqItemPatch{QuotePriceDate: &empty, ChangedBy: rbActor}); err != nil {
		t.Fatalf("clear date: %v", err)
	}
	var pd *string
	_ = pool.QueryRow(ctx, `SELECT quote_price_date::text FROM public.boq_items WHERE id=$1::uuid`, matID).Scan(&pd)
	if pd != nil {
		t.Fatalf("date must be NULL after clear, got %v", *pd)
	}
	after := readFinState(t, pool, tenderID)
	if after.inputRev != before.inputRev {
		t.Fatalf("metadata clear must not bump revision: %d → %d", before.inputRev, after.inputRev)
	}
}

// Справочные tri-state поля: очистка detail_cost_category_id пишет NULL и
// остаётся финансовым изменением (bump); очистка material_name_id на
// мат-строке отклоняется доменным CHECK (boq_items_material_check) —
// fail-closed, а не тихое игнорирование как в старом pointer-декоде.
func TestBoqPatchIntegration_CatalogFieldsClear(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenderID, _, matID := seedPatchFixture(t, pool, "TP-D")
	repo := NewBoqRepo(pool)

	// Сначала назначить категорию, затем очистить tri-state null'ом.
	var dcID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO public.cost_categories (name, unit) VALUES ('tp-cat', 'м2') RETURNING id::text`).Scan(&dcID); err != nil {
		t.Fatal(err)
	}
	var detailID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO public.detail_cost_categories (cost_category_id, location, name, unit)
		VALUES ($1::uuid, 'лок', 'tp-detail', 'м2') RETURNING id::text`, dcID).Scan(&detailID); err != nil {
		t.Fatal(err)
	}
	setPatch := BoqItemPatch{ChangedBy: rbActor}
	setPatch.DetailCostCategoryID.SetValue(detailID)
	if _, err := repo.UpdateBoqItem(ctx, matID, setPatch); err != nil {
		t.Fatalf("set category: %v", err)
	}
	before := readFinState(t, pool, tenderID)

	patch := BoqItemPatch{ChangedBy: rbActor}
	patch.DetailCostCategoryID.SetNull()
	if _, err := repo.UpdateBoqItem(ctx, matID, patch); err != nil {
		t.Fatalf("clear: %v", err)
	}
	var dc *string
	_ = pool.QueryRow(ctx, `
		SELECT detail_cost_category_id::text
		FROM public.boq_items WHERE id=$1::uuid`, matID).Scan(&dc)
	if dc != nil {
		t.Fatalf("category ref must be NULL: %v", *dc)
	}
	after := readFinState(t, pool, tenderID)
	if after.inputRev != before.inputRev+1 {
		t.Fatalf("catalog clear bumps revision once: %d → %d", before.inputRev, after.inputRev)
	}

	// Очистка material_name_id на мат-строке нарушает доменный CHECK — PATCH
	// обязан fail-closed (ошибка, транзакция откатывается), а не молчать.
	bad := BoqItemPatch{ChangedBy: rbActor}
	bad.MaterialNameID.SetNull()
	if _, err := repo.UpdateBoqItem(ctx, matID, bad); err == nil {
		t.Fatal("clearing material_name_id on мат must violate boq_items_material_check")
	}
	var mn *string
	_ = pool.QueryRow(ctx, `SELECT material_name_id::text FROM public.boq_items WHERE id=$1::uuid`, matID).Scan(&mn)
	if mn == nil {
		t.Fatal("failed patch must roll back (material_name_id intact)")
	}
}
