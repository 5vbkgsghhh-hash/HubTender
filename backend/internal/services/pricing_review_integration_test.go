//go:build integration

package services

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/su10/hubtender/backend/internal/cache"
	"github.com/su10/hubtender/backend/internal/mcpauth"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/repository"
)

func reviewPricingService(t *testing.T, tracer pgx.QueryTracer) (context.Context, *pgxpool.Pool, *PricingService, pricing.Principal) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	seedPricingActor(t, ctx, pool)
	t.Cleanup(func() { cleanupPricingActor(ctx, pool); pool.Close() })
	svc := NewPricingService(repository.NewPricingRepo(pool), repository.NewUserRepo(pool), repository.NewLibraryRepo(pool), mcpauth.NewRepository(pool), PricingFeatures{WriteEnabled: true})
	p := pricing.Principal{UserID: evalUser, ClientID: evalClient, Scopes: []string{mcpauth.ScopeTendersRead, mcpauth.ScopeArchiveRead, mcpauth.ScopeLibraryRead, mcpauth.ScopePricingWrite}}
	return ctx, pool, svc, p
}

func reviewArchiveInput(key string) DirectPricingInput {
	qty := 2.0
	return DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, SourceKind: "archive", SourceID: sourceMaterial, ExpectedSourceRate: 2070000, Quantity: &qty, RequestKey: key, Confirm: true}
}

// A client captures this token when reading/selecting the source. Do not
// refresh it inside the mutation or a stale-source test would be meaningless.
func captureReviewSource(t *testing.T, ctx context.Context, svc *PricingService, in *DirectPricingInput) {
	t.Helper()
	if in.SourceKind == "archive" {
		c, err := svc.repo.GetArchiveItem(ctx, in.SourceID)
		if err != nil {
			t.Fatal(err)
		}
		in.ExpectedSourceVersion = c.SourceVersion
	} else if in.SourceKind == "library" {
		c, err := svc.repo.GetLibraryItem(ctx, in.SourceID, in.LibraryKind)
		if err != nil {
			t.Fatal(err)
		}
		in.ExpectedSourceVersion = c.SourceVersion
	}
}

type reviewCommitBarrier struct {
	entered chan struct{}
	release chan struct{}
}

func (b *reviewCommitBarrier) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.HasPrefix(d.SQL, "SELECT financial_input_revision,usd_rate") {
		select {
		case b.entered <- struct{}{}:
		case <-ctx.Done():
		}
		select {
		case <-b.release:
		case <-ctx.Done():
		}
	}
	return ctx
}
func (*reviewCommitBarrier) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func TestDirectPricingConcurrentCommandsDoNotDeadlock(t *testing.T) {
	barrier := &reviewCommitBarrier{entered: make(chan struct{}, 2), release: make(chan struct{})}
	ctx, pool, svc, p := reviewPricingService(t, barrier)
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var release sync.Once
	defer release.Do(func() { close(barrier.release) })
	type outcome struct {
		result *pricing.DirectPricingResult
		err    error
	}
	out := make(chan outcome, 2)
	for _, key := range []string{"review-concurrent-request-001", "review-concurrent-request-002"} {
		in := reviewArchiveInput(key)
		captureReviewSource(t, ctx, svc, &in)
		go func() { r, e := svc.ApplyDirectPrice(ctx, p, in); out <- outcome{r, e} }()
	}
	// Both transactions have inserted receipts (and acquired FK KEY SHARE on
	// the tender) before either attempts to upgrade its tender lock.
	for i := 0; i < 2; i++ {
		select {
		case <-barrier.entered:
		case <-ctx.Done():
			t.Fatal("commands did not reach tender lock")
		}
	}
	release.Do(func() { close(barrier.release) })
	success, stale := 0, 0
	for i := 0; i < 2; i++ {
		r := <-out
		if r.err == nil {
			success++
		} else if errors.Is(r.err, repository.ErrDirectPricingStale) {
			stale++
		} else {
			t.Fatalf("unexpected concurrent write error: %v", r.err)
		}
	}
	if success != 1 || stale != 1 {
		t.Fatalf("success=%d stale=%d", success, stale)
	}
	var receipts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM public.mcp_direct_pricing_requests WHERE actor_id=$1`, evalUser).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("receipts=%d", receipts)
	}
}

func TestDirectPricingBusyPortalRowFailsWithoutWaiting(t *testing.T) {
	ctx, pool, svc, p := reviewPricingService(t, nil)
	in := reviewArchiveInput("review-busy-create-001")
	captureReviewSource(t, ctx, svc, &in)
	created, err := svc.ApplyDirectPrice(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT id FROM public.boq_items WHERE id=$1 FOR UPDATE`, created.ItemID); err != nil {
		t.Fatal(err)
	}
	limited, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	q := 3.0
	_, err = svc.ApplyDirectPrice(limited, p, DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, TargetItemID: &created.ItemID, ExpectedETag: &created.ETag, SourceKind: "current", Quantity: &q, ExpectedRevision: 1, RequestKey: "review-busy-update-001", Confirm: true})
	if err == nil || !strings.Contains(err.Error(), "DIRECT_PRICING_BUSY") {
		t.Fatalf("busy row did not produce actionable conflict: %v", err)
	}
}

func TestDirectPricingRejectsSourceCurrencyChangeBeforeCommit(t *testing.T) {
	ctx, pool, svc, _ := reviewPricingService(t, nil)
	libraryID, category := "eeeeeeee-6000-0000-0000-000000000002", "eeeeeeee-0000-0000-0000-000000000002"
	q := 2.0
	in := DirectPricingInput{SourceKind: "library", LibraryKind: "material", SourceID: libraryID, ExpectedSourceRate: 5049, Quantity: &q, DetailCostCategoryID: &category}
	captureReviewSource(t, ctx, svc, &in)
	position, err := svc.repo.GetTargetPosition(ctx, targetPosition)
	if err != nil {
		t.Fatal(err)
	}
	op := pricing.DirectOperation{Action: "create_item", TargetPositionID: targetPosition, SourceKind: "library", Warnings: []string{}, PositionOrder: 1}
	if err := svc.buildDirectLibrary(ctx, in, position, nil, &op); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.materials_library SET currency_type='USD' WHERE id=$1`, libraryID); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `UPDATE public.materials_library SET currency_type='RUB' WHERE id=$1`, libraryID)
	_, err = svc.repo.ApplyDirectPricing(ctx, repository.DirectPricingCommit{TenderID: targetTender, ActorID: evalUser, RequestKey: "review-source-currency-001", RequestHash: "currency-test", Operation: op})
	if !errors.Is(err, repository.ErrDirectPricingStale) {
		t.Fatalf("currency changed but source was accepted: %v", err)
	}
}

func TestDirectPricingRepriceClearsOldQuoteEvidence(t *testing.T) {
	ctx, pool, svc, p := reviewPricingService(t, nil)
	in := reviewArchiveInput("review-quote-create-001")
	captureReviewSource(t, ctx, svc, &in)
	created, err := svc.ApplyDirectPrice(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.boq_items SET quote_price_date='2026-01-01',quote_valid_until='2026-02-01' WHERE id=$1`, created.ItemID); err != nil {
		t.Fatal(err)
	}
	_, etag, err := svc.repo.GetCurrentBoqItem(ctx, created.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	update := DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, TargetItemID: &created.ItemID, ExpectedETag: &etag, SourceKind: "library", LibraryKind: "material", SourceID: "eeeeeeee-6000-0000-0000-000000000002", ExpectedSourceRate: 5049, ExpectedRevision: 1, RequestKey: "review-quote-reprice-001", Confirm: true}
	captureReviewSource(t, ctx, svc, &update)
	_, err = svc.ApplyDirectPrice(ctx, p, update)
	if err != nil {
		t.Fatal(err)
	}
	row, _, err := svc.repo.GetCurrentBoqItem(ctx, created.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if row.QuoteLink != nil || row.QuotePriceDate != nil || row.QuoteValidUntil != nil {
		t.Fatalf("old quote remained on new price: link=%v date=%v until=%v", row.QuoteLink, row.QuotePriceDate, row.QuoteValidUntil)
	}
}

func TestDirectPricingReplayRepairsMissedCacheInvalidation(t *testing.T) {
	ctx, _, svc, p := reviewPricingService(t, nil)
	c := cache.New()
	svc.WithCache(c)
	in := reviewArchiveInput("review-cache-create-001")
	captureReviewSource(t, ctx, svc, &in)
	if _, err := svc.ApplyDirectPrice(ctx, p, in); err != nil {
		t.Fatal(err)
	}
	// Simulate a committed write whose process died before invalidation/enqueue.
	key := "tender:overview:" + targetTender
	c.Set(key, "stale", time.Minute)
	svc.features.WriteEnabled = false
	if _, err := svc.ApplyDirectPrice(ctx, p, in); err != nil {
		t.Fatal(err)
	}
	if _, found := c.Get(key); found {
		t.Fatal("replayed receipt left stale cache intact")
	}
}

func TestDirectPricingSearchVersionMatchesCommitLookup(t *testing.T) {
	ctx, _, svc, p := reviewPricingService(t, nil)
	archive, _, err := svc.SearchArchive(ctx, p, pricing.ArchiveSearchInput{Query: "Мобильный пресс-компактор", Kind: "material", UnitCode: "шт", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range archive {
		if c.ItemID == sourceMaterial {
			in := reviewArchiveInput("review-search-version-001")
			in.ExpectedSourceVersion = c.SourceVersion
			if _, err := svc.ApplyDirectPrice(ctx, p, in); err != nil {
				t.Fatalf("archive search version cannot be applied: %v", err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("fixture archive source not found")
	}
	library, _, err := svc.SearchLibrary(ctx, p, "Колесоотбойник", "material", "шт", 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(library) == 0 {
		t.Fatal("fixture library source not found")
	}
	selected, err := svc.repo.GetLibraryItem(ctx, library[0].ID, "material")
	if err != nil {
		t.Fatal(err)
	}
	if selected.SourceVersion != library[0].SourceVersion {
		t.Fatal("library search and commit lookup versions differ")
	}
}

func TestDirectPricingRejectsSelectedSourceDriftBeforeService(t *testing.T) {
	for _, tc := range []struct{ name, change, restore string }{
		{"currency", `UPDATE public.materials_library SET currency_type='USD' WHERE id=$1`, `UPDATE public.materials_library SET currency_type='RUB' WHERE id=$1`},
		{"delivery", `UPDATE public.materials_library SET delivery_price_type='не в цене' WHERE id=$1`, `UPDATE public.materials_library SET delivery_price_type='в цене' WHERE id=$1`},
		{"consumption", `UPDATE public.materials_library SET consumption_coefficient=2 WHERE id=$1`, `UPDATE public.materials_library SET consumption_coefficient=1 WHERE id=$1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, pool, svc, p := reviewPricingService(t, nil)
			category := "eeeeeeee-0000-0000-0000-000000000002"
			q := 2.0
			in := DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, SourceKind: "library", LibraryKind: "material", SourceID: "eeeeeeee-6000-0000-0000-000000000002", ExpectedSourceRate: 5049, Quantity: &q, DetailCostCategoryID: &category, RequestKey: "review-client-drift-" + tc.name, Confirm: true}
			captureReviewSource(t, ctx, svc, &in)
			if _, err := pool.Exec(ctx, tc.change, in.SourceID); err != nil {
				t.Fatal(err)
			}
			defer pool.Exec(ctx, tc.restore, in.SourceID)
			if _, err := svc.ApplyDirectPrice(ctx, p, in); !errors.Is(err, repository.ErrDirectPricingStale) {
				t.Fatalf("source drift accepted: %v", err)
			}
		})
	}
}

func TestDirectPricingArchiveQuoteReplacementDoesNotMixDates(t *testing.T) {
	ctx, pool, svc, p := reviewPricingService(t, nil)
	in := reviewArchiveInput("review-archive-quote-create")
	captureReviewSource(t, ctx, svc, &in)
	created, err := svc.ApplyDirectPrice(ctx, p, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.boq_items SET quote_price_date='2026-01-01',quote_valid_until='2026-02-01' WHERE id=$1`, created.ItemID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.boq_items SET quote_price_date='2026-03-01',quote_valid_until=NULL WHERE id=$1`, sourceMaterial); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `UPDATE public.boq_items SET quote_price_date=NULL,quote_valid_until=NULL WHERE id=$1`, sourceMaterial)
	_, etag, err := svc.repo.GetCurrentBoqItem(ctx, created.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	in.TargetItemID, in.ExpectedETag, in.Quantity, in.ExpectedRevision, in.RequestKey = &created.ItemID, &etag, nil, 1, "review-archive-quote-update"
	captureReviewSource(t, ctx, svc, &in)
	if _, err := svc.ApplyDirectPrice(ctx, p, in); err != nil {
		t.Fatalf("new date incorrectly mixed with old expiry: %v", err)
	}
	row, _, err := svc.repo.GetCurrentBoqItem(ctx, created.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	if row.QuotePriceDate == nil || *row.QuotePriceDate != "2026-03-01" || row.QuoteValidUntil != nil {
		t.Fatal("quote evidence was not replaced as one source snapshot")
	}
}
