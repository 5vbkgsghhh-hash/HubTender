package repository

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/su10/hubtender/backend/internal/calc"
)

// PostgreSQL integration tests for the FULL production audit-rollback path
// (BoqAuditRollbackRepo.Rollback). Reuses the existing convention and the
// production-DSN guard (newTestPool / HUBTENDER_TEST_DATABASE_URL); SKIPs when
// no test DB is configured.
//
//	HUBTENDER_TEST_DATABASE_URL='postgres://…/hubtender_test?sslmode=disable' \
//	  go test ./internal/repository/ -run BoqAuditRollback -v
//
// Fixture arithmetic (deterministic, mirrors the transfer fixture):
//
//	tactic: раб → ×(1+works_16_markup/100), мат → ×(1+material_cost_growth/100)
//	seeded percentages: works 50 (⇒ ×1.5), materials 20 (⇒ ×1.2)
//	distribution: work base→work / markup→material; material base→material / markup→work
//	insurance: (apt_price 10 × apt_area 10) × 50% × 100% = 50
//
//	restored раб qty10 × rate100 (RUB) → total 1000; ×1.5 = 1500 → work 1000, mat 500
//	cached_grand_total = 1500 + 50 = 1550

const rbActor = "00000000-0000-0000-0000-000000000000"

type rbFixture struct {
	tenderNumber string
	tenderID     string
	posID        string
	detailCatID  string
	tacticID     string
	pWorksID     string
}

func seedRollbackFixture(t *testing.T, pool *pgxpool.Pool, tag string, usdRate *float64) *rbFixture {
	t.Helper()
	ctx := context.Background()
	f := &rbFixture{tenderNumber: "ITEST-RB-" + tag}

	must := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("fixture exec: %v\nSQL: %s", err, q)
		}
	}
	scan := func(dst *string, q string, args ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, q, args...).Scan(dst); err != nil {
			t.Fatalf("fixture query: %v\nSQL: %s", err, q)
		}
	}

	var costCatID string
	scan(&costCatID, `INSERT INTO public.cost_categories (name, unit) VALUES ($1,'м2') RETURNING id::text`,
		"itest-rb-cat-"+tag)
	scan(&f.detailCatID, `INSERT INTO public.detail_cost_categories (cost_category_id, location, name, unit)
	                      VALUES ($1::uuid,'loc',$2,'м2') RETURNING id::text`, costCatID, "itest-rb-detail-"+tag)

	var pMat string
	scan(&f.pWorksID, `INSERT INTO public.markup_parameters AS mp (key,label,default_value)
	                   VALUES ('works_16_markup','itest rb works',60) ON CONFLICT (key) DO UPDATE SET label = EXCLUDED.label RETURNING id::text`)
	scan(&pMat, `INSERT INTO public.markup_parameters AS mp (key,label,default_value)
	             VALUES ('material_cost_growth','itest rb mat',10) ON CONFLICT (key) DO UPDATE SET label = EXCLUDED.label RETURNING id::text`)

	const sequences = `{
	  "раб":[{"baseIndex":-1,"action1":"multiply","operand1Type":"markup",
	          "operand1Key":"works_16_markup","operand1MultiplyFormat":"addOne"}],
	  "мат":[{"baseIndex":-1,"action1":"multiply","operand1Type":"markup",
	          "operand1Key":"material_cost_growth","operand1MultiplyFormat":"addOne"}]
	}`
	scan(&f.tacticID, `INSERT INTO public.markup_tactics (name, sequences)
	                   VALUES ($1, $2::jsonb) RETURNING id::text`, "itest-rb-tactic-"+tag, sequences)

	scan(&f.tenderID, `INSERT INTO public.tenders
	        (title, client_name, tender_number, version, usd_rate, markup_tactic_id)
	      VALUES ($1,'itest-client',$2,1,$3,$4::uuid) RETURNING id::text`,
		"itest-rb-"+tag, f.tenderNumber, usdRate, f.tacticID)

	must(`INSERT INTO public.tender_markup_percentage (tender_id, markup_parameter_id, value)
	      VALUES ($1::uuid,$2::uuid,50)`, f.tenderID, f.pWorksID)
	must(`INSERT INTO public.tender_markup_percentage (tender_id, markup_parameter_id, value)
	      VALUES ($1::uuid,$2::uuid,20)`, f.tenderID, pMat)
	must(`INSERT INTO public.tender_pricing_distribution
	        (tender_id, markup_tactic_id,
	         basic_material_base_target, basic_material_markup_target,
	         work_base_target, work_markup_target)
	      VALUES ($1::uuid,$2::uuid,'material','work','work','material')`, f.tenderID, f.tacticID)
	must(`INSERT INTO public.tender_insurance
	        (tender_id, judicial_pct, total_pct, apt_price_m2, apt_area)
	      VALUES ($1::uuid, 50, 100, 10, 10)`, f.tenderID)

	scan(&f.posID, `INSERT INTO public.client_positions (tender_id, position_number, work_name)
	                VALUES ($1::uuid, 1, 'itest-rb-pos') RETURNING id::text`, f.tenderID)
	seedTestPricingOwner(t, pool, f.posID)

	t.Cleanup(func() {
		must(`DELETE FROM public.boq_items_audit WHERE (old_data->>'tender_id') = $1 OR (new_data->>'tender_id') = $1`, f.tenderID)
		must(`DELETE FROM public.boq_items_audit WHERE boq_item_id IN
		        (SELECT id FROM public.boq_items WHERE tender_id = $1::uuid)`, f.tenderID)
		must(`DELETE FROM public.boq_items WHERE tender_id = $1::uuid`, f.tenderID)
		must(`DELETE FROM public.client_positions WHERE tender_id = $1::uuid`, f.tenderID)
		for _, tbl := range []string{"tender_insurance", "tender_pricing_distribution", "tender_markup_percentage"} {
			must(`DELETE FROM public.`+tbl+` WHERE tender_id = $1::uuid`, f.tenderID)
		}
		must(`DELETE FROM public.tenders WHERE id = $1::uuid`, f.tenderID)
		must(`DELETE FROM public.markup_tactics WHERE id = $1::uuid`, f.tacticID)
		must(`DELETE FROM public.markup_parameters WHERE id = ANY($1::uuid[])`, []string{f.pWorksID, pMat})
		must(`DELETE FROM public.detail_cost_categories WHERE id = $1::uuid`, f.detailCatID)
		must(`DELETE FROM public.cost_categories WHERE id = $1::uuid`, costCatID)
	})
	return f
}

// addItem inserts a CURRENT boq_items row with deliberately marked derived
// values (total 111, commercial 1/2/3) so any surviving stale value is obvious.
func (f *rbFixture) addItem(t *testing.T, pool *pgxpool.Pool, itemType, currency string, qty, rate float64, parentID *string) string {
	t.Helper()
	workID, matID := ensureTestNames(t, pool)
	var workRef, matRef *string
	if calc.IsWorkBoqType(itemType) {
		workRef = &workID
	} else {
		matRef = &matID
	}
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO public.boq_items
		  (client_position_id, tender_id, boq_item_type, quantity, unit_rate, currency_type,
		   delivery_price_type, parent_work_item_id, detail_cost_category_id,
		   work_name_id, material_name_id,
		   total_amount, commercial_markup, total_commercial_material_cost, total_commercial_work_cost)
		VALUES ($1::uuid,$2::uuid,$3::boq_item_type,$4,$5,$6::currency_type,
		        'в цене',$7::uuid,$8::uuid, $9::uuid, $10::uuid, 111, 1, 2, 3)
		RETURNING id::text`,
		f.posID, f.tenderID, itemType, qty, rate, currency, parentID, f.detailCatID, workRef, matRef,
	).Scan(&id); err != nil {
		t.Fatalf("add item: %v", err)
	}
	return id
}

// triggerShapeOldData builds a full to_jsonb(OLD.*)-shaped snapshot with
// deliberately CORRUPTED derived values (999999 / 777 / 888888 / 999999).
func (f *rbFixture) triggerShapeOldData(t *testing.T, pool *pgxpool.Pool, itemID, itemType, currency string, qty, rate float64, parentID *string) string {
	t.Helper()
	parent := "null"
	if parentID != nil {
		parent = `"` + *parentID + `"`
	}
	workNameID, matNameID := ensureTestNames(t, pool)
	workRef, matRef := "null", "null"
	if calc.IsWorkBoqType(itemType) {
		workRef = `"` + workNameID + `"`
	} else {
		matRef = `"` + matNameID + `"`
	}
	return fmt.Sprintf(`{
		"id": %q, "tender_id": %q, "client_position_id": %q, "sort_number": 1,
		"boq_item_type": %q, "material_type": null,
		"material_name_id": `+matRef+`, "work_name_id": `+workRef+`, "unit_code": "м2",
		"quantity": %v, "base_quantity": null,
		"consumption_coefficient": null, "conversion_coefficient": null,
		"delivery_price_type": "в цене", "delivery_amount": 0,
		"currency_type": %q, "total_amount": 999999,
		"detail_cost_category_id": %q, "quote_link": null,
		"commercial_markup": 777,
		"total_commercial_material_cost": 888888,
		"total_commercial_work_cost": 999999,
		"created_at": "2026-01-01T00:00:00+00:00", "updated_at": "2026-01-02T00:00:00+00:00",
		"parent_work_item_id": %s, "description": "истор.", "unit_rate": %v,
		"import_session_id": null
	}`, itemID, f.tenderID, f.posID, itemType, qty, currency, f.detailCatID, parent, rate)
}

func (f *rbFixture) addAuditRow(t *testing.T, pool *pgxpool.Pool, itemID, op, oldData string) string {
	t.Helper()
	var id string
	// audit_data_check: UPDATE requires BOTH old_data and new_data; DELETE
	// requires old only. The rollback code reads old_data — the UPDATE row's
	// new_data content is irrelevant, so reuse the snapshot shape.
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO public.boq_items_audit (boq_item_id, operation_type, changed_by, old_data, new_data)
		VALUES ($1::uuid, $2, $3::uuid,
		        CASE WHEN $2 = 'INSERT' THEN NULL ELSE NULLIF($4,'')::jsonb END,
		        CASE WHEN $2 = 'UPDATE' THEN NULLIF($4,'')::jsonb
		             WHEN $2 = 'INSERT' THEN COALESCE(NULLIF($4,'')::jsonb, '{}'::jsonb)
		             ELSE NULL END)
		RETURNING id::text`, itemID, op, rbActor, oldData,
	).Scan(&id); err != nil {
		t.Fatalf("add audit row: %v", err)
	}
	return id
}

type rbItemState struct {
	qty, rate, total   *float64
	markup, mat, work  *float64
	currency, itemType string
	parent             *string
}

func readRbItem(t *testing.T, pool *pgxpool.Pool, id string) *rbItemState {
	t.Helper()
	var s rbItemState
	err := pool.QueryRow(context.Background(), `
		SELECT quantity, unit_rate, total_amount, commercial_markup,
		       total_commercial_material_cost, total_commercial_work_cost,
		       COALESCE(currency_type::text,''), boq_item_type::text, parent_work_item_id::text
		FROM public.boq_items WHERE id = $1::uuid`, id,
	).Scan(&s.qty, &s.rate, &s.total, &s.markup, &s.mat, &s.work, &s.currency, &s.itemType, &s.parent)
	if err != nil {
		t.Fatalf("read item: %v", err)
	}
	return &s
}

func readRbTotals(t *testing.T, pool *pgxpool.Pool, f *rbFixture) (posMat, posWork, grand float64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(total_material,0), COALESCE(total_works,0) FROM public.client_positions WHERE id=$1::uuid`,
		f.posID).Scan(&posMat, &posWork); err != nil {
		t.Fatalf("read position totals: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(cached_grand_total,0) FROM public.tenders WHERE id=$1::uuid`,
		f.tenderID).Scan(&grand); err != nil {
		t.Fatalf("read grand total: %v", err)
	}
	return
}

func rbAuditCount(t *testing.T, pool *pgxpool.Pool, itemID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM public.boq_items_audit WHERE boq_item_id=$1::uuid`, itemID).Scan(&n); err != nil {
		t.Fatalf("audit count: %v", err)
	}
	return n
}

// ─── A. UPDATE rollback: inputs restored, ALL derived values recomputed ──────

func TestBoqAuditRollback_RecalculatesDerivedValues(t *testing.T) {
	pool := newTestPool(t)
	f := seedRollbackFixture(t, pool, "recalc", nil)
	repo := NewBoqAuditRollbackRepo(pool)

	// Current state: qty5 × rate10 (junk derived). History: qty10 × rate100.
	itemID := f.addItem(t, pool, calc.BoqRab, calc.CurrencyRUB, 5, 10, nil)
	auditID := f.addAuditRow(t, pool, itemID, "UPDATE",
		f.triggerShapeOldData(t, pool, itemID, calc.BoqRab, calc.CurrencyRUB, 10, 100, nil))
	before := rbAuditCount(t, pool, itemID)

	res, err := repo.Rollback(context.Background(), auditID, rbActor)
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if res.ItemID != itemID || res.TenderID != f.tenderID || res.Operation != "UPDATE" {
		t.Fatalf("result = %+v", res)
	}

	s := readRbItem(t, pool, itemID)
	// Inputs restored…
	if s.qty == nil || *s.qty != 10 || s.rate == nil || *s.rate != 100 {
		t.Fatalf("inputs not restored: qty=%v rate=%v", s.qty, s.rate)
	}
	// …derived values come from calc, NOT from the snapshot.
	if s.total == nil || *s.total != 1000 {
		t.Fatalf("total_amount = %v, want 1000 (calc); snapshot had 999999", s.total)
	}
	if s.mat == nil || *s.mat != 500 || s.work == nil || *s.work != 1000 {
		t.Fatalf("commercial mat/work = %v/%v, want 500/1000 (current calc); snapshot had 888888/999999", s.mat, s.work)
	}
	if s.markup != nil && *s.markup == 777 {
		t.Fatal("commercial_markup restored from snapshot (777)")
	}
	posMat, posWork, grand := readRbTotals(t, pool, f)
	if posWork != 1000 || posMat != 0 {
		t.Fatalf("position totals = mat %v / work %v, want 0 / 1000", posMat, posWork)
	}
	if grand != 1550 {
		t.Fatalf("cached_grand_total = %v, want 1550 (1500 commercial + 50 insurance)", grand)
	}
	// New rollback audit event exists; the original audit record is untouched.
	if got := rbAuditCount(t, pool, itemID); got != before+1 {
		t.Fatalf("audit rows = %d, want %d (one new rollback event)", got, before+1)
	}
	var origTotal string
	if err := pool.QueryRow(context.Background(),
		`SELECT old_data->>'total_amount' FROM public.boq_items_audit WHERE id=$1::uuid`, auditID,
	).Scan(&origTotal); err != nil || origTotal != "999999" {
		t.Fatalf("original audit record must stay immutable, old total=%q err=%v", origTotal, err)
	}
}

// ─── B. Current tender FX, not the snapshot-era rate ─────────────────────────

func TestBoqAuditRollback_UsesCurrentTenderFX(t *testing.T) {
	pool := newTestPool(t)
	rate := 100.0
	f := seedRollbackFixture(t, pool, "fx", &rate) // CURRENT usd_rate = 100
	repo := NewBoqAuditRollbackRepo(pool)

	itemID := f.addItem(t, pool, calc.BoqRab, calc.CurrencyRUB, 1, 1, nil)
	// Historical snapshot: USD qty2 × rate100 written when USD was 80 ⇒ its
	// stored (corrupted) totals reflect the old world; only inputs matter now.
	auditID := f.addAuditRow(t, pool, itemID, "UPDATE",
		f.triggerShapeOldData(t, pool, itemID, calc.BoqRab, calc.CurrencyUSD, 2, 100, nil))

	if _, err := repo.Rollback(context.Background(), auditID, rbActor); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	s := readRbItem(t, pool, itemID)
	if s.total == nil || *s.total != 20000 {
		t.Fatalf("total_amount = %v, want 20000 (2 × 100 × CURRENT USD 100)", s.total)
	}
}

// ─── C. Current commercial configuration, not the snapshot-era one ───────────

func TestBoqAuditRollback_UsesCurrentCommercialConfiguration(t *testing.T) {
	pool := newTestPool(t)
	f := seedRollbackFixture(t, pool, "config", nil)
	repo := NewBoqAuditRollbackRepo(pool)

	itemID := f.addItem(t, pool, calc.BoqRab, calc.CurrencyRUB, 5, 10, nil)
	auditID := f.addAuditRow(t, pool, itemID, "UPDATE",
		f.triggerShapeOldData(t, pool, itemID, calc.BoqRab, calc.CurrencyRUB, 10, 100, nil))

	// AFTER the audit was written the configuration changed: works 50% → 100%.
	if _, err := pool.Exec(context.Background(),
		`UPDATE public.tender_markup_percentage SET value = 100
		 WHERE tender_id = $1::uuid AND markup_parameter_id = $2::uuid`,
		f.tenderID, f.pWorksID); err != nil {
		t.Fatalf("update markup pct: %v", err)
	}

	if _, err := repo.Rollback(context.Background(), auditID, rbActor); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	s := readRbItem(t, pool, itemID)
	// base 1000 × 2.0 = 2000 → work 1000 (base), material 1000 (markup).
	if s.mat == nil || *s.mat != 1000 || s.work == nil || *s.work != 1000 {
		t.Fatalf("commercial mat/work = %v/%v, want 1000/1000 (CURRENT config ×2.0)", s.mat, s.work)
	}
}

// ─── D. Missing current FX ⇒ blocking error, NOTHING changes ─────────────────

func TestBoqAuditRollback_MissingFXRollsBackEverything(t *testing.T) {
	pool := newTestPool(t)
	f := seedRollbackFixture(t, pool, "nofx", nil) // tender has NO usd_rate
	repo := NewBoqAuditRollbackRepo(pool)

	itemID := f.addItem(t, pool, calc.BoqRab, calc.CurrencyRUB, 5, 10, nil)
	auditID := f.addAuditRow(t, pool, itemID, "UPDATE",
		f.triggerShapeOldData(t, pool, itemID, calc.BoqRab, calc.CurrencyUSD, 2, 100, nil))
	beforeItem := readRbItem(t, pool, itemID)
	beforeMat, beforeWork, beforeGrand := readRbTotals(t, pool, f)
	beforeAudit := rbAuditCount(t, pool, itemID)

	_, err := repo.Rollback(context.Background(), auditID, rbActor)
	var fx *calc.MissingFXRateError
	if !errors.As(err, &fx) || fx.Currency != calc.CurrencyUSD {
		t.Fatalf("expected MissingFXRateError(USD), got %v", err)
	}

	after := readRbItem(t, pool, itemID)
	if *after.qty != *beforeItem.qty || *after.rate != *beforeItem.rate ||
		*after.total != *beforeItem.total || after.currency != beforeItem.currency ||
		*after.mat != *beforeItem.mat || *after.work != *beforeItem.work {
		t.Fatalf("item changed after failed rollback: before=%+v after=%+v", beforeItem, after)
	}
	aMat, aWork, aGrand := readRbTotals(t, pool, f)
	if aMat != beforeMat || aWork != beforeWork || aGrand != beforeGrand {
		t.Fatalf("totals changed after failed rollback: %v/%v/%v → %v/%v/%v",
			beforeMat, beforeWork, beforeGrand, aMat, aWork, aGrand)
	}
	if rbAuditCount(t, pool, itemID) != beforeAudit {
		t.Fatal("a rollback audit event was written despite the failure")
	}
}

// ─── E. Invalid historical parent ⇒ blocking error, NOTHING changes ──────────

func TestBoqAuditRollback_InvalidHistoricalParentRollsBackEverything(t *testing.T) {
	pool := newTestPool(t)
	f := seedRollbackFixture(t, pool, "badparent", nil)
	repo := NewBoqAuditRollbackRepo(pool)

	matParent := f.addItem(t, pool, calc.BoqMat, calc.CurrencyRUB, 1, 10, nil)
	itemID := f.addItem(t, pool, calc.BoqMat, calc.CurrencyRUB, 5, 10, nil)

	cases := []struct {
		name   string
		parent string
		reason BoqParentReason
	}{
		{"missing parent", "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee", BoqParentNotFound},
		{"material parent", matParent, BoqParentNotWorkItem},
		{"self parent", itemID, BoqSelfParentReference},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.parent
			auditID := f.addAuditRow(t, pool, itemID, "UPDATE",
				f.triggerShapeOldData(t, pool, itemID, calc.BoqMat, calc.CurrencyRUB, 10, 100, &p))
			before := readRbItem(t, pool, itemID)
			beforeAudit := rbAuditCount(t, pool, itemID)

			_, err := repo.Rollback(context.Background(), auditID, rbActor)
			var pe *InvalidBoqParentError
			if !errors.As(err, &pe) {
				t.Fatalf("expected InvalidBoqParentError, got %v", err)
			}
			if pe.Reason != tc.reason {
				t.Fatalf("reason = %s, want %s", pe.Reason, tc.reason)
			}
			after := readRbItem(t, pool, itemID)
			if *after.qty != *before.qty || *after.total != *before.total || after.parent != nil {
				t.Fatalf("item changed after blocked rollback: %+v", after)
			}
			if rbAuditCount(t, pool, itemID) != beforeAudit {
				t.Fatal("audit event written despite the failure")
			}
		})
	}
}

// ─── F. Audit of another item / tender can never be applied ──────────────────

func TestBoqAuditRollback_CannotApplyAuditToAnotherItemOrTender(t *testing.T) {
	pool := newTestPool(t)
	fA := seedRollbackFixture(t, pool, "tgt-a", nil)
	fB := seedRollbackFixture(t, pool, "tgt-b", nil)
	repo := NewBoqAuditRollbackRepo(pool)

	itemA := fA.addItem(t, pool, calc.BoqRab, calc.CurrencyRUB, 5, 10, nil)
	itemB := fB.addItem(t, pool, calc.BoqRab, calc.CurrencyRUB, 7, 20, nil)

	// 1. Snapshot carries tender B, audit points at item A (cross-tender).
	crossTender := fB.triggerShapeOldData(t, pool, itemA, calc.BoqRab, calc.CurrencyRUB, 10, 100, nil)
	auditID1 := fA.addAuditRow(t, pool, itemA, "UPDATE", crossTender)
	// 2. Snapshot carries item B's id, audit points at item A (cross-item).
	crossItem := fA.triggerShapeOldData(t, pool, itemB, calc.BoqRab, calc.CurrencyRUB, 10, 100, nil)
	auditID2 := fA.addAuditRow(t, pool, itemA, "UPDATE", crossItem)

	beforeA, beforeB := readRbItem(t, pool, itemA), readRbItem(t, pool, itemB)
	for _, auditID := range []string{auditID1, auditID2} {
		_, err := repo.Rollback(context.Background(), auditID, rbActor)
		var tm *BoqAuditTargetMismatchError
		if !errors.As(err, &tm) {
			t.Fatalf("audit %s: expected BoqAuditTargetMismatchError, got %v", auditID, err)
		}
	}
	afterA, afterB := readRbItem(t, pool, itemA), readRbItem(t, pool, itemB)
	if *afterA.qty != *beforeA.qty || *afterA.total != *beforeA.total {
		t.Fatal("item A changed after rejected rollback")
	}
	if *afterB.qty != *beforeB.qty || *afterB.total != *beforeB.total {
		t.Fatal("item B changed after rejected rollback")
	}
}

// ─── G. DELETE restore via the REAL production delete path ───────────────────

func TestBoqAuditRollback_DeleteRestoreRecalculates(t *testing.T) {
	pool := newTestPool(t)
	f := seedRollbackFixture(t, pool, "restore", nil)
	repo := NewBoqAuditRollbackRepo(pool)
	boqRepo := NewBoqRepo(pool)
	ctx := context.Background()

	workID := f.addItem(t, pool, calc.BoqRab, calc.CurrencyRUB, 10, 100, nil)
	childID := f.addItem(t, pool, calc.BoqMat, calc.CurrencyRUB, 10, 100, &workID)

	// Real production delete — writes the DELETE audit row in the same tx.
	if _, err := boqRepo.DeleteBoqItem(ctx, childID, rbActor); err != nil {
		t.Fatalf("delete child: %v", err)
	}
	var auditID string
	if err := pool.QueryRow(ctx, `
		SELECT id::text FROM public.boq_items_audit
		WHERE boq_item_id=$1::uuid AND operation_type='DELETE'
		ORDER BY changed_at DESC LIMIT 1`, childID).Scan(&auditID); err != nil {
		t.Fatalf("find delete audit: %v", err)
	}
	// Corrupt the snapshot's derived values — they must NOT be restored.
	if _, err := pool.Exec(ctx, `
		UPDATE public.boq_items_audit
		SET old_data = old_data || '{"total_amount":999999,"commercial_markup":777,
		    "total_commercial_material_cost":888888,"total_commercial_work_cost":999999}'::jsonb
		WHERE id=$1::uuid`, auditID); err != nil {
		t.Fatalf("corrupt audit: %v", err)
	}

	res, err := repo.Rollback(ctx, auditID, rbActor)
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if res.ItemID != childID || res.Operation != "DELETE" {
		t.Fatalf("result = %+v", res)
	}
	s := readRbItem(t, pool, childID)
	// Child of a work: consumption forced to 1 ⇒ 10 × 100 = 1000, not 999999.
	if s.total == nil || *s.total != 1000 {
		t.Fatalf("restored total_amount = %v, want 1000 (calc), snapshot had 999999", s.total)
	}
	if s.parent == nil || *s.parent != workID {
		t.Fatalf("restored parent = %v, want %s", s.parent, workID)
	}
	if s.mat != nil && *s.mat == 888888 {
		t.Fatal("snapshot commercial material cost was restored")
	}
	// Position totals include both rows again. The rollback recomputes ONLY
	// the restored row (single-item semantics, 0.1.2.2b): the sibling work row
	// keeps its fixture junk total (111), the restored child contributes 1000.
	posMat, posWork, _ := readRbTotals(t, pool, f)
	if posWork != 111 || posMat != 1000 {
		t.Fatalf("position totals = mat %v / work %v, want 1000 / 111", posMat, posWork)
	}
	// Restoring twice must conflict (id already exists) and change nothing.
	if _, err := repo.Rollback(ctx, auditID, rbActor); err == nil {
		t.Fatal("second restore must fail (id already exists)")
	}
}

// ─── H. INSERT undo is NOT supported — typed error, no mutation ──────────────

func TestBoqAuditRollback_InsertUndoUnsupported(t *testing.T) {
	pool := newTestPool(t)
	f := seedRollbackFixture(t, pool, "insundo", nil)
	repo := NewBoqAuditRollbackRepo(pool)

	itemID := f.addItem(t, pool, calc.BoqRab, calc.CurrencyRUB, 5, 10, nil)
	auditID := f.addAuditRow(t, pool, itemID, "INSERT", "")
	before := readRbItem(t, pool, itemID)

	_, err := repo.Rollback(context.Background(), auditID, rbActor)
	var unsup *UnsupportedBoqAuditRollbackError
	if !errors.As(err, &unsup) || unsup.Operation != "INSERT" {
		t.Fatalf("expected UnsupportedBoqAuditRollbackError(INSERT), got %v", err)
	}
	after := readRbItem(t, pool, itemID)
	if *after.qty != *before.qty || *after.total != *before.total {
		t.Fatal("item changed after unsupported rollback")
	}
}
