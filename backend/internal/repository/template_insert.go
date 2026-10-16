package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/su10/hubtender/backend/internal/calc"
)

// Sentinel errors returned by InsertTemplateItems for client-meaningful
// failures (mapped to 4xx by the handler). Messages are Russian because they
// surface directly in the UI, preserving the legacy TypeScript behaviour.
var (
	ErrTemplateNotFound  = errors.New("Шаблон не найден")
	ErrTemplateEmpty     = errors.New("Шаблон пуст")
	ErrPositionNotFound  = errors.New("Позиция заказчика не найдена")
	ErrTemplateItemNoLib = errors.New("элемент шаблона не имеет ссылки на библиотеку")
)

// TemplateInsertResult mirrors the legacy InsertTemplateResult shape.
// TenderID is carried for cache invalidation only (not serialized).
type TemplateInsertResult struct {
	WorksCount     int    `json:"worksCount"`
	MaterialsCount int    `json:"materialsCount"`
	TotalInserted  int    `json:"totalInserted"`
	TenderID       string `json:"-"`
}

// tmplItemRow is one template_items row joined with its works/materials
// library + name unit, ordered by position.
type tmplItemRow struct {
	ID        string
	Kind      string
	ParentTID *string
	ConvCoeff *float64
	Note      *string
	DCC       *string
	HasWL     bool
	HasML     bool
	// works_library
	WNameID   *string
	WUnitRate *float64
	WCur      *string
	WItemType *string
	WUnit     *string
	// materials_library
	MNameID   *string
	MUnitRate *float64
	MCur      *string
	MItemType *string
	MMatType  *string
	MDPT      *string
	MDelivAmt *float64
	MConsCoef *float64
	MUnit     *string
}

const tmplItemsQ = `
	SELECT
		ti.id::text, ti.kind, ti.parent_work_item_id::text,
		ti.conversation_coeff, ti.note, ti.detail_cost_category_id::text,
		(wl.id IS NOT NULL) AS has_wl,
		(ml.id IS NOT NULL) AS has_ml,
		wl.work_name_id::text, wl.unit_rate, wl.currency_type::text,
		wl.item_type::text, wn.unit,
		ml.material_name_id::text, ml.unit_rate, ml.currency_type::text,
		ml.item_type::text, ml.material_type::text,
		ml.delivery_price_type::text, ml.delivery_amount,
		ml.consumption_coefficient, mn.unit
	FROM public.template_items ti
	LEFT JOIN public.works_library    wl ON wl.id = ti.work_library_id
	LEFT JOIN public.work_names       wn ON wn.id = wl.work_name_id
	LEFT JOIN public.materials_library ml ON ml.id = ti.material_library_id
	LEFT JOIN public.material_names   mn ON mn.id = ml.material_name_id
	WHERE ti.template_id = $1
	ORDER BY ti.position ASC
`

func orOne(p *float64) float64 {
	if p == nil || *p == 0 {
		return 1
	}
	return *p
}

func orZero(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// InsertTemplateItems inserts every item of a template into a client position,
// restoring parent_work_item_id links, recomputing position totals, and
// writing INSERT/UPDATE audit rows — all in one transaction.
//
// The operation runs in two phases: PLANNING (read-only — validate every parent
// link, normalize every row and price it via calc) and PERSISTENCE (insert, link,
// audit, totals). Nothing is written until the whole batch validates.
//
// A row counts as a CHILD only when its parent_work_item_id will really point at
// an inserted WORK row. A declared-but-invalid parent (missing / non-work / self)
// is a blocking InvalidTemplateParentError — never silently downgraded to a
// standalone material, which would change the money and hide a corrupt template.
//
// total_amount is derived EXCLUSIVELY by calc.CalculateBoqItemTotalAmount — the
// same authoritative kernel and the same rules as CreateBoqItem (consumption
// coefficient, delivery matrix, and blocking MissingFXRateError on a missing or
// non-positive FX rate). The template library stores only the source inputs; it
// never stores or supplies a money total. Currency rates are loaded ONCE for the
// whole operation (no per-row query). Any calc error aborts the whole insert and
// rolls the transaction back — no partial rows, no audit, no totals update.
//
// NOTE: Yandex public.boq_items has no created_by column (the audit actor is
// boq_items_audit.changed_by), so it is intentionally absent from the INSERT.
func (r *BoqRepo) InsertTemplateItems(
	ctx context.Context,
	templateID, clientPositionID, changedBy string,
) (*TemplateInsertResult, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("boqRepo.InsertTemplateItems: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := setAuditUser(ctx, tx, changedBy); err != nil {
		return nil, err
	}

	if err := skipBoqAuditTrigger(ctx, tx); err != nil {
		return nil, fmt.Errorf("boqRepo.InsertTemplateItems: %w", err)
	}

	// 1. Template (default detail_cost_category_id).
	var tmplDCC string
	err = tx.QueryRow(ctx,
		`SELECT detail_cost_category_id::text FROM public.templates WHERE id = $1`,
		templateID,
	).Scan(&tmplDCC)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrTemplateNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("boqRepo.InsertTemplateItems: template: %w", err)
	}

	// 2. Template items in position order.
	rows, err := tx.Query(ctx, tmplItemsQ, templateID)
	if err != nil {
		return nil, fmt.Errorf("boqRepo.InsertTemplateItems: items query: %w", err)
	}
	var items []tmplItemRow
	for rows.Next() {
		var t tmplItemRow
		if scanErr := rows.Scan(
			&t.ID, &t.Kind, &t.ParentTID,
			&t.ConvCoeff, &t.Note, &t.DCC,
			&t.HasWL, &t.HasML,
			&t.WNameID, &t.WUnitRate, &t.WCur, &t.WItemType, &t.WUnit,
			&t.MNameID, &t.MUnitRate, &t.MCur, &t.MItemType, &t.MMatType,
			&t.MDPT, &t.MDelivAmt, &t.MConsCoef, &t.MUnit,
		); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("boqRepo.InsertTemplateItems: item scan: %w", scanErr)
		}
		items = append(items, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("boqRepo.InsertTemplateItems: items rows: %w", err)
	}
	if len(items) == 0 {
		return nil, ErrTemplateEmpty
	}

	// 3. Client position (tender_id, manual_volume).
	var posTenderID string
	var manualVolume *float64
	err = tx.QueryRow(ctx,
		`SELECT tender_id::text, manual_volume FROM public.client_positions WHERE id = $1`,
		clientPositionID,
	).Scan(&posTenderID, &manualVolume)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrPositionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("boqRepo.InsertTemplateItems: position: %w", err)
	}

	// 0-F2 (category A): one revision bump for the whole template insert;
	// commercial recalc follows async (the service enqueues after commit).
	if _, err := MarkTenderFinancialInputsChangedTx(ctx, tx, posTenderID, "template_insert"); err != nil {
		return nil, fmt.Errorf("boqRepo.InsertTemplateItems: %w", err)
	}

	// 4. Tender currency rates — loaded ONCE for the whole operation (no N+1).
	// Passed verbatim to calc, which BLOCKS on a missing/non-positive foreign
	// rate (MissingFXRateError). There is deliberately no FX fallback to 1.0.
	var usd, eur, cny *float64
	if err := tx.QueryRow(ctx,
		`SELECT usd_rate, eur_rate, cny_rate FROM public.tenders WHERE id = $1`,
		posTenderID,
	).Scan(&usd, &eur, &cny); err != nil {
		return nil, fmt.Errorf("boqRepo.InsertTemplateItems: rates: %w", err)
	}
	rates := calc.CurrencyRates{USDRate: usd, EURRate: eur, CNYRate: cny}

	// ─── PHASE 1 — PLANNING (read-only; no INSERT/UPDATE happens below this
	// point until every row is validated and priced). Any error here returns
	// before the first mutation.

	// 4b. Validate every parent link against the ACTUAL insertion set and resolve
	// each row's effective parent. A row counts as a child only when its
	// parent_work_item_id will really point at an inserted WORK row; a declared
	// but unresolvable/non-work/self link is a blocking InvalidTemplateParentError
	// (never a silent standalone).
	idxByTID := make(map[string]int, len(items))
	for i, t := range items {
		idxByTID[t.ID] = i
	}
	parentIdx := make([]int, len(items))
	for i := range items {
		pIdx, perr := resolveTemplateParent(i, items, idxByTID)
		if perr != nil {
			return nil, fmt.Errorf("boqRepo.InsertTemplateItems: item #%d: %w", i+1, perr)
		}
		parentIdx[i] = pIdx
	}

	// 4c. Quantities. Инвариант: у ПРИВЯЗАННОГО материала количество выводится из
	// родительской РАБОТЫ (work.quantity × перевод × расход) — тот же инвариант,
	// что в position_recompute.go и в форме материала. Объём позиции
	// (manual_volume) применяется ТОЛЬКО к непривязанным материалам.
	// Два прохода: сначала работы (родитель всегда работа), поэтому порядок
	// элементов в шаблоне не важен.
	quantities := make([]float64, len(items))
	for i, t := range items {
		if t.Kind == "work" {
			quantities[i] = 1.0
		}
	}
	for i, t := range items {
		if t.Kind == "work" {
			continue
		}
		switch {
		case parentIdx[i] >= 0:
			quantities[i] = quantities[parentIdx[i]] * orOne(t.ConvCoeff) * orOne(t.MConsCoef)
		case t.ConvCoeff != nil && *t.ConvCoeff != 0:
			quantities[i] = *t.ConvCoeff * orOne(manualVolume)
		default:
			quantities[i] = 1.0
		}
	}

	// 5. Current max sort_number for the position.
	var maxSort int
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(sort_number), 0) FROM public.boq_items WHERE client_position_id = $1`,
		clientPositionID,
	).Scan(&maxSort); err != nil {
		return nil, fmt.Errorf("boqRepo.InsertTemplateItems: max sort: %w", err)
	}

	const insQ = `
		INSERT INTO public.boq_items
			(client_position_id, tender_id, sort_number, boq_item_type, material_type,
			 work_name_id, material_name_id, unit_code, quantity, base_quantity,
			 consumption_coefficient, conversion_coefficient, currency_type, unit_rate,
			 total_amount, detail_cost_category_id, description,
			 delivery_price_type, delivery_amount)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		RETURNING ` + boqScanCols

	newIDs := make([]string, len(items))
	worksCount, materialsCount := 0, 0

	// 5b. Plan + price EVERY row before touching the DB. A library-link problem,
	// an invalid parent or a missing FX rate aborts here — before the first
	// INSERT — so a bad template can never produce a partial write.
	plans := make([]templateRowPlan, len(items))
	for i, t := range items {
		if t.Kind == "work" {
			worksCount++
			if !t.HasWL {
				return nil, fmt.Errorf("%w (#%d)", ErrTemplateItemNoLib, i+1)
			}
		} else {
			materialsCount++
			if !t.HasML {
				return nil, fmt.Errorf("%w (#%d)", ErrTemplateItemNoLib, i+1)
			}
		}

		// Количество предрассчитано в шаге 4c (привязанный материал — от работы,
		// merge 6e8ea39): plan использует его вместо своей старой формулы.
		p, planErr := planTemplateRow(t, parentIdx[i], quantities[i], rates)
		if planErr != nil {
			// Fail-closed: nothing has been written yet. %w keeps MissingFXRateError
			// findable by errors.As all the way up to the handler.
			return nil, fmt.Errorf("boqRepo.InsertTemplateItems: item #%d: %w", i+1, planErr)
		}
		plans[i] = p
	}

	// ─── PHASE 2 — PERSISTENCE. Every row is validated and priced; from here on
	// only mechanical writes happen. Any failure still rolls the whole tx back.

	// 6. Insert every item with a temporary NULL parent_work_item_id.
	for i, t := range items {
		p := plans[i]

		dcc := tmplDCC
		if t.DCC != nil && *t.DCC != "" {
			dcc = *t.DCC
		}

		row := tx.QueryRow(ctx, insQ,
			clientPositionID, posTenderID, maxSort+i+1, p.ItemType, p.MatType,
			p.WorkNameID, p.MaterialNameID, p.UnitCode, p.Quantity, p.BaseQty,
			p.ConsCoef, p.ConvCoef, p.Currency, p.UnitRate,
			p.TotalAmount, dcc, t.Note,
			p.DPT, p.DeliveryAmount,
		)
		item, scanErr := scanBoqItemRow(row)
		if scanErr != nil {
			return nil, fmt.Errorf("boqRepo.InsertTemplateItems: insert scan: %w", scanErr)
		}
		newIDs[i] = item.ID

		newJSON, _ := boqRowJSON(item)
		if auditErr := insertAudit(ctx, tx, item.ID, "INSERT", changedBy, nil, nil, newJSON); auditErr != nil {
			return nil, fmt.Errorf("boqRepo.InsertTemplateItems: insert audit: %w", auditErr)
		}
	}

	// 7. Restore parent_work_item_id links from the VALIDATED plan. parentIdx was
	// resolved in phase 1 and is exactly what calc was told, so the persisted
	// parent state can never disagree with the priced state. Every non-negative
	// parentIdx is guaranteed to point at an inserted WORK row.
	for i := range items {
		pIdx := plans[i].ParentIdx
		if pIdx < 0 {
			continue
		}
		childID, parentID := newIDs[i], newIDs[pIdx]

		lockQ := "SELECT " + boqScanCols + " FROM public.boq_items WHERE id = $1 FOR UPDATE"
		oldItem, lockErr := scanBoqItemRow(tx.QueryRow(ctx, lockQ, childID))
		if lockErr != nil {
			return nil, fmt.Errorf("boqRepo.InsertTemplateItems: parent lock: %w", lockErr)
		}
		updQ := "UPDATE public.boq_items SET parent_work_item_id = $1, updated_at = NOW() WHERE id = $2 RETURNING " + boqScanCols
		newItem, updErr := scanBoqItemRow(tx.QueryRow(ctx, updQ, parentID, childID))
		if updErr != nil {
			return nil, fmt.Errorf("boqRepo.InsertTemplateItems: parent update: %w", updErr)
		}
		oldJSON, _ := boqRowJSON(oldItem)
		newJSON, _ := boqRowJSON(newItem)
		if auditErr := insertAudit(ctx, tx, childID, "UPDATE", changedBy,
			changedFields(oldItem, newItem), oldJSON, newJSON); auditErr != nil {
			return nil, fmt.Errorf("boqRepo.InsertTemplateItems: parent audit: %w", auditErr)
		}
	}

	// 8. Recompute position totals from the now-complete item set.
	if _, err := tx.Exec(ctx, `
		UPDATE public.client_positions cp
		SET total_material = COALESCE(s.tm, 0),
		    total_works    = COALESCE(s.tw, 0),
		    updated_at     = NOW()
		FROM (
			SELECT
				SUM(total_amount) FILTER (WHERE boq_item_type::text IN ('мат','суб-мат','мат-комп.')) AS tm,
				SUM(total_amount) FILTER (WHERE boq_item_type::text IN ('раб','суб-раб','раб-комп.')) AS tw
			FROM public.boq_items
			WHERE client_position_id = $1
		) s
		WHERE cp.id = $1
	`, clientPositionID); err != nil {
		return nil, fmt.Errorf("boqRepo.InsertTemplateItems: position totals: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("boqRepo.InsertTemplateItems: commit: %w", err)
	}

	return &TemplateInsertResult{
		WorksCount:     worksCount,
		MaterialsCount: materialsCount,
		TotalInserted:  len(items),
		TenderID:       posTenderID,
	}, nil
}

func strOr(p *string, def string) string {
	if p == nil || *p == "" {
		return def
	}
	return *p
}

func strOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
