package repository

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/su10/hubtender/backend/internal/quality"
)

func TestDirectPricingScopedRulesMatchCatalogIncludingExternalParent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tender, position := seedSourceTender(t, pool, "CHECK-SCOPE")
	workName, matName := ensureTestNames(t, pool)
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
	var other, work, material string
	if err := tx.QueryRow(ctx, `INSERT INTO public.client_positions(tender_id,position_number,work_name,volume,manual_volume,unit_code) VALUES($1,2,'External parent',10,10,'м2') RETURNING id::text`, tender).Scan(&other); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE public.client_positions SET volume=100,manual_volume=1,unit_code='м2' WHERE id=$1`, position)
	if err := tx.QueryRow(ctx, `INSERT INTO public.boq_items(tender_id,client_position_id,boq_item_type,work_name_id,unit_code,quantity,unit_rate,total_amount,currency_type) VALUES($1,$2,'суб-раб',$3,'м2',1,100,1000,'RUB') RETURNING id::text`, tender, other, workName).Scan(&work); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO public.boq_items(tender_id,client_position_id,boq_item_type,material_name_id,unit_code,quantity,unit_rate,total_amount,currency_type,consumption_coefficient,conversion_coefficient,delivery_price_type,delivery_amount) VALUES($1,$2,'суб-мат',$3,'шт',1000,0,100,'RUB',1,1,'суммой',0) RETURNING id::text`, tender, position, matName).Scan(&material); err != nil {
		t.Fatal(err)
	}
	// Reproduce legacy bad binding without removing production FK constraints.
	exec(`SET LOCAL session_replication_role=replica`)
	exec(`UPDATE public.boq_items SET parent_work_item_id=$2 WHERE id=$1`, material, work)
	exec(`SET LOCAL session_replication_role=origin`)
	scope := []string{position, material}
	collect := func(q string, args ...any) []string {
		t.Helper()
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := []string{}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				t.Fatal(err)
			}
			result = append(result, v)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		sort.Strings(result)
		return result
	}
	catalog := map[string]quality.Rule{}
	for _, r := range quality.Active() {
		catalog[r.Code] = r
	}
	for _, code := range directPricingRuleCodes {
		r := catalog[code]
		original := `SELECT row_to_json(f)::text FROM (` + strings.TrimSuffix(strings.TrimSpace(r.SQL), ";") + `) f WHERE entity_id::text=ANY($2::text[])`
		scoped := `SELECT row_to_json(f)::text FROM (` + scopedDirectPricingRuleSQL(r.SQL) + `) f`
		want := collect(original, tender, scope)
		got := collect(scoped, tender, scope, position)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s scoped=%v original=%v", code, got, want)
		}
		if code == "F" && len(got) != 1 {
			t.Fatalf("legacy external parent hidden: %v", got)
		}
	}
}
