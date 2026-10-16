//go:build integration

package services

import (
	"strings"
	"testing"

	"github.com/su10/hubtender/backend/internal/actorctx"
	"github.com/su10/hubtender/backend/internal/repository"
)

func TestMCPPricingReviewRenameInvalidatesConsent(t *testing.T) {
	ctx, pool, svc, p, in := checksFixture(t)
	created, err := svc.ApplyDirectPrice(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	q := 5.0
	update := DirectPricingInput{TenderID: checksTender, TargetPositionID: checksPosition, TargetItemID: &created.ItemID, SourceKind: "current", Quantity: &q, ExpectedETag: &created.ETag, ExpectedRevision: 1, RequestKey: "fixed-review-name-001", Confirm: true}
	before, err := svc.PreviewDirectPrice(ctx, p, update)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.work_names SET name='Changed nomenclature meaning' WHERE id=$1`, checksWorkName); err != nil {
		t.Fatal(err)
	}
	after, err := svc.PreviewDirectPrice(ctx, p, update)
	if err != nil {
		t.Fatal(err)
	}
	if before.Name == after.Name || before.Checks.Fingerprint == after.Checks.Fingerprint {
		t.Fatal("renamed nomenclature is not bound to consent")
	}
	update.ExpectedChecksFingerprint = before.Checks.Fingerprint
	if _, err := svc.ApplyDirectPrice(ctx, p, update); err == nil || !strings.Contains(err.Error(), "CHECKS_CHANGED") {
		t.Fatalf("old name consent accepted: %v", err)
	}
	update.ExpectedChecksFingerprint = after.Checks.Fingerprint
	if _, err := svc.ApplyDirectPrice(ctx, p, update); err != nil {
		t.Fatal(err)
	}
}

func TestMCPPricingReviewOwnerProtectsMCPAndPortalPaths(t *testing.T) {
	ctx, pool, svc, p, in := checksFixture(t)
	created, err := svc.ApplyDirectPrice(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	other := p
	other.UserID = "dddddddd-9000-0000-0000-000000000099"
	must := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	must(`INSERT INTO auth.users(id,email) VALUES($1,'ownership-other@example.test')`, other.UserID)
	must(`INSERT INTO public.users(id,full_name,email,access_status,access_enabled,role_code,allowed_pages) VALUES($1,'Other engineer','ownership-other@example.test','approved',true,'engineer','["/positions","/library"]')`, other.UserID)
	must(`INSERT INTO app_auth.oauth_grants(user_id,client_id,scopes) VALUES($1,$2,$3)`, other.UserID, p.ClientID, p.Scopes)
	t.Cleanup(func() {
		must(`DELETE FROM public.mcp_direct_pricing_requests WHERE actor_id=$1`, other.UserID)
		must(`DELETE FROM public.boq_items_audit WHERE changed_by=$1`, other.UserID)
		must(`DELETE FROM public.position_pricing_owners WHERE owner_id=$1`, other.UserID)
		must(`DELETE FROM app_auth.oauth_grants WHERE user_id=$1`, other.UserID)
		must(`DELETE FROM public.users WHERE id=$1`, other.UserID)
		must(`DELETE FROM auth.users WHERE id=$1`, other.UserID)
	})
	q := 5.0
	update := DirectPricingInput{TenderID: checksTender, TargetPositionID: checksPosition, TargetItemID: &created.ItemID, SourceKind: "current", Quantity: &q, ExpectedETag: &created.ETag, ExpectedRevision: 1, RequestKey: "fixed-other-actor-001", Confirm: true}
	reject := func(err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "PRICING_OWNER_MISMATCH") {
			t.Fatalf("foreign calculation was not rejected: %v", err)
		}
	}
	_, err = svc.PreviewDirectPrice(ctx, other, update)
	reject(err)
	_, err = svc.ApplyDirectPrice(ctx, other, update)
	reject(err)
	boq := repository.NewBoqRepo(pool)
	_, err = boq.UpdateBoqItem(ctx, created.ItemID, repository.BoqItemPatch{Quantity: &q, ChangedBy: other.UserID})
	reject(err)
	_, err = boq.UpdateBoqItem(actorctx.WithUserID(ctx, other.UserID), created.ItemID, repository.BoqItemPatch{Quantity: &q, ChangedBy: p.UserID})
	reject(err)
	_, err = boq.DeleteBoqItem(ctx, created.ItemID, other.UserID)
	reject(err)
	_, err = boq.CreateBoqItem(ctx, repository.CreateBoqItemInput{TenderID: checksTender, ClientPositionID: checksPosition, BoqItemType: "суб-раб", WorkNameID: stringPtr(checksWorkName), UnitCode: stringPtr("шт"), Quantity: &q, UnitRate: &q, CurrencyType: stringPtr("RUB"), CreatedBy: other.UserID})
	reject(err)
	_, err = repository.NewImportRepo(pool).BulkImport(ctx, repository.ImportInput{TenderID: checksTender, FileName: "foreign.xlsx", UserID: other.UserID, Items: []repository.ImportBoqItem{{ClientPositionID: checksPosition, BoqItemType: "суб-раб", WorkNameID: stringPtr(checksWorkName), Quantity: &q, UnitRate: &q, CurrencyType: stringPtr("RUB")}}})
	reject(err)
	_, err = boq.RecomputeLinkedMaterialsForWork(ctx, created.ItemID, other.UserID)
	reject(err)
	positions := repository.NewPositionRepo(pool)
	reject(positions.ClearPositionsBoq(ctx, []string{checksPosition}, other.UserID))
	reject(positions.BulkDeletePositions(ctx, []string{checksPosition}, other.UserID))
	reject(positions.UpdatePositionFields(actorctx.WithUserID(ctx, other.UserID), checksPosition, repository.UpdatePositionFieldsInput{ManualVolume: &q}))
	// Tender deletion also uses the verified request actor, without changing
	// handler/service signatures or permitting a cascade past row ownership.
	reject(repository.NewTenderRepo(pool).DeleteTender(actorctx.WithUserID(ctx, other.UserID), checksTender))
	// Approved roles do not override the owner, including claimed admin roles.
	other.RoleCode = "administrator"
	_, err = svc.ApplyDirectPrice(ctx, other, update)
	reject(err)
	var count int
	var revision int64
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM public.boq_items WHERE tender_id=$1),financial_input_revision FROM public.tenders WHERE id=$1`, checksTender).Scan(&count, &revision); err != nil || count != 1 || revision != 1 {
		t.Fatalf("rejected operation changed state: %d %d %v", count, revision, err)
	}
	// The actual owner can still update and clear their calculation.
	if _, err := svc.ApplyDirectPrice(ctx, p, update); err != nil {
		t.Fatal(err)
	}
	var auditID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM public.boq_items_audit WHERE boq_item_id=$1 AND operation_type='UPDATE' ORDER BY changed_at DESC LIMIT 1`, created.ItemID).Scan(&auditID); err != nil {
		t.Fatal(err)
	}
	_, err = repository.NewBoqAuditRollbackRepo(pool).Rollback(ctx, auditID, other.UserID)
	reject(err)
	if err := positions.ClearPositionsBoq(ctx, []string{checksPosition}, p.UserID); err != nil {
		t.Fatal(err)
	}
	// Clearing does not silently transfer the position to the next caller.
	in.ExpectedRevision = 3
	_, err = svc.PreviewDirectPrice(ctx, other, in)
	reject(err)
}

func TestMCPPricingReviewUnknownLegacyOwnerCannotBeClaimed(t *testing.T) {
	ctx, pool, svc, p, in := checksFixture(t)
	if _, err := pool.Exec(ctx, `INSERT INTO public.boq_items(tender_id,client_position_id,boq_item_type,work_name_id,unit_code,quantity,unit_rate,currency_type,total_amount,detail_cost_category_id) VALUES($1,$2,'суб-раб',$3,'шт',10,100,'RUB',1000,'eeeeeeee-0000-0000-0000-000000000002')`, checksTender, checksPosition, checksWorkName); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.PreviewDirectPrice(ctx, p, in); err == nil || !strings.Contains(err.Error(), "PRICING_OWNER_UNASSIGNED") {
		t.Fatalf("unattributed legacy row adopted: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.position_pricing_owners WHERE client_position_id=$1`, checksPosition).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed claim persisted: %d %v", count, err)
	}
}

func TestMCPPricingReviewGPAuditIsAtomicAndRecordsActor(t *testing.T) {
	ctx, pool, svc, p, in := checksFixture(t)
	gp := 11.0
	in.PositionQuantity = &gp
	if _, err := svc.PreviewDirectPrice(ctx, p, in); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.client_position_pricing_audit WHERE client_position_id=$1`, checksPosition).Scan(&count); err != nil || count != 0 {
		t.Fatalf("preview leaked GP audit: %d %v", count, err)
	}
	written, err := svc.ApplyDirectPrice(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	if written.PreviousPositionQuantity == nil || *written.PreviousPositionQuantity != 10 {
		t.Fatal("old GP missing from receipt")
	}
	var before, after float64
	var actor, key string
	if err := pool.QueryRow(ctx, `SELECT old_manual_volume,new_manual_volume,actor_id::text,request_key FROM public.client_position_pricing_audit WHERE client_position_id=$1`, checksPosition).Scan(&before, &after, &actor, &key); err != nil {
		t.Fatal(err)
	}
	if before != 10 || after != 11 || actor != p.UserID || key != in.RequestKey {
		t.Fatalf("GP audit: %v %v %s %s", before, after, actor, key)
	}
	if _, err := svc.ApplyDirectPrice(ctx, p, in); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.client_position_pricing_audit WHERE client_position_id=$1`, checksPosition).Scan(&count); err != nil || count != 1 {
		t.Fatalf("receipt replay duplicated audit: %d %v", count, err)
	}
}
