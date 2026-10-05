package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/su10/hubtender/backend/internal/pricing"
)

type PricingRepo struct{ pool *pgxpool.Pool }

func NewPricingRepo(pool *pgxpool.Pool) *PricingRepo { return &PricingRepo{pool: pool} }

func (r *PricingRepo) ListPricingTenders(ctx context.Context, search string, archived *bool, limit, offset int) ([]pricing.TenderSummary, int, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text,tender_number,title,client_name,version,is_archived,
		       housing_class::text,construction_scope::text,submission_deadline,
		       COALESCE(updated_at,created_at,now()),count(*) OVER()
		FROM public.tenders
		WHERE ($1='' OR title ILIKE '%'||$1||'%' OR tender_number ILIKE '%'||$1||'%' OR client_name ILIKE '%'||$1||'%')
		  AND ($2::boolean IS NULL OR is_archived=$2)
		ORDER BY updated_at DESC NULLS LAST,id
		LIMIT $3 OFFSET $4`, strings.TrimSpace(search), archived, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("pricingRepo.ListPricingTenders: %w", err)
	}
	defer rows.Close()
	out := []pricing.TenderSummary{}
	total := 0
	for rows.Next() {
		var row pricing.TenderSummary
		if err := rows.Scan(&row.ID, &row.TenderNumber, &row.Title, &row.ClientName, &row.Version,
			&row.IsArchived, &row.HousingClass, &row.ConstructionScope, &row.SubmissionDeadline,
			&row.UpdatedAt, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, row)
	}
	return out, total, rows.Err()
}

func (r *PricingRepo) GetPricingState(ctx context.Context, tenderID string, limit, offset int) (*pricing.PricingState, error) {
	var state pricing.PricingState
	err := r.pool.QueryRow(ctx, `
		SELECT id::text,tender_number,title,client_name,version,is_archived,
		       housing_class::text,construction_scope::text,submission_deadline,
		       COALESCE(updated_at,created_at,now()),COALESCE(financial_input_revision,0)
		FROM public.tenders WHERE id=$1`, tenderID).Scan(
		&state.Tender.ID, &state.Tender.TenderNumber, &state.Tender.Title, &state.Tender.ClientName,
		&state.Tender.Version, &state.Tender.IsArchived, &state.Tender.HousingClass,
		&state.Tender.ConstructionScope, &state.Tender.SubmissionDeadline, &state.Tender.UpdatedAt,
		&state.FinancialInputRevision,
	)
	if err != nil {
		return nil, err
	}
	if err := r.pool.QueryRow(ctx, `
		SELECT count(DISTINCT cp.id),count(bi.id),
		       count(bi.id) FILTER (WHERE bi.unit_rate IS NOT NULL AND bi.unit_rate>0),
		       count(bi.id) FILTER (WHERE bi.unit_rate IS NULL OR bi.unit_rate<=0),
		       COALESCE(sum(bi.total_amount),0)
		FROM public.client_positions cp
		LEFT JOIN public.boq_items bi ON bi.client_position_id=cp.id
		WHERE cp.tender_id=$1`, tenderID).Scan(
		&state.PositionCount, &state.ItemCount, &state.PricedItemCount,
		&state.MissingRateCount, &state.DirectTotal); err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT cp.id::text,cp.position_number,cp.work_name,cp.unit_code,cp.volume,cp.manual_volume,
		       count(bi.id),count(bi.id) FILTER (WHERE bi.unit_rate IS NOT NULL AND bi.unit_rate>0),
		       count(bi.id) FILTER (WHERE bi.unit_rate IS NULL OR bi.unit_rate<=0),
		       count(bi.id) FILTER (WHERE bi.detail_cost_category_id IS NULL),
		       COALESCE(sum(bi.total_amount),0)
		FROM public.client_positions cp
		LEFT JOIN public.boq_items bi ON bi.client_position_id=cp.id
		WHERE cp.tender_id=$1
		GROUP BY cp.id
		ORDER BY cp.position_number,cp.id
		LIMIT $2 OFFSET $3`, tenderID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p pricing.PositionPricingState
		if err := rows.Scan(&p.ID, &p.PositionNumber, &p.WorkName, &p.UnitCode, &p.Volume, &p.ManualVolume,
			&p.ItemsCount, &p.PricedItems, &p.MissingRates, &p.MissingCategories, &p.DirectTotal); err != nil {
			return nil, err
		}
		state.Positions = append(state.Positions, p)
	}
	state.Pagination = pricing.Page{Limit: limit, Offset: offset, TotalCount: state.PositionCount, HasMore: offset+len(state.Positions) < state.PositionCount}
	if state.Pagination.HasMore {
		next := offset + len(state.Positions)
		state.Pagination.NextOffset = &next
	}
	return &state, rows.Err()
}

func (r *PricingRepo) RawArchiveCandidates(ctx context.Context, in pricing.ArchiveSearchInput, poolLimit int) ([]pricing.ArchiveCandidate, error) {
	rows, err := r.pool.Query(ctx, `
		WITH matched_work_names AS MATERIALIZED (
			SELECT id,similarity(lower(name),lower($1)) AS sim
			FROM public.work_names
			WHERE lower(name) % lower($1) OR lower(name) LIKE '%'||lower($1)||'%'
			ORDER BY sim DESC LIMIT 200
		), matched_material_names AS MATERIALIZED (
			SELECT id,similarity(lower(name),lower($1)) AS sim
			FROM public.material_names
			WHERE lower(name) % lower($1) OR lower(name) LIKE '%'||lower($1)||'%'
			ORDER BY sim DESC LIMIT 200
		), matched_positions AS MATERIALIZED (
			SELECT id,similarity(lower(work_name),lower($1)) AS sim
			FROM public.client_positions
			WHERE lower(work_name) % lower($1) OR lower(work_name) LIKE '%'||lower($1)||'%'
			ORDER BY sim DESC LIMIT 200
		), raw_candidate_ids AS (
			SELECT bi.id,m.sim FROM public.boq_items bi JOIN matched_work_names m ON m.id=bi.work_name_id
			UNION ALL
			SELECT bi.id,m.sim FROM public.boq_items bi JOIN matched_material_names m ON m.id=bi.material_name_id
			UNION ALL
			SELECT bi.id,m.sim FROM public.boq_items bi JOIN matched_positions m ON m.id=bi.client_position_id
		), candidate_ids AS MATERIALIZED (
			SELECT id,max(sim) AS sim FROM raw_candidate_ids GROUP BY id
			ORDER BY max(sim) DESC LIMIT LEAST($5::int*10,5000)
		), items AS (
			SELECT bi.*,COALESCE(wn.name,mn.name,bi.description,'') AS item_name,
			       COALESCE(bi.unit_code,wn.unit,mn.unit) AS effective_unit,
			       CASE WHEN bi.work_name_id IS NOT NULL THEN 'work' ELSE 'material' END AS item_kind
			FROM public.boq_items bi
			LEFT JOIN public.work_names wn ON wn.id=bi.work_name_id
			LEFT JOIN public.material_names mn ON mn.id=bi.material_name_id
		)
		SELECT i.id::text,t.id::text,t.title,t.tender_number,t.version,t.is_archived,
		       COALESCE(i.quote_price_date::timestamptz,t.updated_at,t.created_at,now()),t.housing_class::text,t.construction_scope::text,
		       cp.id::text,cp.work_name,i.item_name,i.item_kind,i.boq_item_type::text,
		       i.effective_unit,i.quantity,i.unit_rate,i.currency_type::text,
		       i.delivery_price_type::text,i.delivery_amount,i.consumption_coefficient,
		       i.detail_cost_category_id::text,dcc.name,i.material_name_id::text,i.work_name_id::text,
		       i.material_type::text,i.description,i.quote_link,to_char(i.quote_price_date,'YYYY-MM-DD'),to_char(i.quote_valid_until,'YYYY-MM-DD'),
		       CASE i.currency_type::text
		         WHEN 'USD' THEN i.unit_rate*t.usd_rate
		         WHEN 'EUR' THEN i.unit_rate*t.eur_rate
		         WHEN 'CNY' THEN i.unit_rate*t.cny_rate
		         ELSE i.unit_rate END AS historical_rub,
		       greatest(ci.sim,similarity(lower(i.item_name),lower($1)),similarity(lower(cp.work_name),lower($1))) AS sim
		FROM items i
		JOIN candidate_ids ci ON ci.id=i.id
		JOIN public.tenders t ON t.id=i.tender_id
		JOIN public.client_positions cp ON cp.id=i.client_position_id
		LEFT JOIN public.detail_cost_categories dcc ON dcc.id=i.detail_cost_category_id
		WHERE ($2 OR t.is_archived=true)
		  AND ($3='' OR i.item_kind=$3)
		  AND ($4='' OR lower(COALESCE(i.effective_unit,''))=lower($4))
		ORDER BY sim DESC,COALESCE(t.updated_at,t.created_at) DESC
		LIMIT $5`, in.Query, in.IncludeActive, in.Kind, in.UnitCode, poolLimit)
	if err != nil {
		return nil, fmt.Errorf("pricingRepo.RawArchiveCandidates: %w", err)
	}
	defer rows.Close()
	out := []pricing.ArchiveCandidate{}
	for rows.Next() {
		var c pricing.ArchiveCandidate
		if err := rows.Scan(
			&c.ItemID, &c.TenderID, &c.TenderTitle, &c.TenderNumber, &c.TenderVersion, &c.TenderArchived,
			&c.TenderDate, &c.HousingClass, &c.ConstructionScope, &c.PositionID, &c.PositionName,
			&c.ItemName, &c.ItemKind, &c.BoqItemType, &c.UnitCode, &c.Quantity, &c.UnitRate,
			&c.CurrencyType, &c.DeliveryPriceType, &c.DeliveryAmount, &c.ConsumptionCoefficient,
			&c.DetailCostCategoryID, &c.DetailCostCategoryName, &c.MaterialNameID, &c.WorkNameID,
			&c.MaterialType, &c.Description, &c.QuoteLink, &c.QuotePriceDate, &c.QuoteValidUntil, &c.HistoricalRUBUnitRate, &c.RawSimilarity,
		); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *PricingRepo) SearchLibrary(ctx context.Context, query, kind, unit string, limit, offset int) ([]pricing.LibraryCandidate, int, error) {
	rows, err := r.pool.Query(ctx, `
		WITH library AS (
			SELECT wl.id::text,'work'::text AS kind,wn.name,wn.id::text AS name_id,wn.unit,
			       wl.item_type::text,NULL::text AS material_type,wl.unit_rate,wl.currency_type::text,
			       NULL::text AS delivery_price_type,NULL::numeric AS delivery_amount,NULL::numeric AS consumption_coefficient,
			       similarity(lower(wn.name),lower($1)) AS sim
			FROM public.works_library wl JOIN public.work_names wn ON wn.id=wl.work_name_id
			UNION ALL
			SELECT ml.id::text,'material',mn.name,mn.id::text,mn.unit,ml.item_type::text,ml.material_type::text,
			       ml.unit_rate,ml.currency_type::text,ml.delivery_price_type::text,ml.delivery_amount,
			       ml.consumption_coefficient,similarity(lower(mn.name),lower($1))
			FROM public.materials_library ml JOIN public.material_names mn ON mn.id=ml.material_name_id
		), filtered AS (
			SELECT *,count(*) OVER() AS total FROM library
			WHERE ($2='' OR kind=$2) AND ($3='' OR lower(unit)=lower($3))
			  AND (lower(name) % lower($1) OR name ILIKE '%'||$1||'%')
		)
		SELECT * FROM filtered ORDER BY sim DESC,name LIMIT $4 OFFSET $5`, query, kind, unit, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("pricingRepo.SearchLibrary: %w", err)
	}
	defer rows.Close()
	out := []pricing.LibraryCandidate{}
	total := 0
	for rows.Next() {
		var c pricing.LibraryCandidate
		if err := rows.Scan(&c.ID, &c.Kind, &c.Name, &c.NameID, &c.UnitCode, &c.ItemType, &c.MaterialType,
			&c.UnitRate, &c.CurrencyType, &c.DeliveryPriceType, &c.DeliveryAmount,
			&c.ConsumptionCoefficient, &c.Confidence, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

func (r *PricingRepo) ListTemplatesForPricing(ctx context.Context, search string, limit, offset int) ([]pricing.TemplateSummary, int, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT t.id::text,t.name,t.detail_cost_category_id::text,t.folder_id::text,
		       count(ti.id),COALESCE(t.updated_at,t.created_at,now()),count(*) OVER()
		FROM public.templates t LEFT JOIN public.template_items ti ON ti.template_id=t.id
		WHERE ($1='' OR t.name ILIKE '%'||$1||'%')
		GROUP BY t.id
		ORDER BY t.name LIMIT $2 OFFSET $3`, search, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []pricing.TemplateSummary{}
	total := 0
	for rows.Next() {
		var t pricing.TemplateSummary
		if err := rows.Scan(&t.ID, &t.Name, &t.DetailCostCategoryID, &t.FolderID, &t.ItemsCount, &t.UpdatedAt, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

func (r *PricingRepo) GetTemplateForPricing(ctx context.Context, id string) (*pricing.TemplateDetail, error) {
	var out pricing.TemplateDetail
	err := r.pool.QueryRow(ctx, `
		SELECT t.id::text,t.name,t.detail_cost_category_id::text,t.folder_id::text,
		       count(ti.id),COALESCE(t.updated_at,t.created_at,now())
		FROM public.templates t LEFT JOIN public.template_items ti ON ti.template_id=t.id
		WHERE t.id=$1 GROUP BY t.id`, id).Scan(&out.ID, &out.Name, &out.DetailCostCategoryID,
		&out.FolderID, &out.ItemsCount, &out.UpdatedAt)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ti.id::text,ti.kind,ti.position,ti.parent_work_item_id::text,
		       COALESCE(wl.item_type::text,ml.item_type::text),COALESCE(wn.name,mn.name),
		       COALESCE(wn.id, mn.id)::text,COALESCE(wn.unit,mn.unit),
		       COALESCE(wl.unit_rate,ml.unit_rate),COALESCE(wl.currency_type::text,ml.currency_type::text),
		       ml.material_type::text,ml.delivery_price_type::text,ml.delivery_amount,
		       ml.consumption_coefficient,ti.conversation_coeff,ti.detail_cost_category_id::text,ti.note
		FROM public.template_items ti
		LEFT JOIN public.works_library wl ON wl.id=ti.work_library_id
		LEFT JOIN public.work_names wn ON wn.id=wl.work_name_id
		LEFT JOIN public.materials_library ml ON ml.id=ti.material_library_id
		LEFT JOIN public.material_names mn ON mn.id=ml.material_name_id
		WHERE ti.template_id=$1 ORDER BY ti.position,ti.id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item pricing.TemplateItem
		if err := rows.Scan(&item.ID, &item.Kind, &item.Position, &item.ParentTemplateItemID,
			&item.ItemType, &item.Name, &item.NameID, &item.UnitCode, &item.UnitRate,
			&item.CurrencyType, &item.MaterialType, &item.DeliveryPriceType, &item.DeliveryAmount,
			&item.ConsumptionCoefficient, &item.ConversionCoefficient, &item.DetailCostCategoryID,
			&item.Note); err != nil {
			return nil, err
		}
		out.Items = append(out.Items, item)
	}
	return &out, rows.Err()
}

func (r *PricingRepo) GetTenderRevisionAndRates(ctx context.Context, id string) (time.Time, *float64, *float64, *float64, error) {
	var rev time.Time
	var usd, eur, cny *float64
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(updated_at,created_at,now()),usd_rate,eur_rate,cny_rate FROM public.tenders WHERE id=$1`, id).Scan(&rev, &usd, &eur, &cny)
	return rev, usd, eur, cny, err
}

func (r *PricingRepo) GetArchiveItem(ctx context.Context, id string) (*pricing.ArchiveCandidate, error) {
	rows, err := r.RawArchiveCandidatesByID(ctx, id)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &rows[0], nil
}

func (r *PricingRepo) RawArchiveCandidatesByID(ctx context.Context, id string) ([]pricing.ArchiveCandidate, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT bi.id::text,t.id::text,t.title,t.tender_number,t.version,t.is_archived,
		       COALESCE(bi.quote_price_date::timestamptz,t.updated_at,t.created_at,now()),t.housing_class::text,t.construction_scope::text,
		       cp.id::text,cp.work_name,COALESCE(wn.name,mn.name,bi.description,''),
		       CASE WHEN bi.work_name_id IS NOT NULL THEN 'work' ELSE 'material' END,
		       bi.boq_item_type::text,COALESCE(bi.unit_code,wn.unit,mn.unit),bi.quantity,bi.unit_rate,
		       bi.currency_type::text,bi.delivery_price_type::text,bi.delivery_amount,
		       bi.consumption_coefficient,bi.detail_cost_category_id::text,dcc.name,
		       bi.material_name_id::text,bi.work_name_id::text,bi.material_type::text,
		       bi.description,bi.quote_link,to_char(bi.quote_price_date,'YYYY-MM-DD'),to_char(bi.quote_valid_until,'YYYY-MM-DD'),
		       CASE bi.currency_type::text WHEN 'USD' THEN bi.unit_rate*t.usd_rate WHEN 'EUR' THEN bi.unit_rate*t.eur_rate WHEN 'CNY' THEN bi.unit_rate*t.cny_rate ELSE bi.unit_rate END,1::float8
		FROM public.boq_items bi JOIN public.tenders t ON t.id=bi.tender_id
		JOIN public.client_positions cp ON cp.id=bi.client_position_id
		LEFT JOIN public.work_names wn ON wn.id=bi.work_name_id
		LEFT JOIN public.material_names mn ON mn.id=bi.material_name_id
		LEFT JOIN public.detail_cost_categories dcc ON dcc.id=bi.detail_cost_category_id
		WHERE bi.id=$1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []pricing.ArchiveCandidate{}
	for rows.Next() {
		var c pricing.ArchiveCandidate
		if err := rows.Scan(&c.ItemID, &c.TenderID, &c.TenderTitle, &c.TenderNumber, &c.TenderVersion,
			&c.TenderArchived, &c.TenderDate, &c.HousingClass, &c.ConstructionScope, &c.PositionID,
			&c.PositionName, &c.ItemName, &c.ItemKind, &c.BoqItemType, &c.UnitCode, &c.Quantity,
			&c.UnitRate, &c.CurrencyType, &c.DeliveryPriceType, &c.DeliveryAmount,
			&c.ConsumptionCoefficient, &c.DetailCostCategoryID, &c.DetailCostCategoryName,
			&c.MaterialNameID, &c.WorkNameID, &c.MaterialType, &c.Description, &c.QuoteLink, &c.QuotePriceDate, &c.QuoteValidUntil,
			&c.HistoricalRUBUnitRate, &c.RawSimilarity); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *PricingRepo) GetLibraryItem(ctx context.Context, id, kind string) (*pricing.LibraryCandidate, error) {
	var c pricing.LibraryCandidate
	if kind == "work" {
		err := r.pool.QueryRow(ctx, `SELECT wl.id::text,'work',wn.name,wn.id::text,wn.unit,wl.item_type::text,NULL::text,wl.unit_rate,wl.currency_type::text,NULL::text,NULL::numeric,NULL::numeric,1::float8
			FROM public.works_library wl JOIN public.work_names wn ON wn.id=wl.work_name_id WHERE wl.id=$1`, id).Scan(
			&c.ID, &c.Kind, &c.Name, &c.NameID, &c.UnitCode, &c.ItemType, &c.MaterialType,
			&c.UnitRate, &c.CurrencyType, &c.DeliveryPriceType, &c.DeliveryAmount,
			&c.ConsumptionCoefficient, &c.Confidence)
		return &c, err
	}
	err := r.pool.QueryRow(ctx, `SELECT ml.id::text,'material',mn.name,mn.id::text,mn.unit,ml.item_type::text,ml.material_type::text,ml.unit_rate,ml.currency_type::text,ml.delivery_price_type::text,ml.delivery_amount,ml.consumption_coefficient,1::float8
		FROM public.materials_library ml JOIN public.material_names mn ON mn.id=ml.material_name_id WHERE ml.id=$1`, id).Scan(
		&c.ID, &c.Kind, &c.Name, &c.NameID, &c.UnitCode, &c.ItemType, &c.MaterialType,
		&c.UnitRate, &c.CurrencyType, &c.DeliveryPriceType, &c.DeliveryAmount,
		&c.ConsumptionCoefficient, &c.Confidence)
	return &c, err
}

func (r *PricingRepo) GetPricingQA(ctx context.Context, tenderID string) (*pricing.QAReport, error) {
	report := &pricing.QAReport{TenderID: tenderID, GeneratedAt: time.Now().UTC()}
	err := r.pool.QueryRow(ctx, `
		WITH leaves AS (
		 SELECT cp.id FROM public.client_positions cp
		 WHERE cp.tender_id=$1 AND NOT EXISTS (
		   SELECT 1 FROM public.client_positions child WHERE child.parent_position_id=cp.id
		 )
		), item_stats AS (
		 SELECT count(*) item_count,
		        count(*) FILTER (WHERE unit_rate IS NULL OR unit_rate<=0) missing_rate,
		        count(*) FILTER (WHERE quantity IS NULL) missing_qty,
		        count(*) FILTER (WHERE detail_cost_category_id IS NULL) missing_cat,
		        COALESCE(sum(total_amount),0) direct_total
		 FROM public.boq_items WHERE tender_id=$1
		)
		SELECT (SELECT count(*) FROM public.client_positions WHERE tender_id=$1),
		       (SELECT count(*) FROM leaves),
		       (SELECT count(*) FROM leaves l WHERE NOT EXISTS (
		          SELECT 1 FROM public.boq_items bi WHERE bi.client_position_id=l.id AND bi.unit_rate IS NOT NULL AND bi.unit_rate>0)),
		       item_count,missing_rate,missing_qty,missing_cat,direct_total
		FROM item_stats`, tenderID).Scan(&report.PositionCount, &report.LeafPositionCount,
		&report.UnpricedPositionCount, &report.ItemCount, &report.MissingRateCount,
		&report.MissingQuantityCount, &report.MissingCategoryCount, &report.DirectTotal)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT bi.currency_type::text
		FROM public.boq_items bi JOIN public.tenders t ON t.id=bi.tender_id
		WHERE bi.tender_id=$1 AND (
		 (bi.currency_type::text='USD' AND COALESCE(t.usd_rate,0)<=0) OR
		 (bi.currency_type::text='EUR' AND COALESCE(t.eur_rate,0)<=0) OR
		 (bi.currency_type::text='CNY' AND COALESCE(t.cny_rate,0)<=0))`, tenderID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			rows.Close()
			return nil, err
		}
		report.MissingFXCurrencies = append(report.MissingFXCurrencies, c)
	}
	rows.Close()
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE COALESCE(pdo.match_level,bps.direct_match_level)='review'),
		       count(*) FILTER (WHERE (bps.source_date IS NOT NULL AND bps.source_date < now()-interval '180 days'))
		FROM public.boq_item_pricing_sources bps
		JOIN public.boq_items bi ON bi.id=bps.boq_item_id
		LEFT JOIN public.pricing_draft_operations pdo ON pdo.id=bps.draft_operation_id
		WHERE bi.tender_id=$1`, tenderID).Scan(&report.WeakSourceCount, &report.StaleSourceCount); err != nil {
		return nil, err
	}
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM public.boq_items bi WHERE bi.tender_id=$1
		AND NOT EXISTS (SELECT 1 FROM public.boq_item_pricing_sources s WHERE s.boq_item_id=bi.id)`, tenderID).Scan(&report.ItemsWithoutProvenance); err != nil {
		return nil, err
	}
	if report.UnpricedPositionCount > 0 {
		report.BlockingIssues = append(report.BlockingIssues, "Есть конечные позиции без расценок")
	}
	if report.MissingRateCount > 0 {
		report.BlockingIssues = append(report.BlockingIssues, "Есть BOQ-строки без ставки")
	}
	if report.MissingQuantityCount > 0 {
		report.BlockingIssues = append(report.BlockingIssues, "Есть BOQ-строки без количества")
	}
	if len(report.MissingFXCurrencies) > 0 {
		report.BlockingIssues = append(report.BlockingIssues, "Для используемых валют отсутствует курс")
	}
	report.ReadyForReview = len(report.BlockingIssues) == 0
	return report, nil
}
