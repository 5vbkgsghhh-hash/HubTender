package repository

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Raw SQL fixtures predate ownership. Give their synthetic calculation an
// explicit author; never relax the production guard to accommodate a fixture.
func seedTestPricingOwner(t *testing.T, pool *pgxpool.Pool, positionID string) {
	t.Helper()
	ensureTestPricingActor(t, pool)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO public.position_pricing_owners(client_position_id,owner_id,origin) VALUES($1,$2,'test-fixture')`, positionID, rbActor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM public.position_pricing_owners WHERE client_position_id=$1`, positionID)
		_, _ = pool.Exec(ctx, `DELETE FROM public.client_position_pricing_audit WHERE client_position_id=$1`, positionID)
	})
}

func ensureTestPricingActor(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO public.roles(code,name) VALUES('engineer','Test engineer') ON CONFLICT(code) DO NOTHING`,
		`INSERT INTO auth.users(id,email) VALUES('00000000-0000-0000-0000-000000000000','repository-test@example.test') ON CONFLICT(id) DO NOTHING`,
		`INSERT INTO public.users(id,email,full_name,role_code,access_enabled,access_status) VALUES('00000000-0000-0000-0000-000000000000','repository-test@example.test','Repository test','engineer',true,'approved') ON CONFLICT(id) DO NOTHING`,
	} {
		if _, err := pool.Exec(context.Background(), q); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPricingOwnershipMigrationUsesCreationEvidence(t *testing.T) {
	pool := newTestPool(t)
	ensureTestPricingActor(t, pool)
	ensureMemUserB(t, pool)
	ctx := context.Background()
	tenderID, positionID := seedSourceTender(t, pool, "OWNER-BACKFILL")
	workID, _ := ensureTestNames(t, pool)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	var mixed, unknown, deleted string
	for i, out := range []*string{&mixed, &unknown, &deleted} {
		if err := tx.QueryRow(ctx, `INSERT INTO public.client_positions(tender_id,position_number,work_name) VALUES($1,$2,'Backfill test') RETURNING id::text`, tenderID, i+2).Scan(out); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(position, author string) string {
		t.Helper()
		var item string
		if err := tx.QueryRow(ctx, `INSERT INTO public.boq_items(tender_id,client_position_id,boq_item_type,work_name_id) VALUES($1,$2,'суб-раб',$3) RETURNING id::text`, tenderID, position, workID).Scan(&item); err != nil {
			t.Fatal(err)
		}
		if author != "" {
			exec(`UPDATE public.boq_items_audit SET changed_by=$2 WHERE boq_item_id=$1 AND operation_type='INSERT'`, item, author)
		}
		return item
	}
	insert(positionID, rbActor)
	insert(mixed, rbActor)
	insert(mixed, memUserB)
	insert(unknown, "")
	oldItem := insert(deleted, rbActor)
	exec(`DELETE FROM public.boq_items WHERE id=$1`, oldItem)
	// A later deleter/editor is never treated as the original author.
	exec(`UPDATE public.boq_items_audit SET changed_by=$2 WHERE boq_item_id=$1 AND operation_type='DELETE'`, oldItem, memUserB)
	source, err := os.ReadFile("../../../db/yandex/incremental/2026_10_mcp_pricing_ownership.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Run the real idempotent migration inside a transaction we always roll back.
	sql := strings.ReplaceAll(strings.ReplaceAll(string(source), "BEGIN;", ""), "COMMIT;", "")
	exec(sql)
	for _, tc := range []struct {
		position string
		owner    *string
	}{{positionID, sptr(rbActor)}, {mixed, nil}, {unknown, nil}, {deleted, sptr(rbActor)}} {
		var owner *string
		if err := tx.QueryRow(ctx, `SELECT owner_id::text FROM public.position_pricing_owners WHERE client_position_id=$1`, tc.position).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		if (owner == nil) != (tc.owner == nil) || (owner != nil && *owner != *tc.owner) {
			t.Fatalf("position %s owner=%v want=%v", tc.position, owner, tc.owner)
		}
	}
	exec(`UPDATE public.position_pricing_owners SET owner_id=$2,origin='operator-assignment' WHERE client_position_id=$1`, mixed, memUserB)
	exec(sql)
	var owner string
	if err := tx.QueryRow(ctx, `SELECT owner_id::text FROM public.position_pricing_owners WHERE client_position_id=$1`, mixed).Scan(&owner); err != nil || owner != memUserB {
		t.Fatalf("rerun overwrote resolved owner: %s %v", owner, err)
	}
}
