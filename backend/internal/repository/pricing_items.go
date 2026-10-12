package repository

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/su10/hubtender/backend/internal/calc"
	"github.com/su10/hubtender/backend/internal/pricing"
)

type TargetPosition struct {
	ID           string
	TenderID     string
	WorkName     string
	UnitCode     *string
	Volume       *float64
	ManualVolume *float64
	MaxSort      int
}

func (r *PricingRepo) GetTargetPosition(ctx context.Context, id string) (*TargetPosition, error) {
	var p TargetPosition
	err := r.pool.QueryRow(ctx, `
		SELECT cp.id::text,cp.tender_id::text,cp.work_name,cp.unit_code,cp.volume,cp.manual_volume,
		       COALESCE(max(bi.sort_number),0)
		FROM public.client_positions cp LEFT JOIN public.boq_items bi ON bi.client_position_id=cp.id
		WHERE cp.id=$1 GROUP BY cp.id`, id).Scan(&p.ID, &p.TenderID, &p.WorkName, &p.UnitCode, &p.Volume, &p.ManualVolume, &p.MaxSort)
	return &p, err
}

func (r *PricingRepo) GetCurrentBoqItem(ctx context.Context, id string) (*BoqItemRow, string, error) {
	item, err := scanBoqItemRow(r.pool.QueryRow(ctx, "SELECT "+boqScanCols+" FROM public.boq_items WHERE id=$1", id))
	if err != nil {
		return nil, "", err
	}
	return item, pricingETag(item.ID, item.UpdatedAt), nil
}

func calculateProposedTotal(p pricing.ProposedItem, parentID *string, rates calc.CurrencyRates) (float64, error) {
	currency, delivery := "", ""
	if p.CurrencyType != nil {
		currency = *p.CurrencyType
	}
	if p.DeliveryPriceType != nil {
		delivery = *p.DeliveryPriceType
	}
	return calc.CalculateBoqItemTotalAmount(calc.BoqItemAmountInput{
		BoqItemType: p.BoqItemType, Quantity: p.Quantity, UnitRate: p.UnitRate,
		CurrencyType: currency, DeliveryPriceType: delivery, DeliveryAmount: p.DeliveryAmount,
		ConsumptionCoefficient: p.ConsumptionCoefficient, ParentWorkItemID: parentID,
	}, rates)
}

func insertPricingItemTx(ctx context.Context, tx pgx.Tx, tenderID string, op pricing.DirectOperation, parentID *string, total float64) (*BoqItemRow, error) {
	p := op.ProposedPayload
	sortNum := op.PositionOrder
	if p.SortNumber != nil {
		sortNum = *p.SortNumber
	}
	q := `INSERT INTO public.boq_items
	 (client_position_id,tender_id,boq_item_type,material_type,description,unit_code,quantity,base_quantity,
	  conversion_coefficient,unit_rate,currency_type,delivery_price_type,delivery_amount,consumption_coefficient,
	  total_amount,detail_cost_category_id,material_name_id,work_name_id,parent_work_item_id,sort_number,quote_link,
	  quote_price_date,quote_valid_until)
	 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22::date,$23::date)
	 RETURNING ` + boqScanCols
	return scanBoqItemRow(tx.QueryRow(ctx, q, op.TargetPositionID, tenderID, p.BoqItemType, p.MaterialType,
		p.Description, p.UnitCode, p.Quantity, p.BaseQuantity, p.ConversionCoefficient, p.UnitRate,
		p.CurrencyType, p.DeliveryPriceType, p.DeliveryAmount, p.ConsumptionCoefficient, total,
		p.DetailCostCategoryID, p.MaterialNameID, p.WorkNameID, parentID, sortNum, p.QuoteLink,
		p.QuotePriceDate, p.QuoteValidUntil))
}

func updatePricingItemTx(ctx context.Context, tx pgx.Tx, id string, p pricing.ProposedItem, parentID *string, total float64) (*BoqItemRow, error) {
	q := `UPDATE public.boq_items SET boq_item_type=$2,material_type=$3,description=$4,unit_code=$5,
	 quantity=$6,base_quantity=$7,conversion_coefficient=$8,unit_rate=$9,currency_type=$10,
	 delivery_price_type=$11,delivery_amount=$12,consumption_coefficient=$13,total_amount=$14,
	 detail_cost_category_id=$15,material_name_id=$16,work_name_id=$17,parent_work_item_id=$18,
	 sort_number=COALESCE($19,sort_number),quote_link=$20,quote_price_date=$21::date,
	 quote_valid_until=$22::date,updated_at=now() WHERE id=$1 RETURNING ` + boqScanCols
	return scanBoqItemRow(tx.QueryRow(ctx, q, id, p.BoqItemType, p.MaterialType, p.Description, p.UnitCode,
		p.Quantity, p.BaseQuantity, p.ConversionCoefficient, p.UnitRate, p.CurrencyType,
		p.DeliveryPriceType, p.DeliveryAmount, p.ConsumptionCoefficient, total, p.DetailCostCategoryID,
		p.MaterialNameID, p.WorkNameID, parentID, p.SortNumber, p.QuoteLink, p.QuotePriceDate, p.QuoteValidUntil))
}

func pricingETag(id string, updatedAt time.Time) string {
	ts := updatedAt.UTC().Format(time.RFC3339Nano)
	encoded := base64.RawURLEncoding.EncodeToString([]byte(ts))
	sum := sha256.Sum256([]byte(id + "|" + ts))
	return `"` + encoded + ":" + hex.EncodeToString(sum[:4]) + `"`
}
