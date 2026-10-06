//go:build integration

package services

import (
	"context"
	"errors"
	"math"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/su10/hubtender/backend/internal/mcpauth"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/repository"
)

func TestDirectPricingLinkedMaterialsAreDerivedAndAtomic(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	seedPricingActor(t, ctx, pool)
	defer cleanupPricingActor(ctx, pool)
	repo := repository.NewPricingRepo(pool)
	svc := NewPricingService(repo, repository.NewUserRepo(pool), repository.NewLibraryRepo(pool), mcpauth.NewRepository(pool), PricingFeatures{WriteEnabled: true})
	p := pricing.Principal{UserID: evalUser, ClientID: evalClient, Scopes: []string{mcpauth.ScopeTendersRead, mcpauth.ScopeArchiveRead, mcpauth.ScopeLibraryRead, mcpauth.ScopePricingWrite}}
	ptr := func(v float64) *float64 { return &v }
	category := "eeeeeeee-0000-0000-0000-000000000002"
	libraryMat := "eeeeeeee-6000-0000-0000-000000000002"
	apply := func(in DirectPricingInput) *pricing.DirectPricingResult {
		t.Helper()
		captureReviewSource(t, ctx, svc, &in)
		out, err := svc.ApplyDirectPrice(ctx, p, in)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	work := apply(DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, SourceKind: "archive", SourceID: sourceWork, ExpectedSourceRate: 1000, Quantity: ptr(10), RequestKey: "linked-create-work-001", Confirm: true})
	// Source recipes are inherited only at creation; later price changes must
	// preserve the target's stored consumption coefficient.
	if _, err := pool.Exec(ctx, `UPDATE public.boq_items SET consumption_coefficient=2 WHERE id=$1`, sourceMaterial); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.materials_library SET consumption_coefficient=1.5 WHERE id=$1`, libraryMat); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `UPDATE public.boq_items SET consumption_coefficient=1 WHERE id=$1`, sourceMaterial)
	defer pool.Exec(ctx, `UPDATE public.materials_library SET consumption_coefficient=1 WHERE id=$1`, libraryMat)
	newMat := DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, SourceKind: "archive", SourceID: sourceMaterial, ExpectedSourceRate: 2070000, ParentWorkItemID: &work.ItemID, ConversionCoefficient: ptr(.25), ExpectedRevision: 1, RequestKey: "linked-create-material-001", Confirm: true}
	captureReviewSource(t, ctx, svc, &newMat)
	bad := newMat
	bad.Quantity = ptr(999)
	if _, err := svc.ApplyDirectPrice(ctx, p, bad); !errors.Is(err, repository.ErrLinkedMaterialQuantity) {
		t.Fatalf("manual linked create: %v", err)
	}
	mat1 := apply(newMat)
	if mat1.Quantity != 5 || mat1.TotalAmount != 5*2070000 {
		t.Fatalf("archive material %+v", mat1)
	}
	mat2 := apply(DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, SourceKind: "library", LibraryKind: "material", SourceID: libraryMat, ExpectedSourceRate: 5049, DetailCostCategoryID: &category, ParentWorkItemID: &work.ItemID, ConversionCoefficient: ptr(3), ExpectedRevision: 2, RequestKey: "linked-create-material-002", Confirm: true})
	if mat2.Quantity != 45 || mat2.TotalAmount != 45*5049 {
		t.Fatalf("library material %+v", mat2)
	}
	bad = DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, TargetItemID: &mat1.ItemID, ExpectedETag: &mat1.ETag, SourceKind: "current", Quantity: ptr(999), ExpectedRevision: 3, RequestKey: "linked-manual-quantity-001", Confirm: true}
	if _, err := svc.ApplyDirectPrice(ctx, p, bad); !errors.Is(err, repository.ErrLinkedMaterialQuantity) {
		t.Fatalf("manual linked update: %v", err)
	}
	// Even an internal caller cannot bypass the restriction at the commit boundary.
	if _, err := repo.ApplyDirectPricing(ctx, repository.DirectPricingCommit{TenderID: targetTender, ActorID: evalUser, RequestKey: "linked-repo-bypass-001", RequestHash: "x", ExpectedRevision: 3, RequestedQuantity: ptr(999), Operation: pricing.DirectOperation{Action: "update_item", SourceKind: "current", TargetPositionID: targetPosition, TargetItemID: &mat1.ItemID, ExpectedETag: &mat1.ETag, ProposedPayload: pricing.ProposedItem{BoqItemType: "мат"}}}); !errors.Is(err, repository.ErrLinkedMaterialQuantity) {
		t.Fatalf("repository bypass: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.boq_items SET consumption_coefficient=9 WHERE id=$1`, sourceMaterial); err != nil {
		t.Fatal(err)
	}
	repriced := apply(DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, TargetItemID: &mat1.ItemID, ExpectedETag: &mat1.ETag, SourceKind: "archive", SourceID: sourceMaterial, ExpectedSourceRate: 2070000, ExpectedRevision: 3, RequestKey: "linked-reprice-material-001", Confirm: true})
	if repriced.Quantity != 5 || repriced.ConsumptionCoefficient == nil || *repriced.ConsumptionCoefficient != 2 {
		t.Fatalf("archive changed recipe: %+v", repriced)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.materials_library SET consumption_coefficient=8 WHERE id=$1`, libraryMat); err != nil {
		t.Fatal(err)
	}
	repriced2 := apply(DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, TargetItemID: &mat2.ItemID, ExpectedETag: &mat2.ETag, SourceKind: "library", LibraryKind: "material", SourceID: libraryMat, ExpectedSourceRate: 5049, ExpectedRevision: 4, RequestKey: "linked-reprice-material-002", Confirm: true})
	if repriced2.Quantity != 45 || *repriced2.ConsumptionCoefficient != 1.5 {
		t.Fatalf("library changed recipe: %+v", repriced2)
	}
	converted := apply(DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, TargetItemID: &mat1.ItemID, ExpectedETag: &repriced.ETag, SourceKind: "current", ConversionCoefficient: ptr(.5), ExpectedRevision: 5, RequestKey: "linked-conversion-001", Confirm: true})
	if converted.Quantity != 10 || converted.TotalAmount != 10*2070000 {
		t.Fatalf("conversion result %+v", converted)
	}
	workUpdate := DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, TargetItemID: &work.ItemID, ExpectedETag: &work.ETag, SourceKind: "current", Quantity: ptr(20), ExpectedRevision: 6, RequestKey: "linked-update-work-001", Confirm: true}
	updated := apply(workUpdate)
	if updated.Quantity != 20 || len(updated.LinkedMaterials) != 2 || updated.FinancialInputRevision != 7 {
		t.Fatalf("work update %+v", updated)
	}
	for _, child := range updated.LinkedMaterials {
		want := 20.0
		if child.ItemID == mat2.ItemID {
			want = 90
		}
		if child.Quantity != want {
			t.Fatalf("child %+v want quantity %v", child, want)
		}
	}
	replay := apply(workUpdate)
	if !replay.Replayed || len(replay.LinkedMaterials) != 2 {
		t.Fatalf("replay %+v", replay)
	}
	var tm, tw, storedSum float64
	if err := pool.QueryRow(ctx, `SELECT total_material,total_works,(SELECT sum(total_amount) FROM public.boq_items WHERE client_position_id=$1) FROM public.client_positions WHERE id=$1`, targetPosition).Scan(&tm, &tw, &storedSum); err != nil {
		t.Fatal(err)
	}
	if tw != 20000 || tm != 20*2070000+90*5049 || math.Abs(tm+tw-storedSum) > .001 {
		t.Fatalf("position totals material=%v work=%v rows=%v", tm, tw, storedSum)
	}
	// A missing FX rate on any child must roll back work, every child, revision,
	// audit and receipt together. EUR fixture has no CNY rate.
	if _, err := pool.Exec(ctx, `UPDATE public.boq_items SET currency_type='CNY' WHERE id=$1`, mat2.ItemID); err != nil {
		t.Fatal(err)
	}
	failed := workUpdate
	failed.Quantity, failed.ExpectedETag, failed.ExpectedRevision, failed.RequestKey = ptr(30), &updated.ETag, 7, "linked-failed-transaction-001"
	if _, err := svc.ApplyDirectPrice(ctx, p, failed); err == nil {
		t.Fatal("missing child FX rate was accepted")
	}
	var q, childQ float64
	var rev int64
	var receipts int
	if err := pool.QueryRow(ctx, `SELECT quantity,(SELECT quantity FROM public.boq_items WHERE id=$2),(SELECT financial_input_revision FROM public.tenders WHERE id=$3),(SELECT count(*) FROM public.mcp_direct_pricing_requests WHERE actor_id=$4 AND request_key=$5) FROM public.boq_items WHERE id=$1`, work.ItemID, mat1.ItemID, targetTender, evalUser, failed.RequestKey).Scan(&q, &childQ, &rev, &receipts); err != nil {
		t.Fatal(err)
	}
	if q != 20 || childQ != 20 || rev != 7 || receipts != 0 {
		t.Fatalf("partial commit: work=%v child=%v rev=%v receipts=%v", q, childQ, rev, receipts)
	}
	// Parent checks and unit conversion are server constraints, not prompts.
	missingParent := "eeeeeeee-5000-0000-0000-000000000099"
	for i, parent := range []string{mat1.ItemID, sourceWork, missingParent} {
		candidate := newMat
		candidate.ParentWorkItemID, candidate.ExpectedRevision = &parent, 7
		candidate.RequestKey = []string{"linked-invalid-parent-001", "linked-invalid-parent-002", "linked-invalid-parent-003"}[i]
		captureReviewSource(t, ctx, svc, &candidate)
		if _, err := svc.ApplyDirectPrice(ctx, p, candidate); !errors.Is(err, repository.ErrDirectParentInvalid) {
			t.Fatalf("parent %s: %v", parent, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO public.units(code,name) VALUES ('м3','Кубический метр') ON CONFLICT (code) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.material_names SET unit='м3' WHERE id='eeeeeeee-1000-0000-0000-000000000005'`); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `UPDATE public.material_names SET unit='шт' WHERE id='eeeeeeee-1000-0000-0000-000000000005'`)
	differentUnits := DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, SourceKind: "library", LibraryKind: "material", SourceID: libraryMat, ExpectedSourceRate: 5049, DetailCostCategoryID: &category, ParentWorkItemID: &work.ItemID, ExpectedRevision: 7, RequestKey: "linked-different-units-001", Confirm: true}
	captureReviewSource(t, ctx, svc, &differentUnits)
	if _, err := svc.ApplyDirectPrice(ctx, p, differentUnits); !errors.Is(err, ErrInvalidPricingInput) {
		t.Fatalf("implicit unit conversion accepted: %v", err)
	}
	differentUnits.ConversionCoefficient = ptr(2)
	unitConverted := apply(differentUnits)
	if unitConverted.Quantity != 320 {
		t.Fatalf("unit conversion quantity=%v want 20*2*8", unitConverted.Quantity)
	}
	// Rejected commands also leave no draft or receipt behind.
	var drafts, badReceipts int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM public.pricing_drafts WHERE tender_id=$1),(SELECT count(*) FROM public.mcp_direct_pricing_requests WHERE actor_id=$2 AND request_key IN ('linked-manual-quantity-001','linked-repo-bypass-001'))`, targetTender, evalUser).Scan(&drafts, &badReceipts); err != nil {
		t.Fatal(err)
	}
	if drafts != 0 || badReceipts != 0 {
		t.Fatalf("drafts=%d rejected receipts=%d", drafts, badReceipts)
	}
}
