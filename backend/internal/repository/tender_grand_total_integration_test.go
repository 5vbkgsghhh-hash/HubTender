package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/su10/hubtender/backend/internal/calc"
)

// PostgreSQL integration tests for stage 0.1.2.4a: the canonical
// cached_grand_total pipeline (Go/calc helper + SQL retirement). Reuses
// newTestPool / HUBTENDER_TEST_DATABASE_URL; SKIPs without a test DB.
//
//	HUBTENDER_TEST_DATABASE_URL='postgres://…/hubtender_test?sslmode=disable' \
//	  go test ./internal/repository/ -run CachedGrandTotal -v

type cgtFixture struct {
	tenderID string
	posID    string
	item1ID  string
	item2ID  string
}

// seedCachedGrandTotalFixture creates a bare tender + position + two BOQ rows
// with KNOWN materialized commercial values (mat 100+200, work 300+400).
func seedCachedGrandTotalFixture(t *testing.T, pool *pgxpool.Pool, tag string) *cgtFixture {
	t.Helper()
	ctx := context.Background()
	f := &cgtFixture{}
	scan := func(dst *string, q string, args ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, q, args...).Scan(dst); err != nil {
			t.Fatalf("fixture: %v\nSQL: %s", err, q)
		}
	}
	scan(&f.tenderID, `INSERT INTO public.tenders (title, client_name, tender_number)
		VALUES ($1,'itest-client',$2) RETURNING id::text`, "itest-cgt-"+tag, "ITEST-CGT-"+tag)
	scan(&f.posID, `INSERT INTO public.client_positions (tender_id, position_number, work_name)
		VALUES ($1::uuid, 1, 'p') RETURNING id::text`, f.tenderID)
	workNameID, _ := ensureTestNames(t, pool)
	addItem := func(mat, work float64) string {
		var id string
		scan(&id, `INSERT INTO public.boq_items
			(client_position_id, tender_id, boq_item_type, work_name_id,
			 total_commercial_material_cost, total_commercial_work_cost)
			VALUES ($1::uuid,$2::uuid,'раб',$3::uuid,$4,$5) RETURNING id::text`,
			f.posID, f.tenderID, workNameID, mat, work)
		return id
	}
	f.item1ID = addItem(100, 300)
	f.item2ID = addItem(200, 400)

	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.boq_items_audit WHERE boq_item_id IN
			(SELECT id FROM public.boq_items WHERE tender_id=$1::uuid)`, f.tenderID)
		_, _ = pool.Exec(ctx, `DELETE FROM public.boq_items WHERE tender_id=$1::uuid`, f.tenderID)
		_, _ = pool.Exec(ctx, `DELETE FROM public.client_positions WHERE tender_id=$1::uuid`, f.tenderID)
		_, _ = pool.Exec(ctx, `DELETE FROM public.tender_insurance WHERE tender_id=$1::uuid`, f.tenderID)
		_, _ = pool.Exec(ctx, `DELETE FROM public.tenders WHERE id=$1::uuid`, f.tenderID)
	})
	return f
}

func readCachedTotal(t *testing.T, pool *pgxpool.Pool, tenderID string) float64 {
	t.Helper()
	var v float64
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(cached_grand_total,0) FROM public.tenders WHERE id=$1::uuid`, tenderID).Scan(&v); err != nil {
		t.Fatalf("read total: %v", err)
	}
	return v
}

func readCachedBaseTotal(t *testing.T, pool *pgxpool.Pool, tenderID string) string {
	t.Helper()
	var v string
	if err := pool.QueryRow(context.Background(),
		`SELECT cached_base_total::text FROM public.tenders WHERE id=$1::uuid`, tenderID).Scan(&v); err != nil {
		t.Fatalf("read base total: %v", err)
	}
	return v
}

func liveBaseTotal(t *testing.T, pool *pgxpool.Pool, tenderID string) string {
	t.Helper()
	var v string
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(COALESCE(total_amount,0)),0)::text FROM public.boq_items WHERE tender_id=$1::uuid`, tenderID).Scan(&v); err != nil {
		t.Fatalf("live base total: %v", err)
	}
	return v
}

// runHelper recomputes via the canonical helper inside a committed tx.
func runHelper(t *testing.T, pool *pgxpool.Pool, tenderID string) *calc.CachedTenderGrandTotalResult {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	res, err := RecalculateTenderGrandTotalTx(ctx, tx, tenderID)
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("helper: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return res
}

// ─── A/I. Commercial base + rounding parity with the persisted value ─────────

func TestCachedGrandTotal_HelperMatchesCalcAndPersists(t *testing.T) {
	pool := newTestPool(t)
	f := seedCachedGrandTotalFixture(t, pool, "base")

	res := runHelper(t, pool, f.tenderID)
	if res.RoundedTotal != 1000 { // 100+200+300+400
		t.Fatalf("total = %v, want 1000", res.RoundedTotal)
	}
	if got := readCachedTotal(t, pool, f.tenderID); got != res.RoundedTotal {
		t.Fatalf("persisted %v != calc %v", got, res.RoundedTotal)
	}
	// Rounding parity on a fractional boundary (float-representable case).
	if _, err := pool.Exec(context.Background(),
		`UPDATE public.boq_items SET total_commercial_work_cost = 300.456 WHERE id=$1::uuid`, f.item1ID); err != nil {
		t.Fatalf("update: %v", err)
	}
	res2 := runHelper(t, pool, f.tenderID)
	if got := readCachedTotal(t, pool, f.tenderID); got != res2.RoundedTotal {
		t.Fatalf("persisted %v != calc %v (rounding parity)", got, res2.RoundedTotal)
	}
	if res2.RoundedTotal != 1000.46 { // 1000.456 → 1000.46
		t.Fatalf("rounded = %v, want 1000.46", res2.RoundedTotal)
	}
}

// ─── B. Insurance save/update via the production Upsert path ─────────────────

func TestCachedGrandTotal_InsuranceUpsertRecalculatesSynchronously(t *testing.T) {
	pool := newTestPool(t)
	f := seedCachedGrandTotalFixture(t, pool, "ins")
	runHelper(t, pool, f.tenderID) // baseline 1000

	repo := NewInsuranceRepo(pool)
	// save: insurance (10×10)×50%×100% = 50 → 1050, no double count.
	if _, err := repo.Upsert(context.Background(), f.tenderID, InsuranceRow{
		JudicialPct: 50, TotalPct: 100, AptPriceM2: 10, AptArea: 10,
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if got := readCachedTotal(t, pool, f.tenderID); got != 1050 {
		t.Fatalf("after save = %v, want 1050", got)
	}
	// update: 100% → 100 → 1100.
	if _, err := repo.Upsert(context.Background(), f.tenderID, InsuranceRow{
		JudicialPct: 100, TotalPct: 100, AptPriceM2: 10, AptArea: 10,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := readCachedTotal(t, pool, f.tenderID); got != 1100 {
		t.Fatalf("after update = %v, want 1100 (no double count)", got)
	}
	// invalid config → rollback: total unchanged.
	_, err := repo.Upsert(context.Background(), f.tenderID, InsuranceRow{
		JudicialPct: 150, TotalPct: 100, AptPriceM2: 10, AptArea: 10,
	})
	var insErr *calc.InvalidInsuranceConfigurationError
	if !errors.As(err, &insErr) {
		t.Fatalf("want InvalidInsuranceConfigurationError, got %v", err)
	}
	if got := readCachedTotal(t, pool, f.tenderID); got != 1100 {
		t.Fatalf("total changed after rejected insurance: %v", got)
	}
	// manual delete + helper → insurance contribution gone.
	if _, err := pool.Exec(context.Background(),
		`DELETE FROM public.tender_insurance WHERE tender_id=$1::uuid`, f.tenderID); err != nil {
		t.Fatalf("delete insurance: %v", err)
	}
	res := runHelper(t, pool, f.tenderID)
	if res.RoundedTotal != 1000 || res.InsuranceTotal != 0 {
		t.Fatalf("after delete: %v (ins %v), want 1000/0", res.RoundedTotal, res.InsuranceTotal)
	}
}

// ─── C. BOQ deletion paths recompute in the same tx ──────────────────────────

func TestCachedGrandTotal_BoqDeletePathsRecalculate(t *testing.T) {
	pool := newTestPool(t)
	f := seedCachedGrandTotalFixture(t, pool, "del")
	runHelper(t, pool, f.tenderID) // 1000

	// production single delete (DeleteBoqItem): −(200+400) = 400.
	boqRepo := NewBoqRepo(pool)
	if _, err := boqRepo.DeleteBoqItem(context.Background(), f.item2ID, rbActor); err != nil {
		t.Fatalf("delete item: %v", err)
	}
	if got := readCachedTotal(t, pool, f.tenderID); got != 400 {
		t.Fatalf("after single delete = %v, want 400", got)
	}

	// batch path (ClearPositionsBoq) → 0, recomputed ONCE per tender.
	posRepo := NewPositionRepo(pool)
	if err := posRepo.ClearPositionsBoq(context.Background(), []string{f.posID}, rbActor); err != nil {
		t.Fatalf("clear positions: %v", err)
	}
	if got := readCachedTotal(t, pool, f.tenderID); got != 0 {
		t.Fatalf("after clear = %v, want 0", got)
	}
}

// ─── D. Rollback keeps commercial/insurance/total untouched ──────────────────

func TestCachedGrandTotal_RollbackKeepsTotal(t *testing.T) {
	pool := newTestPool(t)
	f := seedCachedGrandTotalFixture(t, pool, "rb")
	runHelper(t, pool, f.tenderID) // 1000

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE public.boq_items SET total_commercial_work_cost = 999999 WHERE id=$1::uuid`, f.item1ID); err != nil {
		t.Fatalf("update: %v", err)
	}
	if _, err := RecalculateTenderGrandTotalTx(ctx, tx, f.tenderID); err != nil {
		t.Fatalf("helper in tx: %v", err)
	}
	_ = tx.Rollback(ctx) // error before commit → everything reverts
	if got := readCachedTotal(t, pool, f.tenderID); got != 1000 {
		t.Fatalf("total after rollback = %v, want 1000", got)
	}
}

// ─── J. Unknown tender → typed not-found; others untouched ───────────────────

func TestCachedGrandTotal_UnknownTender(t *testing.T) {
	pool := newTestPool(t)
	f := seedCachedGrandTotalFixture(t, pool, "unk")
	runHelper(t, pool, f.tenderID)

	ctx := context.Background()
	tx, _ := pool.Begin(ctx)
	defer tx.Rollback(ctx) //nolint:errcheck
	_, err := RecalculateTenderGrandTotalTx(ctx, tx, "ffffffff-ffff-ffff-ffff-ffffffffffff")
	var nf *CachedGrandTotalTenderNotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("want CachedGrandTotalTenderNotFoundError, got %v", err)
	}
	if got := readCachedTotal(t, pool, f.tenderID); got != 1000 {
		t.Fatalf("other tender changed: %v", got)
	}
}

// ─── E/F/G/H. SQL retirement: tombstone, triggers gone, helper unaffected ────

// legacyGrandTotalSQL — the PRE-0.1.2.4a definitions (verbatim) so the
// migration is exercised against a REAL old installation.
const legacyGrandTotalSQL = `
CREATE OR REPLACE FUNCTION public.recalculate_tender_grand_total(p_tender_id uuid)
 RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp
AS $function$
DECLARE
  v_boq_total NUMERIC; v_insurance NUMERIC; v_grand_total NUMERIC;
BEGIN
  SELECT COALESCE(SUM(total_commercial_material_cost + total_commercial_work_cost), 0)
    INTO v_boq_total FROM public.boq_items WHERE tender_id = p_tender_id;
  SELECT COALESCE((apt_price_m2*apt_area + parking_price_m2*parking_area + storage_price_m2*storage_area)
    * (judicial_pct/100.0) * (total_pct/100.0), 0)
    INTO v_insurance FROM public.tender_insurance WHERE tender_id = p_tender_id LIMIT 1;
  v_grand_total := v_boq_total + COALESCE(v_insurance, 0);
  UPDATE public.tenders SET cached_grand_total = ROUND(v_grand_total, 2) WHERE id = p_tender_id;
END;
$function$;

CREATE OR REPLACE FUNCTION public.trg_boq_items_update_grand_total()
 RETURNS trigger LANGUAGE plpgsql SET search_path = public, pg_temp
AS $function$
BEGIN
  IF current_setting('app.skip_grand_total', true) = 'on' THEN RETURN COALESCE(NEW, OLD); END IF;
  IF TG_OP = 'DELETE' THEN PERFORM public.recalculate_tender_grand_total(OLD.tender_id); RETURN OLD; END IF;
  PERFORM public.recalculate_tender_grand_total(NEW.tender_id);
  RETURN NEW;
END;
$function$;

DROP TRIGGER IF EXISTS trg_boq_items_grand_total ON public.boq_items;
CREATE TRIGGER trg_boq_items_grand_total
  AFTER INSERT OR DELETE OR UPDATE OF total_amount ON public.boq_items
  FOR EACH ROW EXECUTE FUNCTION public.trg_boq_items_update_grand_total();
`

func TestCachedGrandTotal_SQLRetirement(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	// Real old installation → retirement migration, applied TWICE (§18 H).
	if _, err := tx.Exec(ctx, legacyGrandTotalSQL); err != nil {
		t.Fatalf("install legacy: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "db", "yandex", "incremental",
		"2026_07_retire_sql_grand_total_recalc.sql"))
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	mig := regexp.MustCompile(`(?m)^(BEGIN|COMMIT);\s*$`).ReplaceAllString(string(raw), "")
	for i := 1; i <= 2; i++ {
		if _, err := tx.Exec(ctx, mig); err != nil {
			t.Fatalf("apply migration (attempt %d): %v", i, err)
		}
	}

	// E. Tombstone blocks every payload.
	call := func(arg string) *pgconn.PgError {
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatalf("savepoint: %v", err)
		}
		defer sp.Rollback(ctx) //nolint:errcheck
		_, err = sp.Exec(ctx, "SELECT public.recalculate_tender_grand_total("+arg+")")
		if err == nil {
			t.Fatalf("tombstone call (%s) succeeded", arg)
		}
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) {
			t.Fatalf("want PgError, got %v", err)
		}
		return pgErr
	}
	for name, arg := range map[string]string{
		"valid uuid":   `'11111111-1111-1111-1111-111111111111'::uuid`,
		"NULL":         `NULL::uuid`,
		"unknown uuid": `'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid`,
	} {
		pgErr := call(arg)
		if pgErr.Code != "0A000" || !strings.Contains(pgErr.Message, "GRAND_TOTAL_SQL_RETIRED") {
			t.Fatalf("%s: SQLSTATE=%s msg=%q", name, pgErr.Code, pgErr.Message)
		}
	}

	// F. Triggers + trigger functions gone.
	var trgCount, fnCount int
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgname IN
		 ('trg_boq_items_grand_total','trg_insurance_grand_total',
		  'trg_markup_pct_grand_total','trg_subcontract_excl_grand_total')`).Scan(&trgCount); err != nil {
		t.Fatalf("pg_trigger: %v", err)
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
		WHERE n.nspname='public' AND p.proname IN
		 ('trg_boq_items_update_grand_total','trg_insurance_update_grand_total',
		  'trg_markup_pct_update_grand_total','trg_subcontract_excl_update_grand_total')`).Scan(&fnCount); err != nil {
		t.Fatalf("pg_proc: %v", err)
	}
	if trgCount != 0 || fnCount != 0 {
		t.Fatalf("triggers/functions survive: %d/%d", trgCount, fnCount)
	}

	// ACL closed; tombstone properties.
	var secdef, isStrict, publicHas bool
	if err := tx.QueryRow(ctx, `
		SELECT p.prosecdef, p.proisstrict,
		       EXISTS (SELECT 1 FROM aclexplode(coalesce(p.proacl,'{}'::aclitem[])) a WHERE a.grantee = 0)
		FROM pg_proc p WHERE p.oid = 'public.recalculate_tender_grand_total(uuid)'::regprocedure`,
	).Scan(&secdef, &isStrict, &publicHas); err != nil {
		t.Fatalf("pg_proc tombstone: %v", err)
	}
	if secdef || isStrict || publicHas {
		t.Fatalf("tombstone props wrong: secdef=%v strict=%v public=%v", secdef, isStrict, publicHas)
	}

	// G. The Go helper keeps working inside the SAME retired-state tx.
	var tenderID, posID string
	if err := tx.QueryRow(ctx, `INSERT INTO public.tenders (title, client_name, tender_number)
		VALUES ('itest-cgt-post','c','ITEST-CGT-POST') RETURNING id::text`).Scan(&tenderID); err != nil {
		t.Fatalf("seed tender: %v", err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO public.client_positions (tender_id, position_number, work_name)
		VALUES ($1::uuid,1,'p') RETURNING id::text`, tenderID).Scan(&posID); err != nil {
		t.Fatalf("seed pos: %v", err)
	}
	wnID, _ := ensureTestNames(t, pool)
	if _, err := tx.Exec(ctx, `INSERT INTO public.boq_items
		(client_position_id, tender_id, boq_item_type, work_name_id, total_commercial_material_cost, total_commercial_work_cost)
		VALUES ($1::uuid,$2::uuid,'раб',$3::uuid,10,20)`, posID, tenderID, wnID); err != nil {
		t.Fatalf("seed item: %v", err)
	}
	res, err := RecalculateTenderGrandTotalTx(ctx, tx, tenderID)
	if err != nil {
		t.Fatalf("helper after retirement: %v", err)
	}
	if res.RoundedTotal != 30 {
		t.Fatalf("helper total = %v, want 30", res.RoundedTotal)
	}
	var persisted float64
	if err := tx.QueryRow(ctx,
		`SELECT cached_grand_total FROM public.tenders WHERE id=$1::uuid`, tenderID).Scan(&persisted); err != nil {
		t.Fatalf("read: %v", err)
	}
	if persisted != 30 {
		t.Fatalf("persisted = %v, want 30", persisted)
	}
}

// ─── K. Stage 0.1.2.4a.1 §9: decimal rounding parity with PostgreSQL ─────────
//
// Boundary fixtures are seeded EXCLUSIVELY via string binds / numeric literals
// (never a float64 bind), then three values must agree byte-for-byte:
//
//	calc RoundedTotalDecimal == SELECT ROUND($1::numeric, 2)::text == persisted ::text

func TestCachedGrandTotal_DecimalRoundingParity(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	type insFixture struct{ judicial, total, aptPrice, aptArea string }
	cases := []struct {
		name        string
		boqRows     [][2]string // {material, work} as exact decimal strings
		insurance   *insFixture
		beforeExact string // known exact pre-rounding sum (test-controlled)
		want        string // canonical ROUND(…, 2) result
	}{
		{"0.005 half-cent up", [][2]string{{"0.005", "0"}}, nil, "0.005", "0.01"},
		{"1.005 canonical bugfix", [][2]string{{"1.005", "0"}}, nil, "1.005", "1.01"},
		{"1.015", [][2]string{{"0", "1.015"}}, nil, "1.015", "1.02"},
		{"2.675 binary trap", [][2]string{{"2.675", "0"}}, nil, "2.675", "2.68"},
		{"100.555", [][2]string{{"100", "0.555"}}, nil, "100.555", "100.56"},
		{"multi-row aggregate half-cent", [][2]string{{"0.0025", "0"}, {"0.0025", "0"}}, nil, "0.005", "0.01"},
		{"commercial+insurance half-cent", [][2]string{{"1.00", "0"}},
			&insFixture{judicial: "1", total: "1", aptPrice: "50", aptArea: "1"}, "1.005", "1.01"},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var tenderID, posID string
			tag := "PARITY-" + string(rune('A'+i))
			if err := pool.QueryRow(ctx, `INSERT INTO public.tenders (title, client_name, tender_number)
				VALUES ($1,'itest-client',$2) RETURNING id::text`,
				"itest-cgt-parity-"+tag, "ITEST-CGT-"+tag).Scan(&tenderID); err != nil {
				t.Fatalf("seed tender: %v", err)
			}
			t.Cleanup(func() {
				_, _ = pool.Exec(ctx, `DELETE FROM public.boq_items WHERE tender_id=$1::uuid`, tenderID)
				_, _ = pool.Exec(ctx, `DELETE FROM public.client_positions WHERE tender_id=$1::uuid`, tenderID)
				_, _ = pool.Exec(ctx, `DELETE FROM public.tender_insurance WHERE tender_id=$1::uuid`, tenderID)
				_, _ = pool.Exec(ctx, `DELETE FROM public.tenders WHERE id=$1::uuid`, tenderID)
			})
			if err := pool.QueryRow(ctx, `INSERT INTO public.client_positions (tender_id, position_number, work_name)
				VALUES ($1::uuid, 1, 'p') RETURNING id::text`, tenderID).Scan(&posID); err != nil {
				t.Fatalf("seed position: %v", err)
			}
			parityWorkName, _ := ensureTestNames(t, pool)
			for _, row := range tc.boqRows {
				// String binds → NUMERIC: the exact decimal reaches the DB untouched.
				if _, err := pool.Exec(ctx, `INSERT INTO public.boq_items
					(client_position_id, tender_id, boq_item_type, work_name_id,
					 total_commercial_material_cost, total_commercial_work_cost)
					VALUES ($1::uuid,$2::uuid,'раб',$3::uuid,$4::numeric,$5::numeric)`,
					posID, tenderID, parityWorkName, row[0], row[1]); err != nil {
					t.Fatalf("seed boq row: %v", err)
				}
			}
			if tc.insurance != nil {
				if _, err := pool.Exec(ctx, `INSERT INTO public.tender_insurance
					(tender_id, judicial_pct, total_pct, apt_price_m2, apt_area)
					VALUES ($1::uuid,$2::numeric,$3::numeric,$4::numeric,$5::numeric)`,
					tenderID, tc.insurance.judicial, tc.insurance.total,
					tc.insurance.aptPrice, tc.insurance.aptArea); err != nil {
					t.Fatalf("seed insurance: %v", err)
				}
			}

			res := runHelper(t, pool, tenderID)
			if res.RoundedTotalDecimal != tc.want {
				t.Fatalf("calc = %q, want %q", res.RoundedTotalDecimal, tc.want)
			}
			// PostgreSQL's own ROUND over the SAME exact pre-rounding value.
			var sqlRound string
			if err := pool.QueryRow(ctx,
				`SELECT ROUND($1::numeric, 2)::text`, tc.beforeExact).Scan(&sqlRound); err != nil {
				t.Fatalf("sql round: %v", err)
			}
			if sqlRound != tc.want {
				t.Fatalf("SELECT ROUND(%s,2) = %q, want %q (calc/SQL policy diverged)", tc.beforeExact, sqlRound, tc.want)
			}
			// Persisted value, byte-for-byte (never read back through float64).
			var persisted string
			if err := pool.QueryRow(ctx,
				`SELECT cached_grand_total::text FROM public.tenders WHERE id=$1::uuid`, tenderID).Scan(&persisted); err != nil {
				t.Fatalf("read persisted: %v", err)
			}
			if persisted != tc.want {
				t.Fatalf("persisted = %q, want %q", persisted, tc.want)
			}
		})
	}
}


// ─── cached_base_total: the helper persists SUM(total_amount) in the same write ─

// The tender list reads tenders.cached_base_total instead of aggregating
// boq_items per request; the helper is its only writer.
func TestCachedBaseTotal_HelperPersistsSumOfTotalAmount(t *testing.T) {
	pool := newTestPool(t)
	f := seedCachedGrandTotalFixture(t, pool, "basetotal")
	ctx := context.Background()

	if _, err := pool.Exec(ctx,
		`UPDATE public.boq_items SET total_amount = 1234.5678 WHERE id=$1::uuid`, f.item1ID); err != nil {
		t.Fatalf("update total_amount: %v", err)
	}
	runHelper(t, pool, f.tenderID)
	want := liveBaseTotal(t, pool, f.tenderID)
	if got := readCachedBaseTotal(t, pool, f.tenderID); got != want {
		t.Fatalf("cached_base_total = %s, live SUM(total_amount) = %s", got, want)
	}

	// Rows removed → the stored total follows on the next helper run.
	if _, err := pool.Exec(ctx, `DELETE FROM public.boq_items_audit WHERE boq_item_id=$1::uuid`, f.item1ID); err != nil {
		t.Fatalf("delete audit: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM public.boq_items WHERE id=$1::uuid`, f.item1ID); err != nil {
		t.Fatalf("delete item: %v", err)
	}
	runHelper(t, pool, f.tenderID)
	want = liveBaseTotal(t, pool, f.tenderID)
	if got := readCachedBaseTotal(t, pool, f.tenderID); got != want {
		t.Fatalf("after delete: cached_base_total = %s, live = %s", got, want)
	}
}
