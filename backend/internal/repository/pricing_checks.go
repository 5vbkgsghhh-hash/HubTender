package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/su10/hubtender/backend/internal/pricing"
	"github.com/su10/hubtender/backend/internal/quality"
)

// Explicit policy agreed with the engineer: guide sections 5.1, 5.2, 5.3,
// 5.4 except E/P, and 5.7. V and GA are outside this write gate. Existing
// fail-closed FX calculation is independent of this selection.
var directPricingRuleCodes = []string{"A", "AB", "F", "B", "M", "R", "S", "U", "H", "YD", "Y", "I", "J", "L", "QA", "AA", "N"}

type PricingChecksError struct{ Checks pricing.PricingChecks }

func (e *PricingChecksError) Error() string {
	parts := []string{}
	for _, f := range e.Checks.Findings {
		if f.Severity == quality.SeverityError {
			parts = append(parts, f.RuleCode+": "+f.Detail)
		}
	}
	return "MCP_PRICING_CHECKS_BLOCKED: " + strings.Join(parts, "; ")
}

func validateMCPPricingItemTx(ctx context.Context, tx pgx.Tx, p pricing.ProposedItem, parentID *string) error {
	bad := func(field string) error { return fmt.Errorf("MCP_PRICING_REQUIRED_FIELD: %s", field) }
	positive := func(v *float64) bool { return v != nil && *v > 0 && !math.IsNaN(*v) && !math.IsInf(*v, 0) }
	if p.BoqItemType != "суб-раб" && p.BoqItemType != "суб-мат" {
		return bad("boq_item_type must be суб-раб or суб-мат; complex types are not supported")
	}
	if !positive(p.Quantity) {
		return bad("quantity must be finite and positive")
	}
	if !positive(p.UnitRate) {
		return bad("unit_rate must be finite and positive")
	}
	if p.DetailCostCategoryID == nil || strings.TrimSpace(*p.DetailCostCategoryID) == "" {
		return bad("detail_cost_category_id (N/AA)")
	}
	if p.UnitCode == nil || strings.TrimSpace(*p.UnitCode) == "" {
		return bad("unit_code")
	}
	nameID, table := p.WorkNameID, "work_names"
	if p.BoqItemType == "суб-мат" {
		nameID, table = p.MaterialNameID, "material_names"
		if !positive(p.ConsumptionCoefficient) || *p.ConsumptionCoefficient < 1 {
			return bad("consumption_coefficient must be finite and at least 1")
		}
		if parentID != nil && !positive(p.ConversionCoefficient) {
			return bad("conversion_coefficient must be finite and positive")
		}
		if p.DeliveryPriceType == nil {
			return bad("delivery_price_type")
		}
		if *p.DeliveryPriceType == "суммой" && !positive(p.DeliveryAmount) {
			return bad("delivery_amount must be positive for суммой (L)")
		}
	}
	if nameID == nil || strings.TrimSpace(*nameID) == "" {
		return bad("nomenclature ID")
	}
	var name, unit string
	if err := tx.QueryRow(ctx, "SELECT name,unit FROM public."+table+" WHERE id=$1 FOR SHARE NOWAIT", *nameID).Scan(&name, &unit); err != nil {
		return fmt.Errorf("MCP_PRICING_NOMENCLATURE: %w", err)
	}
	if strings.TrimSpace(name) == "" || unit != *p.UnitCode {
		return bad("nomenclature must have a name and the same canonical unit as the BOQ row")
	}
	return nil
}

func runDirectPricingChecksTx(ctx context.Context, tx pgx.Tx, tenderID, positionID, proposedID string, isNew bool, previousGP *float64) (*pricing.PricingChecks, error) {
	report := &pricing.PricingChecks{Rules: append([]string{}, directPricingRuleCodes...), Findings: []pricing.PricingCheck{}}
	// IDs, rather than displayed position numbers, define the scope. Do not
	// reuse the portal's broad readiness flag as a write authorisation gate.
	scope := []string{positionID}
	rows, err := tx.Query(ctx, `SELECT id::text FROM public.boq_items WHERE client_position_id=$1 ORDER BY id`, positionID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		scope = append(scope, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	// Bind consent to all effective inputs and totals, including changes from
	// legacy routes which did not advance the tender financial revision.
	var snapshot struct {
		PositionName                     string
		PositionUnit                     *string
		CustomerVolume, ContractorVolume *float64
		Note                             *string
		Materials, Works                 float64
		Items                            []BoqItemRow
		PreviousContractorVolume         *float64
		Nomenclature                     []pricingNameSnapshot
	}
	if err := tx.QueryRow(ctx, `SELECT work_name,unit_code,volume,manual_volume,manual_note,COALESCE(total_material,0),COALESCE(total_works,0) FROM public.client_positions WHERE id=$1`, positionID).Scan(&snapshot.PositionName, &snapshot.PositionUnit, &snapshot.CustomerVolume, &snapshot.ContractorVolume, &snapshot.Note, &snapshot.Materials, &snapshot.Works); err != nil {
		return nil, err
	}
	snapshot.PreviousContractorVolume = previousGP
	snapshot.Items = []BoqItemRow{}
	itemRows, err := tx.Query(ctx, "SELECT "+boqScanCols+" FROM public.boq_items WHERE client_position_id=$1 ORDER BY id", positionID)
	if err != nil {
		return nil, err
	}
	for itemRows.Next() {
		item, err := scanBoqItemRow(itemRows)
		if err != nil {
			itemRows.Close()
			return nil, err
		}
		if isNew && item.ID == proposedID {
			item.ID = "proposed-item"
		}
		item.CreatedAt = time.Time{}
		item.UpdatedAt = time.Time{}
		snapshot.Items = append(snapshot.Items, *item)
	}
	err = itemRows.Err()
	itemRows.Close()
	if err != nil {
		return nil, err
	}
	sort.Slice(snapshot.Items, func(i, j int) bool { return snapshot.Items[i].ID < snapshot.Items[j].ID })
	snapshot.Nomenclature = []pricingNameSnapshot{}
	workIDs, materialIDs := []string{}, []string{}
	for _, item := range snapshot.Items {
		if item.WorkNameID != nil {
			workIDs = append(workIDs, *item.WorkNameID)
		}
		if item.MaterialNameID != nil {
			materialIDs = append(materialIDs, *item.MaterialNameID)
		}
	}
	for _, kind := range []struct {
		kind, table string
		ids         []string
	}{{"work", "work_names", workIDs}, {"material", "material_names", materialIDs}} {
		names, err := tx.Query(ctx, "SELECT id::text,name,unit FROM public."+kind.table+" WHERE id=ANY($1::uuid[]) ORDER BY id FOR SHARE NOWAIT", kind.ids)
		if err != nil {
			return nil, err
		}
		for names.Next() {
			n := pricingNameSnapshot{Kind: kind.kind}
			if err := names.Scan(&n.ID, &n.Name, &n.Unit); err != nil {
				names.Close()
				return nil, err
			}
			snapshot.Nomenclature = append(snapshot.Nomenclature, n)
		}
		err = names.Err()
		names.Close()
		if err != nil {
			return nil, err
		}
	}

	catalog := map[string]quality.Rule{}
	for _, r := range quality.Active() {
		catalog[r.Code] = r
	}
	for _, code := range directPricingRuleCodes {
		r, ok := catalog[code]
		if !ok {
			return nil, fmt.Errorf("MCP_PRICING_CHECK_UNAVAILABLE: %s is not active", code)
		}
		q := scopedDirectPricingRuleSQL(r.SQL)
		found, err := tx.Query(ctx, q, tenderID, scope, positionID)
		if err != nil {
			return nil, fmt.Errorf("MCP_PRICING_CHECK_FAILED: %s: %w", code, err)
		}
		for found.Next() {
			var tid, fingerprint, detail, entityID string
			var pos *float64
			var itemNo *string
			var delta *float64
			if err := found.Scan(&tid, &pos, &itemNo, &entityID, &fingerprint, &detail, &delta); err != nil {
				found.Close()
				return nil, err
			}
			if isNew && entityID == proposedID {
				entityID = "proposed-item"
			}
			report.Findings = append(report.Findings, pricing.PricingCheck{RuleCode: code, Severity: r.Severity, EntityID: entityID, Fingerprint: fingerprint, Detail: detail, MoneyDelta: delta})
			if r.Severity == quality.SeverityError {
				report.Blocking = true
			}
		}
		err = found.Err()
		found.Close()
		if err != nil {
			return nil, fmt.Errorf("MCP_PRICING_CHECK_FAILED: %s: %w", code, err)
		}
	}
	sort.Slice(report.Findings, func(i, j int) bool {
		a, b := report.Findings[i], report.Findings[j]
		return a.RuleCode+"|"+a.EntityID < b.RuleCode+"|"+b.EntityID
	})
	encoded, err := json.Marshal(struct {
		Catalog  string
		Findings []pricing.PricingCheck
		Snapshot any
	}{quality.CatalogHash(), report.Findings, snapshot})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	report.Fingerprint = hex.EncodeToString(sum[:])
	return report, nil
}

func proposedFromBoqRow(b *BoqItemRow) pricing.ProposedItem {
	return pricing.ProposedItem{BoqItemType: b.BoqItemType, Quantity: b.Quantity, UnitRate: b.UnitRate, UnitCode: b.UnitCode, WorkNameID: b.WorkNameID, MaterialNameID: b.MaterialNameID, DetailCostCategoryID: b.DetailCostCategoryID, ConsumptionCoefficient: b.ConsumptionCoefficient, ConversionCoefficient: b.ConversionCoefficient, DeliveryPriceType: b.DeliveryPriceType, DeliveryAmount: b.DeliveryAmount}
}

// Keep the catalogue's business predicates unchanged, but supply bounded input
// relations before its aggregates/joins. Include referenced external parents so
// legacy cross-position links still trigger F rather than disappearing.
func scopedDirectPricingRuleSQL(body string) string {
	body = strings.ReplaceAll(body, "public.boq_items", "mcp_pricing_scope_boq")
	body = strings.ReplaceAll(body, "public.client_positions", "mcp_pricing_scope_positions")
	return `WITH mcp_pricing_scope_positions AS MATERIALIZED (
  SELECT * FROM public.client_positions WHERE (id=$3::uuid AND tender_id=$1::uuid)
  OR id IN (SELECT w.client_position_id FROM public.boq_items b
   JOIN public.boq_items w ON w.id=b.parent_work_item_id
   WHERE b.client_position_id=$3::uuid AND b.tender_id=$1::uuid)
 ),mcp_pricing_scope_boq AS MATERIALIZED (
  SELECT * FROM public.boq_items WHERE client_position_id=$3::uuid AND tender_id=$1::uuid
  UNION ALL
  SELECT p.* FROM public.boq_items p WHERE p.id IN (
   SELECT parent_work_item_id FROM public.boq_items WHERE client_position_id=$3::uuid AND tender_id=$1::uuid
  ) AND (p.client_position_id<>$3::uuid OR p.tender_id<>$1::uuid)
 ) SELECT * FROM (` + strings.TrimSuffix(strings.TrimSpace(body), ";") + `) AS finding WHERE entity_id::text=ANY($2::text[])`
}

type pricingNameSnapshot struct{ ID, Kind, Name, Unit string }
