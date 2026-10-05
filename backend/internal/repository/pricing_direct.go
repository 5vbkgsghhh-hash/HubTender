package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/su10/hubtender/backend/internal/calc"
	"github.com/su10/hubtender/backend/internal/pricing"
)

var (
	ErrDirectPricingStale     = errors.New("DIRECT_PRICING_STALE: reload the tender and item before writing")
	ErrDirectRequestKeyReused = errors.New("DIRECT_REQUEST_KEY_REUSED: use a new request key for different inputs")
	ErrDirectTargetInvalid    = errors.New("DIRECT_TARGET_INVALID: position must be a leaf in the selected tender")
	ErrDirectParentInvalid    = errors.New("DIRECT_PARENT_INVALID: parent must be a work row in the same VOR position")
	ErrLinkedMaterialQuantity = errors.New("LINKED_MATERIAL_QUANTITY_READ_ONLY: omit quantity; the server calculates it from the parent work and stored consumption; edit conversion_coefficient for unit conversion")
)

type DirectPricingCommit struct {
	TenderID          string
	ActorID           string
	RequestKey        string
	RequestHash       string
	ExpectedRevision  int64
	Operation         pricing.DirectOperation
	ParentWorkItemID  *string
	RequestedQuantity *float64
}

// GetDirectPricingReceipt returns a committed command by actor and request
// key. A missing row is not an error, so callers can check before rebuilding
// a source-backed operation after a lost MCP response.
func (r *PricingRepo) GetDirectPricingReceipt(ctx context.Context, actorID, requestKey string) (*pricing.DirectPricingResult, string, error) {
	var hash string
	var raw []byte
	err := r.pool.QueryRow(ctx, `SELECT request_hash,result
		FROM public.mcp_direct_pricing_requests
		WHERE actor_id=$1 AND request_key=$2`, actorID, requestKey).Scan(&hash, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var result pricing.DirectPricingResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, "", err
	}
	return &result, hash, nil
}

func (r *PricingRepo) ListDirectBoqItems(ctx context.Context, tenderID, positionID string, limit, offset int) ([]BoqItemRow, int, error) {
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM public.boq_items
		WHERE tender_id=$1 AND client_position_id=$2`, tenderID, positionID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, "SELECT "+boqScanCols+
		" FROM public.boq_items WHERE tender_id=$1 AND client_position_id=$2 ORDER BY sort_number,id LIMIT $3 OFFSET $4",
		tenderID, positionID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []BoqItemRow{}
	for rows.Next() {
		item, err := scanBoqItemRow(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}
	return items, total, rows.Err()
}

func (r *PricingRepo) ListDirectCostCategories(ctx context.Context, search string, limit, offset int) ([]pricing.CostCategory, int, error) {
	var total int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM public.detail_cost_categories
		WHERE $1='' OR name ILIKE '%'||$1||'%'`, search).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, `SELECT id::text,name,unit,location,cost_category_id::text
		FROM public.detail_cost_categories
		WHERE $1='' OR name ILIKE '%'||$1||'%'
		ORDER BY name,id LIMIT $2 OFFSET $3`, search, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []pricing.CostCategory{}
	for rows.Next() {
		var item pricing.CostCategory
		if err := rows.Scan(&item.ID, &item.Name, &item.Unit, &item.Location, &item.CategoryID); err != nil {
			return nil, 0, err
		}
		items = append(items, item)
	}
	return items, total, rows.Err()
}

// ApplyDirectPricing commits one source-backed BOQ mutation. The request key
// makes creation safe to retry when an MCP response is lost after commit.
func (r *PricingRepo) ApplyDirectPricing(ctx context.Context, in DirectPricingCommit) (*pricing.DirectPricingResult, error) {
	// The tender and target rows are locked explicitly. Read committed lets an
	// identical concurrent request observe the committed command receipt after
	// ON CONFLICT waits, while revision/ETag checks still reject stale writes.
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var inserted int
	err = tx.QueryRow(ctx, `INSERT INTO public.mcp_direct_pricing_requests
		(actor_id,request_key,request_hash,tender_id,result)
		VALUES ($1,$2,$3,$4,'{}'::jsonb)
		ON CONFLICT (actor_id,request_key) DO NOTHING RETURNING 1`,
		in.ActorID, in.RequestKey, in.RequestHash, in.TenderID).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		var existingHash string
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT request_hash,result
			FROM public.mcp_direct_pricing_requests
			WHERE actor_id=$1 AND request_key=$2 FOR UPDATE`,
			in.ActorID, in.RequestKey).Scan(&existingHash, &raw); err != nil {
			return nil, err
		}
		if existingHash != in.RequestHash {
			return nil, ErrDirectRequestKeyReused
		}
		var prior pricing.DirectPricingResult
		if err := json.Unmarshal(raw, &prior); err != nil {
			return nil, err
		}
		prior.Replayed = true
		return &prior, nil
	}
	if err != nil {
		return nil, err
	}

	var revision int64
	var rates calc.CurrencyRates
	err = tx.QueryRow(ctx, `SELECT financial_input_revision,usd_rate,eur_rate,cny_rate
		FROM public.tenders WHERE id=$1 FOR UPDATE`, in.TenderID).
		Scan(&revision, &rates.USDRate, &rates.EURRate, &rates.CNYRate)
	if err != nil {
		return nil, err
	}
	if revision != in.ExpectedRevision {
		return nil, ErrDirectPricingStale
	}
	var positionTender string
	var leaf bool
	err = tx.QueryRow(ctx, `SELECT cp.tender_id::text,
		NOT EXISTS (SELECT 1 FROM public.client_positions child WHERE child.parent_position_id=cp.id)
		FROM public.client_positions cp WHERE cp.id=$1 FOR UPDATE OF cp`,
		in.Operation.TargetPositionID).Scan(&positionTender, &leaf)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDirectTargetInvalid
		}
		return nil, err
	}
	if positionTender != in.TenderID || !leaf {
		return nil, ErrDirectTargetInvalid
	}
	// Check the selected rate under a lock in the same transaction as the BOQ
	// write, closing the service-read/commit race with a source editor.
	if in.Operation.SourceKind != "current" {
		var sourceRate *float64
		src := in.Operation.SourceRef
		switch in.Operation.SourceKind {
		case "archive":
			if src.ItemID == nil {
				return nil, ErrDirectPricingStale
			}
			err = tx.QueryRow(ctx, "SELECT unit_rate FROM public.boq_items WHERE id=$1 FOR SHARE", *src.ItemID).Scan(&sourceRate)
		case "library":
			if src.LibraryID == nil {
				return nil, ErrDirectPricingStale
			}
			query := "SELECT unit_rate FROM public.materials_library WHERE id=$1 FOR SHARE"
			if calc.IsWorkBoqType(in.Operation.ProposedPayload.BoqItemType) {
				query = "SELECT unit_rate FROM public.works_library WHERE id=$1 FOR SHARE"
			}
			err = tx.QueryRow(ctx, query, *src.LibraryID).Scan(&sourceRate)
		default:
			return nil, ErrDirectTargetInvalid
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDirectPricingStale
		}
		if err != nil {
			return nil, err
		}
		if sourceRate == nil || src.Rate == nil || math.Abs(*sourceRate-*src.Rate) > 0.000001 {
			return nil, ErrDirectPricingStale
		}
	}

	var old *BoqItemRow
	if in.Operation.Action == "update_item" {
		if in.Operation.TargetItemID == nil || in.Operation.ExpectedETag == nil {
			return nil, ErrDirectPricingStale
		}
		old, err = scanBoqItemRow(tx.QueryRow(ctx, "SELECT "+boqScanCols+" FROM public.boq_items WHERE id=$1 FOR UPDATE", *in.Operation.TargetItemID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrDirectPricingStale
			}
			return nil, err
		}
		if old.TenderID != in.TenderID || old.ClientPositionID != in.Operation.TargetPositionID ||
			pricingETag(old.ID, old.UpdatedAt) != *in.Operation.ExpectedETag {
			return nil, ErrDirectPricingStale
		}
	} else if in.Operation.Action != "create_item" || in.Operation.TargetItemID != nil {
		return nil, ErrDirectTargetInvalid
	}
	payload := in.Operation.ProposedPayload
	parentID := in.ParentWorkItemID
	if old != nil {
		parentID = old.ParentWorkItemID
	}
	if parentID != nil {
		if in.RequestedQuantity != nil || payload.Quantity != nil {
			return nil, ErrLinkedMaterialQuantity
		}
		if !calc.IsMaterialBoqType(payload.BoqItemType) {
			return nil, ErrDirectParentInvalid
		}
		parent, err := scanBoqItemRow(tx.QueryRow(ctx, "SELECT "+boqScanCols+
			" FROM public.boq_items WHERE id=$1 FOR UPDATE", *parentID))
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return nil, ErrDirectParentInvalid
			}
			return nil, err
		}
		if parent.TenderID != in.TenderID ||
			(old == nil && parent.ClientPositionID != in.Operation.TargetPositionID) ||
			!calc.IsWorkBoqType(parent.BoqItemType) || parent.Quantity == nil {
			return nil, ErrDirectParentInvalid
		}
		quantity, err := calc.CalculateLinkedMaterialQuantity(*parent.Quantity, payload.ConversionCoefficient, payload.ConsumptionCoefficient)
		if err != nil {
			return nil, err
		}
		payload.Quantity, payload.BaseQuantity = &quantity, nil
	}
	in.Operation.ProposedPayload = payload

	if err := skipBoqAuditTrigger(ctx, tx); err != nil {
		return nil, err
	}
	newRevision, err := MarkTenderFinancialInputsChangedTx(ctx, tx, in.TenderID, "mcp_direct_pricing")
	if err != nil {
		return nil, err
	}
	var item *BoqItemRow
	if old != nil {
		if err := validateQuoteDates(payload.QuotePriceDate, payload.QuoteValidUntil, old); err != nil {
			return nil, err
		}
		total, err := calculateProposedTotal(payload, old.ParentWorkItemID, rates)
		if err != nil {
			return nil, err
		}
		item, err = updatePricingItemTx(ctx, tx, old.ID, payload, old.ParentWorkItemID, total)
		if err != nil {
			return nil, err
		}
		oldJSON, _ := boqRowJSON(old)
		newJSON, _ := boqRowJSON(item)
		if err := insertAudit(ctx, tx, item.ID, "UPDATE", in.ActorID, changedFields(old, item), oldJSON, newJSON); err != nil {
			return nil, err
		}
	} else {
		if err := validateQuoteDates(payload.QuotePriceDate, payload.QuoteValidUntil, &BoqItemRow{}); err != nil {
			return nil, err
		}
		total, err := calculateProposedTotal(payload, parentID, rates)
		if err != nil {
			return nil, err
		}
		item, err = insertPricingItemTx(ctx, tx, in.TenderID, in.Operation, parentID, total)
		if err != nil {
			return nil, err
		}
		newJSON, _ := boqRowJSON(item)
		if err := insertAudit(ctx, tx, item.ID, "INSERT", in.ActorID, nil, nil, newJSON); err != nil {
			return nil, err
		}
	}
	etag := pricingETag(item.ID, item.UpdatedAt)
	src := in.Operation.SourceRef
	warningsJSON, err := json.Marshal(in.Operation.Warnings)
	if err != nil {
		return nil, err
	}
	if in.Operation.SourceKind != "current" {
		_, err = tx.Exec(ctx, `INSERT INTO public.boq_item_pricing_sources
		(boq_item_id,draft_operation_id,source_kind,source_tender_id,source_item_id,
		source_template_id,source_library_id,source_rate,source_currency,source_date,
		applied_by,direct_request_key,direct_match_level,direct_confidence,direct_warnings)
		VALUES ($1,NULL,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb)`,
			item.ID, in.Operation.SourceKind, src.TenderID, src.ItemID, src.TemplateID,
			src.LibraryID, src.Rate, src.Currency, src.Date, in.ActorID,
			in.RequestKey, in.Operation.MatchLevel, in.Operation.Confidence, warningsJSON)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return nil, ErrDirectRequestKeyReused
			}
			return nil, fmt.Errorf("insert direct pricing source: %w", err)
		}
	}
	children := []*BoqItemRow{}
	if calc.IsWorkBoqType(item.BoqItemType) && old != nil {
		children, err = recomputeLinkedMaterialsTx(ctx, tx, item, in.ActorID, rates)
		if err != nil {
			return nil, err
		}
	}
	if err := recomputePositionTotalsByIDsTx(ctx, tx, []string{in.Operation.TargetPositionID}); err != nil {
		return nil, err
	}
	result := directPricingResult(in, item, newRevision, false)
	for _, child := range children {
		result.LinkedMaterials = append(result.LinkedMaterials, pricing.LinkedMaterialResult{
			ItemID: child.ID, Quantity: *child.Quantity, TotalAmount: *child.TotalAmount,
			ETag: pricingETag(child.ID, child.UpdatedAt),
		})
	}
	result.ETag = etag
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE public.mcp_direct_pricing_requests SET result=$3::jsonb
		WHERE actor_id=$1 AND request_key=$2`, in.ActorID, in.RequestKey, encoded); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return result, nil
}

func directPricingResult(in DirectPricingCommit, item *BoqItemRow, revision int64, replayed bool) *pricing.DirectPricingResult {
	result := &pricing.DirectPricingResult{
		RequestKey: in.RequestKey, TenderID: in.TenderID, PositionID: item.ClientPositionID,
		ItemID: item.ID, Action: in.Operation.Action, SourceKind: in.Operation.SourceKind,
		ETag: pricingETag(item.ID, item.UpdatedAt), FinancialInputRevision: revision,
		Warnings: in.Operation.Warnings, Replayed: replayed,
		ParentWorkItemID: item.ParentWorkItemID, ConversionCoefficient: item.ConversionCoefficient,
		ConsumptionCoefficient: item.ConsumptionCoefficient, LinkedMaterials: []pricing.LinkedMaterialResult{},
	}
	if item.Quantity != nil {
		result.Quantity = *item.Quantity
	}
	if item.UnitRate != nil {
		result.UnitRate = *item.UnitRate
	}
	if item.CurrencyType != nil {
		result.Currency = *item.CurrencyType
	}
	if item.TotalAmount != nil {
		result.TotalAmount = *item.TotalAmount
	}
	if in.Operation.SourceRef.ItemID != nil {
		result.SourceID = *in.Operation.SourceRef.ItemID
	} else if in.Operation.SourceRef.LibraryID != nil {
		result.SourceID = *in.Operation.SourceRef.LibraryID
	}
	return result
}
