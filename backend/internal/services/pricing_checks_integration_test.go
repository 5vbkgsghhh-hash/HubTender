//go:build integration

package services

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/repository"
)

const checksTender = "dddddddd-1000-0000-0000-000000000001"
const checksPosition = "dddddddd-2000-0000-0000-000000000001"
const checksWorkName = "dddddddd-3000-0000-0000-000000000001"
const checksWorkCard = "dddddddd-4000-0000-0000-000000000001"
const checksWork = "dddddddd-5000-0000-0000-000000000001"
const checksMaterial = "dddddddd-5000-0000-0000-000000000002"

func checksFixture(t *testing.T) (context.Context, *pgxpool.Pool, *PricingService, pricing.Principal, DirectPricingInput) {
	t.Helper()
	ctx, pool, svc, p := reviewPricingService(t, nil)
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`DELETE FROM public.boq_items_audit WHERE new_data->>'tender_id'=$1 OR old_data->>'tender_id'=$1`, checksTender)
	must(`INSERT INTO public.tenders(id,title,client_name,tender_number,version) VALUES($1,'MCP checks','Eval','MCP-CHECKS',1)`, checksTender)
	must(`INSERT INTO public.client_positions(id,tender_id,position_number,work_name,unit_code,volume,manual_volume) VALUES($1,$2,1,'MCP checks work','шт',10,10)`, checksPosition, checksTender)
	must(`INSERT INTO public.work_names(id,name,unit) VALUES($1,'MCP checks work','шт')`, checksWorkName)
	must(`INSERT INTO public.works_library(id,work_name_id,item_type,unit_rate,currency_type) VALUES($1,$2,'суб-раб',100,'RUB')`, checksWorkCard, checksWorkName)
	t.Cleanup(func() {
		must(`DELETE FROM public.mcp_direct_pricing_requests WHERE tender_id=$1`, checksTender)
		must(`DELETE FROM public.boq_items WHERE tender_id=$1`, checksTender)
		must(`DELETE FROM public.position_pricing_owners WHERE client_position_id IN (SELECT id FROM public.client_positions WHERE tender_id=$1)`, checksTender)
		must(`DELETE FROM public.client_position_pricing_audit WHERE tender_id=$1`, checksTender)
		must(`DELETE FROM public.client_positions WHERE tender_id=$1`, checksTender)
		must(`DELETE FROM public.tenders WHERE id=$1`, checksTender)
		must(`DELETE FROM public.boq_items_audit WHERE new_data->>'tender_id'=$1 OR old_data->>'tender_id'=$1`, checksTender)
		must(`DELETE FROM public.works_library WHERE id=$1`, checksWorkCard)
		must(`DELETE FROM public.work_names WHERE id=$1`, checksWorkName)
	})
	source, err := repository.NewPricingRepo(pool).GetLibraryItem(ctx, checksWorkCard, "work")
	if err != nil {
		t.Fatal(err)
	}
	q := 10.0
	in := DirectPricingInput{TenderID: checksTender, TargetPositionID: checksPosition, SourceKind: "library", LibraryKind: "work", SourceID: checksWorkCard, ExpectedSourceRate: 100, ExpectedSourceVersion: source.SourceVersion, DetailCostCategoryID: stringPtr("eeeeeeee-0000-0000-0000-000000000002"), Quantity: &q, RequestKey: "mcp-checks-direct-001", Confirm: true}
	return ctx, pool, svc, p, in
}

func checkHas(out *pricing.DirectPricingResult, code string) bool {
	if out == nil || out.Checks == nil {
		return false
	}
	for _, f := range out.Checks.Findings {
		if f.RuleCode == code {
			return true
		}
	}
	return false
}

func TestMCPPricingPreviewIsRolledBackAndWarningsBindConsent(t *testing.T) {
	ctx, pool, svc, p, in := checksFixture(t)
	q, gp := 1.0, 20.0
	in.Quantity, in.PositionQuantity = &q, &gp // R: work=1, GP>10
	before := func() (int, int, int, int64, float64) {
		var items, audits, receipts int
		var rev int64
		var volume float64
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM public.boq_items WHERE tender_id=$1),(SELECT count(*) FROM public.boq_items_audit WHERE new_data->>'tender_id'=$1::text),(SELECT count(*) FROM public.mcp_direct_pricing_requests WHERE tender_id=$1),financial_input_revision,(SELECT manual_volume FROM public.client_positions WHERE id=$2) FROM public.tenders WHERE id=$1`, checksTender, checksPosition).Scan(&items, &audits, &receipts, &rev, &volume); err != nil {
			t.Fatal(err)
		}
		return items, audits, receipts, rev, volume
	}
	preview, err := svc.PreviewDirectPrice(ctx, p, in)
	if err != nil || !checkHas(preview, "R") {
		t.Fatalf("preview=%+v err=%v", preview, err)
	}
	if a, b, c, d, e := before(); a != 0 || b != 0 || c != 0 || d != 0 || e != 10 {
		t.Fatalf("preview persisted state: %d %d %d %d %v", a, b, c, d, e)
	}
	if _, err := svc.ApplyDirectPrice(ctx, p, in); err == nil || !strings.Contains(err.Error(), "CHECKS_CHANGED") {
		t.Fatalf("unacknowledged warning committed: %v", err)
	}
	if a, _, c, d, e := before(); a != 0 || c != 0 || d != 0 || e != 10 {
		t.Fatal("failed confirmation changed state")
	}
	// A change after the preview, even one made without a financial revision
	// bump by a legacy path, must invalidate the exact findings acknowledged.
	if _, err := pool.Exec(ctx, `UPDATE public.client_positions SET volume=1.5 WHERE id=$1`, checksPosition); err != nil {
		t.Fatal(err)
	}
	in.ExpectedChecksFingerprint = preview.Checks.Fingerprint
	if _, err := svc.ApplyDirectPrice(ctx, p, in); err == nil || !strings.Contains(err.Error(), "CHECKS_CHANGED") {
		t.Fatalf("changed findings accepted: %v", err)
	}
	preview, err = svc.PreviewDirectPrice(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	in.ExpectedChecksFingerprint = preview.Checks.Fingerprint
	written, err := svc.ApplyDirectPrice(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	if written.PositionQuantity == nil || *written.PositionQuantity != 20 || written.PositionTotal != 100 {
		t.Fatalf("result=%+v", written)
	}
	if _, err := svc.ApplyDirectPrice(ctx, p, in); err != nil {
		t.Fatalf("receipt replay: %v", err)
	}
	if a, b, c, d, e := before(); a != 1 || b != 1 || c != 1 || d != 1 || e != 20 {
		t.Fatalf("commit/replay state: %d %d %d %d %v", a, b, c, d, e)
	}
}

func TestMCPPricingBlocksMissingGPAndExcludesOtherPositions(t *testing.T) {
	ctx, pool, svc, p, in := checksFixture(t)
	if _, err := pool.Exec(ctx, `UPDATE public.client_positions SET manual_volume=NULL WHERE id=$1`, checksPosition); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.PreviewDirectPrice(ctx, p, in)
	if err != nil || !checkHas(preview, "U") || !preview.Checks.Blocking {
		t.Fatalf("U: result=%+v err=%v", preview, err)
	}
	if _, err := svc.ApplyDirectPrice(ctx, p, in); err == nil || !strings.Contains(err.Error(), "U:") {
		t.Fatalf("missing GP committed: %v", err)
	}
	gp := 10.0
	in.PositionQuantity = &gp
	if _, err := svc.ApplyDirectPrice(ctx, p, in); err != nil {
		t.Fatalf("explicit GP did not fix U: %v", err)
	}
	// Existing evaluation tenders contain incomplete rows; their findings must
	// not block a clean write in this position.
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.boq_items WHERE tender_id=$1`, checksTender).Scan(&count); err != nil || count != 1 {
		t.Fatalf("items=%d err=%v", count, err)
	}
}

func TestMCPPricingRequiredFieldsAndLinkedQuantity(t *testing.T) {
	ctx, pool, svc, p, in := checksFixture(t)
	first, err := svc.ApplyDirectPrice(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	base := DirectPricingInput{TenderID: checksTender, TargetPositionID: checksPosition, TargetItemID: &first.ItemID, SourceKind: "current", Quantity: in.Quantity, ExpectedETag: &first.ETag, ExpectedRevision: 1, RequestKey: "mcp-checks-invalid-001", Confirm: true}
	for n, change := range []string{"unit_rate=NULL", "detail_cost_category_id=NULL", "unit_code=NULL", "boq_item_type='раб-комп.'"} {
		t.Run(change, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `UPDATE public.boq_items SET `+change+` WHERE id=$1`, first.ItemID); err != nil {
				t.Fatal(err)
			}
			_, etag, err := repository.NewPricingRepo(pool).GetCurrentBoqItem(ctx, first.ItemID)
			if err != nil {
				t.Fatal(err)
			}
			bad := base
			bad.ExpectedETag = &etag
			bad.RequestKey = fmt.Sprintf("mcp-checks-invalid-%03d", n)
			if _, err := svc.PreviewDirectPrice(ctx, p, bad); err == nil || !strings.Contains(err.Error(), "REQUIRED_FIELD") {
				t.Fatalf("accepted invalid %s: %v", change, err)
			}
			if _, err := pool.Exec(ctx, `UPDATE public.boq_items SET unit_rate=100,detail_cost_category_id='eeeeeeee-0000-0000-0000-000000000002',unit_code='шт',boq_item_type='суб-раб' WHERE id=$1`, first.ItemID); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMCPPricingLinkedMaterialsAndLegacyErrorsAreAtomic(t *testing.T) {
	ctx, pool, svc, p, in := checksFixture(t)
	work, err := svc.ApplyDirectPrice(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	source, err := svc.repo.GetArchiveItem(ctx, sourceMaterial)
	if err != nil {
		t.Fatal(err)
	}
	one := 1.0
	materialInput := DirectPricingInput{TenderID: checksTender, TargetPositionID: checksPosition, ParentWorkItemID: &work.ItemID, SourceKind: "archive", SourceID: sourceMaterial, ExpectedSourceRate: *source.UnitRate, ExpectedSourceVersion: source.SourceVersion, ConversionCoefficient: &one, ExpectedRevision: 1, RequestKey: "checks-linked-material-001", Confirm: true}
	material, err := svc.ApplyDirectPrice(ctx, p, materialInput)
	if err != nil {
		t.Fatal(err)
	}
	if material.Quantity != 10 {
		t.Fatalf("derived quantity %v", material.Quantity)
	}
	manual := materialInput
	manual.RequestKey = "checks-linked-manual-001"
	manual.Quantity = &one
	if _, err := svc.ApplyDirectPrice(ctx, p, manual); err == nil || !strings.Contains(err.Error(), "LINKED_MATERIAL_QUANTITY_READ_ONLY") {
		t.Fatalf("manual linked quantity accepted: %v", err)
	}
	five := 5.0
	update := DirectPricingInput{TenderID: checksTender, TargetPositionID: checksPosition, TargetItemID: &work.ItemID, SourceKind: "current", Quantity: &five, ExpectedETag: &work.ETag, ExpectedRevision: 2, RequestKey: "checks-parent-update-001", Confirm: true}
	changed, err := svc.ApplyDirectPrice(ctx, p, update)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed.LinkedMaterials) != 1 || changed.LinkedMaterials[0].Quantity != 5 || changed.PositionTotal != 5*(100+*source.UnitRate) {
		t.Fatalf("linked recompute: %+v", changed)
	}
	if checkHas(changed, "A") || checkHas(changed, "AB") || checkHas(changed, "I") || checkHas(changed, "J") {
		t.Fatalf("derived values failed checks: %+v", changed.Checks)
	}
	for _, tc := range []struct {
		code           string
		qty, total     float64
		delivery       string
		deliveryAmount float64
	}{
		{"A", 4, 4 * *source.UnitRate, "в цене", 0},
		{"AB", 50, 50 * *source.UnitRate, "в цене", 0},
		{"I", 5, 5**source.UnitRate + 1000, "в цене", 0},
		{"L", 5, 5 * *source.UnitRate, "суммой", 0},
	} {
		t.Run(tc.code, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `UPDATE public.boq_items SET quantity=$2,total_amount=$3,delivery_price_type=$4,delivery_amount=$5 WHERE id=$1`, material.ItemID, tc.qty, tc.total, tc.delivery, tc.deliveryAmount); err != nil {
				t.Fatal(err)
			}
			attempt := in
			attempt.ExpectedRevision = 3
			attempt.RequestKey = "checks-block-existing-" + tc.code
			preview, err := svc.PreviewDirectPrice(ctx, p, attempt)
			if err != nil || !checkHas(preview, tc.code) || !preview.Checks.Blocking {
				t.Fatalf("%s preview: %+v %v", tc.code, preview, err)
			}
			if _, err := svc.ApplyDirectPrice(ctx, p, attempt); err == nil || !strings.Contains(err.Error(), tc.code+":") {
				t.Fatalf("%s error did not block: %v", tc.code, err)
			}
			var count, receipts int
			var revision int64
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM public.boq_items WHERE tender_id=$1),(SELECT count(*) FROM public.mcp_direct_pricing_requests WHERE tender_id=$1),financial_input_revision FROM public.tenders WHERE id=$1`, checksTender).Scan(&count, &receipts, &revision); err != nil || count != 2 || receipts != 3 || revision != 3 {
				t.Fatalf("failed check changed state: %d %d %d %v", count, receipts, revision, err)
			}
		})
	}
}

func TestMCPPricingConsentBindsValuesEvenWithoutWarnings(t *testing.T) {
	ctx, pool, svc, p, in := checksFixture(t)
	preview, err := svc.PreviewDirectPrice(ctx, p, in)
	if err != nil || len(preview.Checks.Findings) != 0 {
		t.Fatalf("clean preview: %+v %v", preview, err)
	}
	in.ExpectedChecksFingerprint = preview.Checks.Fingerprint
	// Simulate a legacy position edit which did not bump financial revision.
	if _, err := pool.Exec(ctx, `UPDATE public.client_positions SET manual_volume=11 WHERE id=$1`, checksPosition); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ApplyDirectPrice(ctx, p, in); err == nil || !strings.Contains(err.Error(), "CHECKS_CHANGED") {
		t.Fatalf("changed GP accepted with old confirmation: %v", err)
	}
}
