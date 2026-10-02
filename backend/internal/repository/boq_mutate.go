package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/su10/hubtender/backend/internal/calc"
)

// BoqItemPatch — этап 2.4 (§6): PATCH-вход с tri-state полями для nullable
// колонок, которые UI реально очищает (аудит §5). Остальные поля сохраняют
// семантику «absent = не менять» через *T.
type BoqItemPatch struct {
	BoqItemType            *string
	MaterialType           *string
	Description            *string
	UnitCode               *string
	Quantity               *float64
	UnitRate               *float64
	CurrencyType           *string
	DeliveryPriceType      *string
	DeliveryAmount         *float64
	ConsumptionCoefficient *float64
	SortNumber             *int
	QuoteLink              *string
	QuotePriceDate         *string // YYYY-MM-DD; "" = очистить; metadata-only (1.3)
	QuoteValidUntil        *string // YYYY-MM-DD; "" = очистить; metadata-only (1.3)

	// Tri-state (§6): различают absent / явный null / значение.
	BaseQuantity          OptionalNullable[float64]
	ConversionCoefficient OptionalNullable[float64]
	DetailCostCategoryID  OptionalNullable[string]
	MaterialNameID        OptionalNullable[string]
	WorkNameID            OptionalNullable[string]
	ParentWorkItemID      OptionalNullable[string]

	ChangedBy string // app users UUID for audit (changed_by)
}

// touchesAnything — есть ли хоть одно присутствующее поле.
func (p *BoqItemPatch) touchesAnything() bool {
	return p.BoqItemType != nil || p.MaterialType != nil || p.Description != nil ||
		p.UnitCode != nil || p.Quantity != nil || p.UnitRate != nil ||
		p.CurrencyType != nil || p.DeliveryPriceType != nil || p.DeliveryAmount != nil ||
		p.ConsumptionCoefficient != nil || p.SortNumber != nil ||
		p.QuoteLink != nil || p.QuotePriceDate != nil || p.QuoteValidUntil != nil ||
		p.BaseQuantity.Present || p.ConversionCoefficient.Present ||
		p.DetailCostCategoryID.Present || p.MaterialNameID.Present ||
		p.WorkNameID.Present || p.ParentWorkItemID.Present
}

// UpdateBoqItem применяет tri-state patch (§6) одним статическим typed-SQL
// UPDATE (CASE per column, без динамической сборки SET), пишет UPDATE-audit,
// пересчитывает total_amount — всё в одной транзакции.
func (r *BoqRepo) UpdateBoqItem(ctx context.Context, id string, in BoqItemPatch) (*BoqItemRow, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("boqRepo.UpdateBoqItem: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := skipBoqAuditTrigger(ctx, tx); err != nil {
		return nil, fmt.Errorf("boqRepo.UpdateBoqItem: %w", err)
	}

	// Lock and fetch current row inside the transaction.
	lockQ := "SELECT " + boqScanCols + " FROM public.boq_items WHERE id = $1 FOR UPDATE"
	oldItem, err := scanBoqItemRow(tx.QueryRow(ctx, lockQ, id))
	if err != nil {
		return nil, fmt.Errorf("boqRepo.UpdateBoqItem: lock row: %w", err)
	}

	// 1.3: quote source dates — валидация ДО каких-либо записей.
	if err := validateQuoteDates(in.QuotePriceDate, in.QuoteValidUntil, oldItem); err != nil {
		return nil, err
	}

	// 0-F2 (category A): one revision bump; commercial recalc follows async.
	// 1.3: patch, меняющий ТОЛЬКО source-метаданные (quote_link / даты цены),
	// НЕ является финансовым изменением — ревизия не двигается, approval не
	// снимается, recalc не нужен (метаданные не входят в формулу).
	// §6: очистка финансово значимого input (parent/base_quantity/коэффициент)
	// — финансовое изменение: ревизия двигается ровно один раз здесь же.
	if !isQuoteMetadataOnlyPatch(&in) {
		if _, err := MarkTenderFinancialInputsChangedTx(ctx, tx, oldItem.TenderID, "boq_update"); err != nil {
			return nil, fmt.Errorf("boqRepo.UpdateBoqItem: %w", err)
		}
	}

	touched := in.touchesAnything()
	var newItem *BoqItemRow
	if !touched {
		newItem = oldItem
	} else {
		// Статический typed-SET (§6): col = CASE WHEN $present THEN $value ELSE col END.
		// Для tri-state полей $value=NULL при явной очистке.
		bqP, bqV := in.BaseQuantity.arg()
		ccP, ccV := in.ConversionCoefficient.arg()
		dcP, dcV := in.DetailCostCategoryID.arg()
		mnP, mnV := in.MaterialNameID.arg()
		wnP, wnV := in.WorkNameID.arg()
		pwP, pwV := in.ParentWorkItemID.arg()
		ptrArg := func(p *string) (bool, any) {
			if p == nil {
				return false, nil
			}
			return true, *p
		}
		fArg := func(p *float64) (bool, any) {
			if p == nil {
				return false, nil
			}
			return true, *p
		}
		btP, btV := ptrArg(in.BoqItemType)
		mtP, mtV := ptrArg(in.MaterialType)
		deP, deV := ptrArg(in.Description)
		ucP, ucV := ptrArg(in.UnitCode)
		qtP, qtV := fArg(in.Quantity)
		urP, urV := fArg(in.UnitRate)
		cuP, cuV := ptrArg(in.CurrencyType)
		dtP, dtV := ptrArg(in.DeliveryPriceType)
		daP, daV := fArg(in.DeliveryAmount)
		coP, coV := fArg(in.ConsumptionCoefficient)
		snP, snV := false, any(nil)
		if in.SortNumber != nil {
			snP, snV = true, *in.SortNumber
		}
		qlP, qlV := ptrArg(in.QuoteLink)
		qdP, qdV := false, any(nil)
		if in.QuotePriceDate != nil {
			qdP, qdV = true, nullIfEmptyDate(*in.QuotePriceDate)
		}
		qvP, qvV := false, any(nil)
		if in.QuoteValidUntil != nil {
			qvP, qvV = true, nullIfEmptyDate(*in.QuoteValidUntil)
		}

		const updQ = `UPDATE public.boq_items SET
			boq_item_type           = CASE WHEN $2  THEN $3::public.boq_item_type      ELSE boq_item_type END,
			material_type           = CASE WHEN $4  THEN $5::public.material_type      ELSE material_type END,
			description             = CASE WHEN $6  THEN $7::text                      ELSE description END,
			unit_code               = CASE WHEN $8  THEN $9::text                      ELSE unit_code END,
			quantity                = CASE WHEN $10 THEN $11::numeric                  ELSE quantity END,
			unit_rate               = CASE WHEN $12 THEN $13::numeric                  ELSE unit_rate END,
			currency_type           = CASE WHEN $14 THEN $15::public.currency_type     ELSE currency_type END,
			delivery_price_type     = CASE WHEN $16 THEN $17::public.delivery_price_type ELSE delivery_price_type END,
			delivery_amount         = CASE WHEN $18 THEN $19::numeric                  ELSE delivery_amount END,
			consumption_coefficient = CASE WHEN $20 THEN $21::numeric                  ELSE consumption_coefficient END,
			sort_number             = CASE WHEN $22 THEN $23::integer                  ELSE sort_number END,
			quote_link              = CASE WHEN $24 THEN $25::text                     ELSE quote_link END,
			quote_price_date        = CASE WHEN $26 THEN $27::date                     ELSE quote_price_date END,
			quote_valid_until       = CASE WHEN $28 THEN $29::date                     ELSE quote_valid_until END,
			base_quantity           = CASE WHEN $30 THEN $31::numeric                  ELSE base_quantity END,
			conversion_coefficient  = CASE WHEN $32 THEN $33::numeric                  ELSE conversion_coefficient END,
			detail_cost_category_id = CASE WHEN $34 THEN $35::uuid                     ELSE detail_cost_category_id END,
			material_name_id        = CASE WHEN $36 THEN $37::uuid                     ELSE material_name_id END,
			work_name_id            = CASE WHEN $38 THEN $39::uuid                     ELSE work_name_id END,
			parent_work_item_id     = CASE WHEN $40 THEN $41::uuid                     ELSE parent_work_item_id END,
			updated_at              = NOW()
		WHERE id = $1 RETURNING ` + boqScanCols
		newItem, err = scanBoqItemRow(tx.QueryRow(ctx, updQ, id,
			btP, btV, mtP, mtV, deP, deV, ucP, ucV, qtP, qtV, urP, urV,
			cuP, cuV, dtP, dtV, daP, daV, coP, coV, snP, snV, qlP, qlV,
			qdP, qdV, qvP, qvV,
			bqP, bqV, ccP, ccV, dcP, dcV, mnP, mnV, wnP, wnV, pwP, pwV))
		if err != nil {
			return nil, fmt.Errorf("boqRepo.UpdateBoqItem: update scan: %w", err)
		}
	}

	// Recompute total_amount on every patch that touched a price input.
	// Parent clear (§6) проходит здесь же: standalone-семантика total.
	if touched {
		rates, err := loadTenderRates(ctx, tx, newItem.TenderID)
		if err != nil {
			return nil, fmt.Errorf("boqRepo.UpdateBoqItem: %w", err)
		}
		newTotal, err := calc.CalculateBoqItemTotalAmount(boqAmountInputFromRow(newItem), rates)
		if err != nil {
			return nil, fmt.Errorf("boqRepo.UpdateBoqItem: %w", err)
		}
		if newItem.TotalAmount == nil || *newItem.TotalAmount != newTotal {
			const totQ = "UPDATE public.boq_items SET total_amount = $1 WHERE id = $2 RETURNING " + boqScanCols
			newItem, err = scanBoqItemRow(tx.QueryRow(ctx, totQ, newTotal, id))
			if err != nil {
				return nil, fmt.Errorf("boqRepo.UpdateBoqItem: total_amount scan: %w", err)
			}
		}
		// Итоги позиции — производные от её строк (сумма и тип): в этой же транзакции.
		if err := recomputePositionTotalsByIDsTx(ctx, tx, []string{newItem.ClientPositionID}); err != nil {
			return nil, fmt.Errorf("boqRepo.UpdateBoqItem: %w", err)
		}
	}

	oldJSON, _ := boqRowJSON(oldItem)
	newJSON, _ := boqRowJSON(newItem)
	fields := changedFields(oldItem, newItem)

	if err := insertAudit(ctx, tx, id, "UPDATE", in.ChangedBy, fields, oldJSON, newJSON); err != nil {
		return nil, fmt.Errorf("boqRepo.UpdateBoqItem: audit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("boqRepo.UpdateBoqItem: commit: %w", err)
	}
	return newItem, nil
}

// DeleteBoqItem deletes a boq_item and writes a DELETE audit row, all in one
// transaction. Returns the deleted row so the caller can include it in the
// response body.
func (r *BoqRepo) DeleteBoqItem(ctx context.Context, id, changedBy string) (*BoqItemRow, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("boqRepo.DeleteBoqItem: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if err := skipBoqAuditTrigger(ctx, tx); err != nil {
		return nil, fmt.Errorf("boqRepo.DeleteBoqItem: %w", err)
	}

	// Lock the row first so we capture a stable snapshot for the audit.
	lockQ := "SELECT " + boqScanCols + " FROM public.boq_items WHERE id = $1 FOR UPDATE"
	item, err := scanBoqItemRow(tx.QueryRow(ctx, lockQ, id))
	if err != nil {
		return nil, fmt.Errorf("boqRepo.DeleteBoqItem: lock row: %w", err)
	}

	// 0-F2 (category A): one revision bump; the sync grand-total refresh below
	// keeps the cached value fresh, but the commercial recalc is async → stale.
	if _, err := MarkTenderFinancialInputsChangedTx(ctx, tx, item.TenderID, "boq_delete"); err != nil {
		return nil, fmt.Errorf("boqRepo.DeleteBoqItem: %w", err)
	}

	if _, err := tx.Exec(ctx, "DELETE FROM public.boq_items WHERE id = $1", id); err != nil {
		return nil, fmt.Errorf("boqRepo.DeleteBoqItem: delete: %w", err)
	}
	if err := recomputePositionTotalsByIDsTx(ctx, tx, []string{item.ClientPositionID}); err != nil {
		return nil, fmt.Errorf("boqRepo.DeleteBoqItem: %w", err)
	}

	// Категория A (0.1.2.4a): удаление строки с materialized commercial values
	// немедленно меняет состав cached_grand_total — пересчёт в ЭТОЙ транзакции
	// (per-row SQL-триггеров больше нет).
	if _, err := RecalculateTenderGrandTotalTx(ctx, tx, item.TenderID); err != nil {
		return nil, fmt.Errorf("boqRepo.DeleteBoqItem: grand total: %w", err)
	}

	oldJSON, _ := boqRowJSON(item)
	if err := insertAudit(ctx, tx, id, "DELETE", changedBy, nil, oldJSON, nil); err != nil {
		return nil, fmt.Errorf("boqRepo.DeleteBoqItem: audit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("boqRepo.DeleteBoqItem: commit: %w", err)
	}
	return item, nil
}

// ─── 1.3: source metadata (quote dates) ──────────────────────────────────────

// InvalidQuoteDatesError — некорректные даты источника (400 на write-path).
type InvalidQuoteDatesError struct {
	Reason string
}

func (e *InvalidQuoteDatesError) Error() string {
	return "INVALID_QUOTE_DATES: " + e.Reason
}

// Code returns the stable machine-readable code.
func (e *InvalidQuoteDatesError) Code() string { return "INVALID_QUOTE_DATES" }

// isQuoteMetadataOnlyPatch — true, если patch затрагивает ТОЛЬКО справочные
// source-поля (quote_link, даты цены): такие правки не финансовые (§3 этапа
// 1.3) — ревизия/approval/recalc не трогаются. Tri-state поля (§6) считаются
// финансовыми при ЛЮБОМ присутствии (включая явную очистку null).
func isQuoteMetadataOnlyPatch(in *BoqItemPatch) bool {
	touchesMetadata := in.QuoteLink != nil || in.QuotePriceDate != nil || in.QuoteValidUntil != nil
	touchesFinancial := in.BoqItemType != nil || in.MaterialType != nil || in.Description != nil ||
		in.UnitCode != nil || in.Quantity != nil ||
		in.UnitRate != nil || in.CurrencyType != nil ||
		in.DeliveryPriceType != nil || in.DeliveryAmount != nil || in.ConsumptionCoefficient != nil ||
		in.SortNumber != nil ||
		in.BaseQuantity.Present || in.ConversionCoefficient.Present ||
		in.DetailCostCategoryID.Present || in.MaterialNameID.Present ||
		in.WorkNameID.Present || in.ParentWorkItemID.Present
	return touchesMetadata && !touchesFinancial
}

// nullIfEmptyDate — "" → NULL (очистка), иначе дата как есть (валидация выше).
func nullIfEmptyDate(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.TrimSpace(s)
}

// validateQuoteDates — семантика §3: формат YYYY-MM-DD; price_date не в
// будущем относительно СЕРВЕРНОЙ даты; valid_until >= price_date (учитывая
// уже сохранённые значения при частичном patch'е). Пустые значения разрешены;
// valid_until в прошлом — допустимые исторические данные.
func validateQuoteDates(priceDate, validUntil *string, old *BoqItemRow) error {
	parse := func(field string, p *string, fallback *string) (*time.Time, error) {
		var raw *string
		if p != nil {
			if strings.TrimSpace(*p) == "" {
				return nil, nil // явная очистка
			}
			raw = p
		} else {
			raw = fallback
		}
		if raw == nil || strings.TrimSpace(*raw) == "" {
			return nil, nil
		}
		t, err := time.Parse("2006-01-02", strings.TrimSpace(*raw))
		if err != nil {
			return nil, &InvalidQuoteDatesError{Reason: field + ": ожидается дата в формате ГГГГ-ММ-ДД"}
		}
		return &t, nil
	}
	pd, err := parse("Дата цены", priceDate, old.QuotePriceDate)
	if err != nil {
		return err
	}
	vu, err := parse("Действительно до", validUntil, old.QuoteValidUntil)
	if err != nil {
		return err
	}
	if pd != nil && pd.After(time.Now().UTC().Truncate(24*time.Hour).Add(24*time.Hour-time.Second)) {
		return &InvalidQuoteDatesError{Reason: "дата цены не может быть в будущем"}
	}
	if pd != nil && vu != nil && vu.Before(*pd) {
		return &InvalidQuoteDatesError{Reason: "срок действия не может быть раньше даты цены"}
	}
	return nil
}
