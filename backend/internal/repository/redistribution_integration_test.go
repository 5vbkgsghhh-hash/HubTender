package repository

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/su10/hubtender/backend/internal/calc"
)

// PostgreSQL integration tests for the FULL production redistribution save
// path (RedistributionRepo.SaveAuthoritative). Reuses the existing convention
// and the production-DSN guard (newTestPool / HUBTENDER_TEST_DATABASE_URL);
// SKIPs when no test DB is configured.
//
//	HUBTENDER_TEST_DATABASE_URL='postgres://…/hubtender_test?sslmode=disable' \
//	  go test ./internal/repository/ -run Redistribution -v
//
// Fixture arithmetic (same tactic as the transfer/rollback fixtures):
//
//	tactic: раб ×1.5 (works_16_markup 50), мат ×1.2 (material_cost_growth 20)
//	distribution: work base→work / markup→material
//	insurance: (10 × 10) × 50% × 100% = 50
//
//	w1 (раб, detail d1, qty10 × 100) → base 1000 → commercial work 1000, mat 500
//	w2 (раб, detail d2, qty5  × 100) → base  500 → commercial work  500, mat 250
//
//	rules: deduct d1 10% (=100) → target d2:
//	  w1: orig 1000, ded 100, add   0, final  900
//	  w2: orig  500, ded   0, add 100, final  600

type rdFixture struct {
	tenderNumber string
	tenderID     string
	pos1ID       string
	pos2ID       string
	catID        string
	cat2ID       string
	detail1ID    string
	detail2ID    string
	tacticID     string
	item1ID      string
	item2ID      string
}

// seedRedistributionFixture builds a tender with an active tactic, explicit
// markup percentages/distribution, insurance and two positions.
func seedRedistributionFixture(t *testing.T, pool *pgxpool.Pool, tag string, usdRate *float64) *rdFixture {
	t.Helper()
	ctx := context.Background()
	f := &rdFixture{tenderNumber: "ITEST-RD-" + tag}

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

	scan(&f.catID, `INSERT INTO public.cost_categories (name, unit) VALUES ($1,'м2') RETURNING id::text`,
		"itest-rd-cat1-"+tag)
	scan(&f.cat2ID, `INSERT INTO public.cost_categories (name, unit) VALUES ($1,'м2') RETURNING id::text`,
		"itest-rd-cat2-"+tag)
	scan(&f.detail1ID, `INSERT INTO public.detail_cost_categories (cost_category_id, location, name, unit)
	                    VALUES ($1::uuid,'loc',$2,'м2') RETURNING id::text`, f.catID, "itest-rd-d1-"+tag)
	scan(&f.detail2ID, `INSERT INTO public.detail_cost_categories (cost_category_id, location, name, unit)
	                    VALUES ($1::uuid,'loc',$2,'м2') RETURNING id::text`, f.cat2ID, "itest-rd-d2-"+tag)

	var pWorks, pMat string
	scan(&pWorks, `INSERT INTO public.markup_parameters AS mp (key,label,default_value)
	               VALUES ('works_16_markup','itest rd works',60) ON CONFLICT (key) DO UPDATE SET label = EXCLUDED.label RETURNING id::text`)
	scan(&pMat, `INSERT INTO public.markup_parameters AS mp (key,label,default_value)
	             VALUES ('material_cost_growth','itest rd mat',10) ON CONFLICT (key) DO UPDATE SET label = EXCLUDED.label RETURNING id::text`)
	const sequences = `{
	  "раб":[{"baseIndex":-1,"action1":"multiply","operand1Type":"markup",
	          "operand1Key":"works_16_markup","operand1MultiplyFormat":"addOne"}],
	  "мат":[{"baseIndex":-1,"action1":"multiply","operand1Type":"markup",
	          "operand1Key":"material_cost_growth","operand1MultiplyFormat":"addOne"}]
	}`
	scan(&f.tacticID, `INSERT INTO public.markup_tactics (name, sequences)
	                   VALUES ($1, $2::jsonb) RETURNING id::text`, "itest-rd-tactic-"+tag, sequences)

	scan(&f.tenderID, `INSERT INTO public.tenders
	        (title, client_name, tender_number, version, usd_rate, markup_tactic_id)
	      VALUES ($1,'itest-client',$2,1,$3,$4::uuid) RETURNING id::text`,
		"itest-rd-"+tag, f.tenderNumber, usdRate, f.tacticID)

	must(`INSERT INTO public.tender_markup_percentage (tender_id, markup_parameter_id, value)
	      VALUES ($1::uuid,$2::uuid,50)`, f.tenderID, pWorks)
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

	scan(&f.pos1ID, `INSERT INTO public.client_positions (tender_id, position_number, work_name)
	                 VALUES ($1::uuid, 1, 'itest-rd-p1') RETURNING id::text`, f.tenderID)
	scan(&f.pos2ID, `INSERT INTO public.client_positions (tender_id, position_number, work_name)
	                 VALUES ($1::uuid, 2, 'itest-rd-p2') RETURNING id::text`, f.tenderID)

	t.Cleanup(func() {
		must(`DELETE FROM public.cost_redistribution_results WHERE tender_id = $1::uuid`, f.tenderID)
		must(`DELETE FROM public.boq_items WHERE tender_id = $1::uuid`, f.tenderID)
		must(`DELETE FROM public.client_positions WHERE tender_id = $1::uuid`, f.tenderID)
		for _, tbl := range []string{"tender_insurance", "tender_pricing_distribution", "tender_markup_percentage"} {
			must(`DELETE FROM public.`+tbl+` WHERE tender_id = $1::uuid`, f.tenderID)
		}
		must(`DELETE FROM public.tenders WHERE id = $1::uuid`, f.tenderID)
		must(`DELETE FROM public.markup_tactics WHERE id = $1::uuid`, f.tacticID)
		must(`DELETE FROM public.markup_parameters WHERE id = ANY($1::uuid[])`, []string{pWorks, pMat})
		must(`DELETE FROM public.detail_cost_categories WHERE id = ANY($1::uuid[])`, []string{f.detail1ID, f.detail2ID})
		must(`DELETE FROM public.cost_categories WHERE id = ANY($1::uuid[])`, []string{f.catID, f.cat2ID})
	})
	return f
}

// addRdItem inserts a раб-item with deliberately CORRUPTED stored commercial
// values (999999/777) — the save must materialize fresh ones.
func (f *rdFixture) addRdItem(t *testing.T, pool *pgxpool.Pool, posID, detailID, currency string, qty, rate float64) string {
	t.Helper()
	workNameID, _ := ensureTestNames(t, pool)
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO public.boq_items
		  (client_position_id, tender_id, boq_item_type, work_name_id, quantity, unit_rate, currency_type,
		   delivery_price_type, detail_cost_category_id,
		   total_amount, commercial_markup, total_commercial_material_cost, total_commercial_work_cost)
		VALUES ($1::uuid,$2::uuid,'раб',$3::uuid,$4,$5,$6::currency_type,'в цене',$7::uuid,
		        $4::numeric * $5::numeric, 777, 999999, 999999)
		RETURNING id::text`,
		posID, f.tenderID, workNameID, qty, rate, currency, detailID,
	).Scan(&id); err != nil {
		t.Fatalf("add rd item: %v", err)
	}
	return id
}

func (f *rdFixture) seedTwoItems(t *testing.T, pool *pgxpool.Pool) {
	f.item1ID = f.addRdItem(t, pool, f.pos1ID, f.detail1ID, "RUB", 10, 100)
	f.item2ID = f.addRdItem(t, pool, f.pos2ID, f.detail2ID, "RUB", 5, 100)
}

func (f *rdFixture) d1toD2Rules() calc.RedistributionRulesInput {
	return calc.RedistributionRulesInput{
		Deductions: []calc.RedistributionSourceRuleInput{{
			Level: "detail", DetailCostCategoryID: f.detail1ID, Percentage: 10,
		}},
		Targets: []calc.RedistributionTargetInput{{
			Level: "detail", DetailCostCategoryID: f.detail2ID,
		}},
	}
}

type rdRow struct {
	orig, ded, add, final float64
	rules                 []byte
}

func readRdRows(t *testing.T, pool *pgxpool.Pool, tenderID, tacticID string) map[string]rdRow {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT boq_item_id::text, COALESCE(original_work_cost,0), deducted_amount,
		       added_amount, COALESCE(final_work_cost,0), redistribution_rules
		FROM public.cost_redistribution_results
		WHERE tender_id=$1::uuid AND markup_tactic_id=$2::uuid`, tenderID, tacticID)
	if err != nil {
		t.Fatalf("read rows: %v", err)
	}
	defer rows.Close()
	out := map[string]rdRow{}
	for rows.Next() {
		var id string
		var r rdRow
		if err := rows.Scan(&id, &r.orig, &r.ded, &r.add, &r.final, &r.rules); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out[id] = r
	}
	return out
}

// ─── A+B. Exact server-generated set; forged client values unreachable ───────

func TestRedistributionSave_ExactServerSet(t *testing.T) {
	pool := newTestPool(t)
	f := seedRedistributionFixture(t, pool, "exact", nil)
	f.seedTwoItems(t, pool)
	// A second tender whose item must NEVER appear in the snapshot.
	other := seedRedistributionFixture(t, pool, "exact-other", nil)
	other.seedTwoItems(t, pool)

	repo := NewRedistributionRepo(pool)
	out, err := repo.SaveAuthoritative(context.Background(), f.tenderID, f.tacticID, f.d1toD2Rules(), rbActor)
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	if out.SavedCount != 2 || len(out.Results) != 2 {
		t.Fatalf("saved_count = %d / results %d, want 2", out.SavedCount, len(out.Results))
	}
	if !out.IsBalanced || out.TotalDeducted != 100 || out.TotalAdded != 100 {
		t.Fatalf("totals wrong: %+v", out)
	}

	rows := readRdRows(t, pool, f.tenderID, f.tacticID)
	if len(rows) != 2 {
		t.Fatalf("persisted rows = %d, want 2 (exact server set)", len(rows))
	}
	r1, ok1 := rows[f.item1ID]
	r2, ok2 := rows[f.item2ID]
	if !ok1 || !ok2 {
		t.Fatalf("persisted set != server-loaded tender BOQ set: %v", rows)
	}
	// The forged legacy constants (777 / 888888 / 999999 / -123) are unreachable:
	// values equal the authoritative calc.
	if r1.orig != 1000 || r1.ded != 100 || r1.add != 0 || r1.final != 900 {
		t.Fatalf("w1 = %+v, want {1000 100 0 900}", r1)
	}
	if r2.orig != 500 || r2.ded != 0 || r2.add != 100 || r2.final != 600 {
		t.Fatalf("w2 = %+v, want {500 0 100 600}", r2)
	}

	// Deterministic holder: rules live ONLY on the smallest boq_item_id.
	holder := f.item1ID
	if f.item2ID < holder {
		holder = f.item2ID
	}
	for id, r := range rows {
		if id == holder && len(r.rules) == 0 {
			t.Fatal("holder row lost the rules JSONB")
		}
		if id != holder && len(r.rules) > 0 {
			t.Fatalf("non-holder row %s carries rules", id)
		}
	}
	var meta struct {
		SchemaVersion     int    `json:"schema_version"`
		CalculationSource string `json:"calculation_source"`
		Deductions        []struct {
			CategoryName string `json:"category_name"`
		} `json:"deductions"`
	}
	if err := json.Unmarshal(rows[holder].rules, &meta); err != nil {
		t.Fatalf("rules JSON: %v", err)
	}
	if meta.SchemaVersion != 2 || meta.CalculationSource != "server" {
		t.Fatalf("server metadata missing in persisted rules: %+v", meta)
	}
	if len(meta.Deductions) != 1 || meta.Deductions[0].CategoryName != "itest-rd-d1-exact" {
		t.Fatalf("canonical DB name missing: %+v", meta.Deductions)
	}

	// The other tender's snapshot table stays empty.
	if got := readRdRows(t, pool, other.tenderID, other.tacticID); len(got) != 0 {
		t.Fatalf("cross-tender rows appeared: %v", got)
	}
	// GET reports the snapshot as server-authoritative.
	load, err := repo.LoadResults(context.Background(), f.tenderID, f.tacticID)
	if err != nil || load.Status != RedistributionStatusCalculated {
		t.Fatalf("status = %q err=%v, want calculated", load.Status, err)
	}
}

// ─── C. The commercial base is materialized fresh, not the stale fields ──────

func TestRedistributionSave_UsesCurrentCommercialBase(t *testing.T) {
	pool := newTestPool(t)
	f := seedRedistributionFixture(t, pool, "base", nil)
	f.seedTwoItems(t, pool) // stored commercial = 999999 (corrupted)

	repo := NewRedistributionRepo(pool)
	out, err := repo.SaveAuthoritative(context.Background(), f.tenderID, f.tacticID, f.d1toD2Rules(), rbActor)
	if err != nil {
		t.Fatalf("save failed: %v", err)
	}
	for _, r := range out.Results {
		if r.OriginalWorkCost == 999999 {
			t.Fatal("original_work_cost took the STALE stored commercial field")
		}
	}
	// boq_items commercial fields were materialized inside the same tx.
	var work float64
	if err := pool.QueryRow(context.Background(),
		`SELECT total_commercial_work_cost FROM public.boq_items WHERE id=$1::uuid`, f.item1ID).Scan(&work); err != nil {
		t.Fatalf("read item: %v", err)
	}
	if work != 1000 {
		t.Fatalf("materialized work cost = %v, want 1000", work)
	}
}

// ─── D. Tactic mismatch → typed 409, nothing changes ─────────────────────────

func TestRedistributionSave_TacticMismatchChangesNothing(t *testing.T) {
	pool := newTestPool(t)
	f := seedRedistributionFixture(t, pool, "tactic", nil)
	f.seedTwoItems(t, pool)

	var otherTactic string
	if err := pool.QueryRow(context.Background(), `INSERT INTO public.markup_tactics (name, sequences)
		VALUES ('itest-rd-other-tactic','{}'::jsonb) RETURNING id::text`).Scan(&otherTactic); err != nil {
		t.Fatalf("seed other tactic: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM public.markup_tactics WHERE id=$1::uuid`, otherTactic)
	})

	repo := NewRedistributionRepo(pool)
	_, err := repo.SaveAuthoritative(context.Background(), f.tenderID, otherTactic, f.d1toD2Rules(), rbActor)
	var tm *calc.RedistributionTacticMismatchError
	if !errors.As(err, &tm) {
		t.Fatalf("want RedistributionTacticMismatchError, got %v", err)
	}
	// Snapshot and commercial state untouched (corrupted markers survive).
	if rows := readRdRows(t, pool, f.tenderID, otherTactic); len(rows) != 0 {
		t.Fatal("snapshot rows appeared despite mismatch")
	}
	var work float64
	if err := pool.QueryRow(context.Background(),
		`SELECT total_commercial_work_cost FROM public.boq_items WHERE id=$1::uuid`, f.item1ID).Scan(&work); err != nil {
		t.Fatalf("read item: %v", err)
	}
	if work != 999999 {
		t.Fatalf("commercial fields changed on a rejected save: %v", work)
	}
}

// ─── E. FX independence: redistribution reads MATERIALIZED totals ────────────
//
// Live-DB acceptance (0-F2) pinned the actual semantics: redistribution save
// derives commercial values from the PERSISTED authoritative total_amount —
// it never recomputes row totals, so it needs no FX rates. The fail-closed
// MISSING_FX_RATE lives on every path that (re)computes total_amount (create/
// update/import/reprice/rollback); a USD row cannot reach this state with a
// wrong total in the first place.

func TestRedistributionSave_MissingFXRollsBackEverything(t *testing.T) {
	pool := newTestPool(t)
	f := seedRedistributionFixture(t, pool, "nofx", nil) // no usd_rate
	f.item1ID = f.addRdItem(t, pool, f.pos1ID, f.detail1ID, "RUB", 10, 100)
	f.item2ID = f.addRdItem(t, pool, f.pos2ID, f.detail2ID, "USD", 5, 100) // total already materialized

	repo := NewRedistributionRepo(pool)
	out, err := repo.SaveAuthoritative(context.Background(), f.tenderID, f.tacticID, f.d1toD2Rules(), rbActor)
	if err != nil {
		t.Fatalf("save must not require FX (works on materialized totals): %v", err)
	}
	if out.SavedCount != 2 {
		t.Fatalf("saved = %d, want 2", out.SavedCount)
	}
	if rows := readRdRows(t, pool, f.tenderID, f.tacticID); len(rows) != 2 {
		t.Fatalf("snapshot rows = %d, want 2", len(rows))
	}
}

// ─── F. Effective source/target overlap → blocked, no mutation ───────────────

func TestRedistributionSave_EffectiveOverlapBlocked(t *testing.T) {
	pool := newTestPool(t)
	f := seedRedistributionFixture(t, pool, "overlap", nil)
	f.seedTwoItems(t, pool)

	rules := calc.RedistributionRulesInput{
		Deductions: []calc.RedistributionSourceRuleInput{{
			Level: "detail", DetailCostCategoryID: f.detail1ID, Percentage: 10,
		}},
		// Target = the category containing d1 → item1 is source AND target.
		Targets: []calc.RedistributionTargetInput{{Level: "category", CategoryID: f.catID}},
	}
	repo := NewRedistributionRepo(pool)
	_, err := repo.SaveAuthoritative(context.Background(), f.tenderID, f.tacticID, rules, rbActor)
	var rulesErr *calc.InvalidRedistributionRulesError
	if !errors.As(err, &rulesErr) {
		t.Fatalf("want InvalidRedistributionRulesError, got %v", err)
	}
	if rows := readRdRows(t, pool, f.tenderID, f.tacticID); len(rows) != 0 {
		t.Fatal("mutation happened despite invalid rules")
	}
}

// ─── G. Position-only rules → complete server-generated no-op set ────────────

func TestRedistributionSave_PositionOnlyNoop(t *testing.T) {
	pool := newTestPool(t)
	f := seedRedistributionFixture(t, pool, "posonly", nil)
	f.seedTwoItems(t, pool)

	rules := calc.RedistributionRulesInput{
		PositionAdjustments: []calc.PositionAdjustmentRuleInput{{
			Mode: "transfer", Amount: 200, SourceIDs: []string{f.pos1ID}, TargetIDs: []string{f.pos2ID},
		}},
	}
	repo := NewRedistributionRepo(pool)
	out, err := repo.SaveAuthoritative(context.Background(), f.tenderID, f.tacticID, rules, rbActor)
	if err != nil {
		t.Fatalf("position-only save failed: %v", err)
	}
	rows := readRdRows(t, pool, f.tenderID, f.tacticID)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want the COMPLETE no-op set (no client placeholder)", len(rows))
	}
	r1 := rows[f.item1ID]
	if r1.ded != 0 || r1.add != 0 || r1.orig != 1000 || r1.final != 1000 {
		t.Fatalf("no-op row wrong: %+v", r1)
	}
	// Rules persisted with the adjustments + server metadata.
	holder := f.item1ID
	if f.item2ID < holder {
		holder = f.item2ID
	}
	var meta struct {
		PositionAdjustments []calc.PositionAdjustmentRuleInput `json:"position_adjustments"`
		CalculationSource   string                             `json:"calculation_source"`
	}
	if err := json.Unmarshal(rows[holder].rules, &meta); err != nil {
		t.Fatalf("rules JSON: %v", err)
	}
	if len(meta.PositionAdjustments) != 1 || meta.CalculationSource != "server" {
		t.Fatalf("persisted rules wrong: %+v", meta)
	}
	// Diagnostic deltas returned (0.1.2.3b preparation): -200 on p1, +200 on p2.
	if d := out.PositionDeltas[f.pos1ID]; d != -200 {
		t.Fatalf("delta p1 = %v, want -200", d)
	}
	if d := out.PositionDeltas[f.pos2ID]; d != 200 {
		t.Fatalf("delta p2 = %v, want 200", d)
	}
}

// ─── H. Legacy snapshot → requires_recalculation ─────────────────────────────

func TestRedistributionLoad_LegacySnapshotRequiresRecalculation(t *testing.T) {
	pool := newTestPool(t)
	f := seedRedistributionFixture(t, pool, "legacy", nil)
	f.seedTwoItems(t, pool)

	// A legacy client-calculated row: rules WITHOUT server metadata.
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO public.cost_redistribution_results
		  (tender_id, markup_tactic_id, boq_item_id, original_work_cost,
		   deducted_amount, added_amount, final_work_cost, redistribution_rules)
		VALUES ($1::uuid,$2::uuid,$3::uuid, 777, 1, 2, 778,
		        '{"deductions":[],"targets":[]}'::jsonb)`,
		f.tenderID, f.tacticID, f.item1ID); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	repo := NewRedistributionRepo(pool)
	load, err := repo.LoadResults(context.Background(), f.tenderID, f.tacticID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if load.Status != RedistributionStatusRequiresRecalculation {
		t.Fatalf("status = %q, want requires_recalculation", load.Status)
	}
	if len(load.Results) != 1 {
		t.Fatalf("legacy rows must still be returned for inspection, got %d", len(load.Results))
	}
}

// ─── Очистка снимка: все правила удалены на странице ─────────────────────────

func TestRedistributionClear_DeletesSnapshotAndBumpsRevision(t *testing.T) {
	pool := newTestPool(t)
	f := seedRedistributionFixture(t, pool, "clear", nil)
	f.seedTwoItems(t, pool)
	ctx := context.Background()

	repo := NewRedistributionRepo(pool)
	if _, err := repo.SaveAuthoritative(ctx, f.tenderID, f.tacticID, f.d1toD2Rules(), rbActor); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	approveDirect(t, pool, f.tenderID)
	before := readFinState(t, pool, f.tenderID)

	deleted, err := repo.ClearAuthoritative(ctx, f.tenderID, f.tacticID)
	if err != nil {
		t.Fatalf("clear failed: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
	if rows := readRdRows(t, pool, f.tenderID, f.tacticID); len(rows) != 0 {
		t.Fatalf("snapshot rows survived the clear: %v", rows)
	}
	load, err := repo.LoadResults(ctx, f.tenderID, f.tacticID)
	if err != nil || load.Status != RedistributionStatusNotConfigured {
		t.Fatalf("status = %q err=%v, want not_configured", load.Status, err)
	}
	after := readFinState(t, pool, f.tenderID)
	if after.inputRev != before.inputRev+1 || after.calcRev != after.inputRev || after.status != "calculated" {
		t.Fatalf("revision state wrong: before %+v after %+v", before, after)
	}
	if after.approved {
		t.Fatal("clearing a snapshot must invalidate the financial approval")
	}

	// Повторная очистка — no-op: ревизия и согласование не трогаются.
	approveDirect(t, pool, f.tenderID)
	deleted, err = repo.ClearAuthoritative(ctx, f.tenderID, f.tacticID)
	if err != nil || deleted != 0 {
		t.Fatalf("repeat clear: deleted=%d err=%v, want 0/nil", deleted, err)
	}
	again := readFinState(t, pool, f.tenderID)
	if again.inputRev != after.inputRev || !again.approved {
		t.Fatalf("no-op clear changed tender state: %+v → %+v", after, again)
	}
}

func TestRedistributionClear_UnknownTender(t *testing.T) {
	pool := newTestPool(t)
	repo := NewRedistributionRepo(pool)
	_, err := repo.ClearAuthoritative(context.Background(),
		"00000000-0000-0000-0000-00000000c1ea", "00000000-0000-0000-0000-00000000c1eb")
	if !errors.Is(err, ErrRedistributionTenderNotFound) {
		t.Fatalf("err = %v, want ErrRedistributionTenderNotFound", err)
	}
}
