//go:build integration

package services

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/su10/hubtender/backend/internal/mcpauth"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/repository"
)

func TestDirectPricingCommitsWithoutDraftAndReplaysSafely(t *testing.T) {
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
	svc := NewPricingService(repo, repository.NewUserRepo(pool), repository.NewLibraryRepo(pool),
		mcpauth.NewRepository(pool), PricingFeatures{WriteEnabled: true})
	principal := pricing.Principal{
		UserID: evalUser, ClientID: evalClient, RoleCode: "engineer",
		Scopes: []string{mcpauth.ScopeTendersRead, mcpauth.ScopeArchiveRead, mcpauth.ScopeLibraryRead, mcpauth.ScopePricingWrite},
	}
	state, err := svc.GetPricingState(ctx, principal, targetTender, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if state.ItemCount != 0 || state.FinancialInputRevision != 0 {
		t.Fatalf("unexpected initial state: items=%d revision=%d", state.ItemCount, state.FinancialInputRevision)
	}
	source, err := repo.GetArchiveItem(ctx, sourceMaterial)
	if err != nil || source == nil || source.UnitRate == nil {
		t.Fatalf("source unavailable: %v", err)
	}
	quantity := 2.0
	in := DirectPricingInput{
		TenderID: targetTender, TargetPositionID: targetPosition,
		SourceKind: "archive", SourceID: sourceMaterial,
		ExpectedSourceRate: *source.UnitRate, Quantity: &quantity,
		ExpectedSourceVersion: source.SourceVersion,
		ExpectedRevision:      state.FinancialInputRevision,
		RequestKey:            "integration-direct-create-001", Confirm: true,
	}
	created, err := svc.ApplyDirectPrice(ctx, principal, in)
	if err != nil {
		t.Fatal(err)
	}
	if created.Action != "create_item" || created.Replayed || created.FinancialInputRevision != 1 || created.ItemID == "" {
		t.Fatalf("unexpected direct create: %+v", created)
	}
	var sourceRows, draftRows, auditRows int
	if err := pool.QueryRow(ctx, `SELECT count(*),count(draft_operation_id)
		FROM public.boq_item_pricing_sources WHERE boq_item_id=$1`, created.ItemID).Scan(&sourceRows, &draftRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.pricing_drafts WHERE tender_id=$1`, targetTender).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if sourceRows != 1 || draftRows != 0 || auditRows != 0 {
		t.Fatalf("source rows=%d draft links=%d drafts=%d", sourceRows, draftRows, auditRows)
	}
	svc.features.WriteEnabled = false // receipt must still be recoverable when the kill switch is off
	disabled := in
	disabled.RequestKey = "integration-direct-disabled-001"
	if _, err := svc.ApplyDirectPrice(ctx, principal, disabled); !errors.Is(err, ErrPricingDisabled) {
		t.Fatalf("new write with disabled feature: %v", err)
	}
	replayed, err := svc.ApplyDirectPrice(ctx, principal, in)
	if err != nil || !replayed.Replayed || replayed.ItemID != created.ItemID || replayed.FinancialInputRevision != 1 {
		t.Fatalf("idempotent replay=%+v err=%v", replayed, err)
	}
	svc.features.WriteEnabled = true
	different := in
	different.Quantity = new(float64)
	*different.Quantity = 3
	if _, err := svc.ApplyDirectPrice(ctx, principal, different); !errors.Is(err, repository.ErrDirectRequestKeyReused) {
		t.Fatalf("request key reuse error=%v", err)
	}
	stale := in
	stale.RequestKey = "integration-direct-stale-001"
	if _, err := svc.ApplyDirectPrice(ctx, principal, stale); !errors.Is(err, repository.ErrDirectPricingStale) {
		t.Fatalf("stale financial revision error=%v", err)
	}
	wrongRate := in
	wrongRate.RequestKey = "integration-direct-rate-001"
	wrongRate.ExpectedRevision = 1
	wrongRate.ExpectedSourceRate++
	if _, err := svc.ApplyDirectPrice(ctx, principal, wrongRate); !errors.Is(err, repository.ErrDirectPricingStale) {
		t.Fatalf("stale source rate error=%v", err)
	}
	update := in
	update.TargetItemID = &created.ItemID
	update.Quantity = nil
	update.ExpectedETag = &created.ETag
	update.ExpectedRevision = 1
	update.RequestKey = "integration-direct-update-001"
	badETag := update
	wrongETag := "\"stale\""
	badETag.ExpectedETag = &wrongETag
	badETag.RequestKey = "integration-direct-etag-001"
	if _, err := svc.ApplyDirectPrice(ctx, principal, badETag); !errors.Is(err, repository.ErrDirectPricingStale) {
		t.Fatalf("stale ETag error=%v", err)
	}
	updated, err := svc.ApplyDirectPrice(ctx, principal, update)
	if err != nil || updated.Action != "update_item" || updated.FinancialInputRevision != 2 {
		t.Fatalf("direct update=%+v err=%v", updated, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.boq_items_audit
		WHERE boq_item_id=$1 AND changed_by=$2`, created.ItemID, evalUser).Scan(&auditRows); err != nil {
		t.Fatal(err)
	}
	if auditRows != 2 {
		t.Fatalf("audit rows=%d, want 2", auditRows)
	}
	finalState, err := svc.GetPricingState(ctx, principal, targetTender, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if finalState.ItemCount != 1 || finalState.FinancialInputRevision != 2 {
		t.Fatalf("final items=%d revision=%d", finalState.ItemCount, finalState.FinancialInputRevision)
	}
}
