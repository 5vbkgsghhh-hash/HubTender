//go:build integration

package services

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/su10/hubtender/backend/internal/mcpauth"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/repository"
)

func catalogTestService(t *testing.T) (context.Context, *pgxpool.Pool, *PricingService, pricing.Principal) {
	ctx, pool, svc, p := reviewPricingService(t, nil)
	p.Scopes = append(p.Scopes, mcpauth.ScopeNomenclatureCreate, mcpauth.ScopeLibraryCreate)
	if _, err := pool.Exec(ctx, `UPDATE app_auth.oauth_grants SET scopes=$2 WHERE user_id=$1`, p.UserID, p.Scopes); err != nil {
		t.Fatal(err)
	}
	svc.features.CatalogWriteEnabled = true
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM public.boq_items WHERE tender_id=$1`, targetTender)
		pool.Exec(ctx, `DELETE FROM public.mcp_catalog_creation_requests WHERE actor_id=$1`, evalUser)
		pool.Exec(ctx, `DELETE FROM public.works_library WHERE work_name_id IN (SELECT id FROM public.work_names WHERE name LIKE 'MCP CATALOG TEST%')`)
		pool.Exec(ctx, `DELETE FROM public.materials_library WHERE material_name_id IN (SELECT id FROM public.material_names WHERE name LIKE 'MCP CATALOG TEST%')`)
		pool.Exec(ctx, `DELETE FROM public.work_names WHERE name LIKE 'MCP CATALOG TEST%'`)
		pool.Exec(ctx, `DELETE FROM public.material_names WHERE name LIKE 'MCP CATALOG TEST%'`)
		pool.Exec(ctx, `UPDATE public.client_positions SET unit_code='шт' WHERE id=$1`, targetPosition)
		pool.Exec(ctx, `DELETE FROM public.units WHERE code='MCP-CAT-TEST'`)
	})
	return ctx, pool, svc, p
}

func TestCatalogEngineerCreatesUnitNamesCardsAndLinkedVOR(t *testing.T) {
	ctx, pool, svc, p := catalogTestService(t)
	create := func(in pricing.CatalogCreationInput) *pricing.CatalogCreationResult {
		t.Helper()
		in.Confirm = true
		r, e := svc.CreateCatalogEntity(ctx, p, in)
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	u := create(pricing.CatalogCreationInput{EntityType: "unit", Name: "Единица теста", UnitCode: "MCP-CAT-TEST", RequestKey: "catalog-flow-unit-001"})
	if !u.Created {
		t.Fatal("unit was not created")
	}
	wn := create(pricing.CatalogCreationInput{EntityType: "nomenclature", Kind: "work", Name: "MCP CATALOG TEST work", UnitCode: u.EntityID, RequestKey: "catalog-flow-work-name-001"})
	mn := create(pricing.CatalogCreationInput{EntityType: "nomenclature", Kind: "material", Name: "MCP CATALOG TEST material", UnitCode: u.EntityID, RequestKey: "catalog-flow-mat-name-001"})
	if !wn.Created || !mn.Created || wn.NomenclatureItem == nil || mn.NomenclatureItem == nil {
		t.Fatal("nomenclature creation missing IDs/versions")
	}
	workIn := pricing.CatalogCreationInput{EntityType: "library", Kind: "work", NameID: wn.EntityID, ExpectedNameVersion: wn.NomenclatureItem.Version, UnitRate: 500, Currency: "RUB", PriceSource: "Цена работы задана пользователем", RequestKey: "catalog-flow-work-card-001", Confirm: true}
	wc := create(workIn)
	cons, delivery := 1.2, 5.0
	matIn := pricing.CatalogCreationInput{EntityType: "library", Kind: "material", NameID: mn.EntityID, ExpectedNameVersion: mn.NomenclatureItem.Version, UnitRate: 100, Currency: "RUB", ConsumptionCoefficient: &cons, DeliveryPriceType: "суммой", DeliveryAmount: &delivery, PriceSource: "Тестовый КП №MCP", RequestKey: "catalog-flow-mat-card-001", Confirm: true}
	mc := create(matIn)
	if !wc.Created || !mc.Created || wc.LibraryItem == nil || mc.LibraryItem == nil || mc.LibraryItem.SourceVersion == "" {
		t.Fatal("library creation missing source/readback")
	}
	if _, err := pool.Exec(ctx, `UPDATE public.client_positions SET unit_code=$2 WHERE id=$1`, targetPosition, u.EntityID); err != nil {
		t.Fatal(err)
	}
	qty, category := 10.0, "eeeeeeee-0000-0000-0000-000000000002"
	work, err := svc.ApplyDirectPrice(ctx, p, DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, SourceKind: "library", LibraryKind: "work", SourceID: wc.EntityID, ExpectedSourceRate: 500, ExpectedSourceVersion: wc.LibraryItem.SourceVersion, DetailCostCategoryID: &category, Quantity: &qty, RequestKey: "catalog-flow-vor-work-001", Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	conv := 2.0
	mat, err := svc.ApplyDirectPrice(ctx, p, DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, SourceKind: "library", LibraryKind: "material", SourceID: mc.EntityID, ExpectedSourceRate: 100, ExpectedSourceVersion: mc.LibraryItem.SourceVersion, DetailCostCategoryID: &category, ParentWorkItemID: &work.ItemID, ConversionCoefficient: &conv, ExpectedRevision: 1, RequestKey: "catalog-flow-vor-mat-001", Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if mat.Quantity != 24 || mat.TotalAmount != 2520 {
		t.Fatalf("linked material quantity=%v amount=%v", mat.Quantity, mat.TotalAmount)
	}
	q := 999.0
	_, err = svc.ApplyDirectPrice(ctx, p, DirectPricingInput{TenderID: targetTender, TargetPositionID: targetPosition, TargetItemID: &mat.ItemID, ExpectedETag: &mat.ETag, SourceKind: "current", Quantity: &q, ExpectedRevision: 2, RequestKey: "catalog-flow-vor-forbid-001", Confirm: true})
	if !errors.Is(err, repository.ErrLinkedMaterialQuantity) {
		t.Fatalf("manual linked volume accepted: %v", err)
	}
	// Exact cards and normalized nomenclature are reused, never overwritten.
	matIn.RequestKey = "catalog-flow-mat-card-002"
	reused := create(matIn)
	if reused.Created || reused.EntityID != mc.EntityID {
		t.Fatal("exact library duplicate was created")
	}
	nameReuse := create(pricing.CatalogCreationInput{EntityType: "nomenclature", Kind: "work", Name: "MCP   CATALOG TEST work", UnitCode: u.EntityID, RequestKey: "catalog-flow-work-name-002"})
	if nameReuse.Created || nameReuse.EntityID != wn.EntityID {
		t.Fatal("normalized name duplicate was created")
	}
	svc.features.CatalogWriteEnabled = false
	replay, err := svc.CreateCatalogEntity(ctx, p, workIn)
	if err != nil || !replay.Replayed || replay.EntityID != wc.EntityID {
		t.Fatalf("receipt replay=%+v err=%v", replay, err)
	}
	workIn.UnitRate = 501
	if _, err := svc.CreateCatalogEntity(ctx, p, workIn); !errors.Is(err, repository.ErrCatalogKeyReused) {
		t.Fatalf("different price reused key: %v", err)
	}
	workIn.RequestKey = "catalog-flow-disabled-001"
	if _, err := svc.CreateCatalogEntity(ctx, p, workIn); !errors.Is(err, ErrPricingDisabled) {
		t.Fatalf("disabled new create: %v", err)
	}
	var input string
	if err := pool.QueryRow(ctx, `SELECT input::text FROM public.mcp_catalog_creation_requests WHERE actor_id=$1 AND request_key=$2`, p.UserID, matIn.RequestKey).Scan(&input); err != nil || input == "" {
		t.Fatalf("audit missing: %v", err)
	}
}

func TestCatalogCreationConcurrencyPermissionsAndStaleName(t *testing.T) {
	ctx, pool, svc, p := catalogTestService(t)
	var wg sync.WaitGroup
	type outcome struct {
		r *pricing.CatalogCreationResult
		e error
	}
	results := make(chan outcome, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, e := svc.CreateCatalogEntity(ctx, p, pricing.CatalogCreationInput{EntityType: "nomenclature", Kind: "material", Name: "MCP CATALOG TEST concurrent", UnitCode: "шт", RequestKey: fmt.Sprintf("catalog-concurrent-name-%03d", i), Confirm: true})
			results <- outcome{r, e}
		}(i)
	}
	wg.Wait()
	close(results)
	id, created := "", 0
	var nameVersion string
	for result := range results {
		if result.e != nil {
			t.Fatal(result.e)
		}
		if id != "" && id != result.r.EntityID {
			t.Fatal("concurrent names diverged")
		}
		id = result.r.EntityID
		nameVersion = result.r.NomenclatureItem.Version
		if result.r.Created {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created=%d", created)
	}
	card := pricing.CatalogCreationInput{EntityType: "library", Kind: "material", NameID: id, ExpectedNameVersion: nameVersion, UnitRate: 10, Currency: "RUB", PriceSource: "Цена из тестового КП", RequestKey: "catalog-stale-name-card-001", Confirm: true}
	if _, err := pool.Exec(ctx, `UPDATE public.material_names SET name='MCP CATALOG TEST changed' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateCatalogEntity(ctx, p, card); !errors.Is(err, repository.ErrCatalogNameStale) {
		t.Fatalf("changed name accepted: %v", err)
	}
	readOnly := p
	readOnly.Scopes = []string{mcpauth.ScopeLibraryRead}
	if _, err := svc.CreateCatalogEntity(ctx, readOnly, pricing.CatalogCreationInput{EntityType: "unit", UnitCode: "x", Name: "x", RequestKey: "catalog-no-scope-001", Confirm: true}); !errors.Is(err, ErrPricingForbidden) {
		t.Fatalf("read scope granted creation: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO public.roles(code,name,allowed_pages) VALUES('catalog_readonly','Readonly','["/library"]') ON CONFLICT(code) DO NOTHING`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE public.users SET role_code='catalog_readonly' WHERE id=$1`, p.UserID); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `UPDATE public.users SET role_code='engineer' WHERE id=$1`, p.UserID)
	p.RoleCode = "administrator" // token-side claims cannot override actual role
	if _, err := svc.CreateCatalogEntity(ctx, p, card); !errors.Is(err, ErrPricingForbidden) {
		t.Fatalf("forged role granted creation: %v", err)
	}
}
