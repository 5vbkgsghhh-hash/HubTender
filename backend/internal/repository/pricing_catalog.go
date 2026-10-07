package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/su10/hubtender/backend/internal/pricing"
)

var (
	ErrCatalogKeyReused = errors.New("CATALOG_REQUEST_KEY_REUSED: use a new key for different confirmed inputs")
	ErrCatalogAmbiguous = errors.New("CATALOG_DUPLICATE_AMBIGUOUS: several exact records exist; search and select their ID rather than creating another")
	ErrCatalogUnit      = errors.New("CATALOG_UNIT_INVALID: choose an existing active unit or create one explicitly")
	ErrCatalogName      = errors.New("CATALOG_NAME_INVALID: choose a nomenclature ID of the requested kind")
	ErrCatalogBusy      = errors.New("CATALOG_BUSY: a catalog record is being edited; reload and retry the uncommitted command")
	ErrCatalogNameStale = errors.New("CATALOG_NAME_STALE: nomenclature name/unit changed; read its current version before creating a price card")
)

const catalogNormalizedName = "lower(regexp_replace(btrim(name), '[[:space:]]+', ' ', 'g'))"

func catalogNameTable(kind string) (string, error) {
	switch kind {
	case "work":
		return tableWorks, nil
	case "material":
		return tableMaterials, nil
	}
	return "", ErrCatalogName
}

func (r *PricingRepo) SearchCatalogNames(ctx context.Context, kind, query, unit string, limit, offset int) ([]pricing.CatalogName, int, error) {
	table, err := catalogNameTable(kind)
	if err != nil {
		return nil, 0, err
	}
	var total int
	where := " WHERE ($1='' OR name ILIKE '%'||$1||'%') AND ($2='' OR unit=$2)"
	if err := r.pool.QueryRow(ctx, "SELECT count(*) FROM "+table+where, query, unit).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, "SELECT id::text,name,unit FROM "+table+where+" ORDER BY name,id LIMIT $3 OFFSET $4", query, unit, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []pricing.CatalogName{}
	for rows.Next() {
		c := pricing.CatalogName{Kind: kind}
		if err := rows.Scan(&c.ID, &c.Name, &c.UnitCode); err != nil {
			return nil, 0, err
		}
		c.Version = pricing.CatalogNameVersion(c)
		out = append(out, c)
	}
	return out, total, rows.Err()
}

func (r *PricingRepo) GetCatalogName(ctx context.Context, kind, id string) (*pricing.CatalogName, error) {
	table, err := catalogNameTable(kind)
	if err != nil {
		return nil, err
	}
	n := pricing.CatalogName{Kind: kind, ID: id}
	if err := r.pool.QueryRow(ctx, "SELECT name,unit FROM "+table+" WHERE id=$1", id).Scan(&n.Name, &n.UnitCode); err != nil {
		return nil, err
	}
	n.Version = pricing.CatalogNameVersion(n)
	return &n, nil
}

func (r *PricingRepo) ListCatalogUnits(ctx context.Context, query string, includeInactive bool, limit, offset int) ([]pricing.CatalogUnit, int, error) {
	where := ` WHERE ($1='' OR code ILIKE '%'||$1||'%' OR name ILIKE '%'||$1||'%') AND ($2 OR is_active=true)`
	var total int
	if err := r.pool.QueryRow(ctx, "SELECT count(*) FROM public.units"+where, query, includeInactive).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx, "SELECT code,name,COALESCE(is_active,false) FROM public.units"+where+" ORDER BY sort_order,code LIMIT $3 OFFSET $4", query, includeInactive, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []pricing.CatalogUnit{}
	for rows.Next() {
		var u pricing.CatalogUnit
		if err := rows.Scan(&u.Code, &u.Name, &u.Active); err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

func (r *PricingRepo) GetCatalogCreationReceipt(ctx context.Context, actor, key string) (*pricing.CatalogCreationResult, string, error) {
	var hash string
	var raw []byte
	err := r.pool.QueryRow(ctx, `SELECT request_hash,result FROM public.mcp_catalog_creation_requests WHERE actor_id=$1 AND request_key=$2`, actor, key).Scan(&hash, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var out pricing.CatalogCreationResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, "", err
	}
	return &out, hash, nil
}

func catalogActiveUnitTx(ctx context.Context, tx pgx.Tx, code string) (string, error) {
	var canonical string
	var active bool
	err := tx.QueryRow(ctx, `SELECT code,COALESCE(is_active,false) FROM public.units WHERE code=$1 FOR SHARE NOWAIT`, code).Scan(&canonical, &active)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !active {
		return "", ErrCatalogUnit
	}
	return canonical, err
}

func (r *PricingRepo) CreateCatalogEntity(ctx context.Context, actor, key, hash string, encodedInput []byte, in pricing.CatalogCreationInput) (out *pricing.CatalogCreationResult, err error) {
	defer func() {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "55P03" {
			err = ErrCatalogBusy
		}
	}()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var inserted int
	err = tx.QueryRow(ctx, `INSERT INTO public.mcp_catalog_creation_requests(actor_id,request_key,request_hash,input,result) VALUES($1,$2,$3,$4::jsonb,'{}'::jsonb) ON CONFLICT(actor_id,request_key) DO NOTHING RETURNING 1`, actor, key, hash, encodedInput).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		var previousHash string
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT request_hash,result FROM public.mcp_catalog_creation_requests WHERE actor_id=$1 AND request_key=$2`, actor, key).Scan(&previousHash, &raw); err != nil {
			return nil, err
		}
		if previousHash != hash {
			return nil, ErrCatalogKeyReused
		}
		var prior pricing.CatalogCreationResult
		if err := json.Unmarshal(raw, &prior); err != nil {
			return nil, err
		}
		prior.Replayed = true
		return &prior, nil
	}
	if err != nil {
		return nil, err
	}
	out = &pricing.CatalogCreationResult{RequestKey: key, EntityType: in.EntityType, Kind: in.Kind, Name: in.Name, UnitCode: in.UnitCode}
	switch in.EntityType {
	case "unit":
		err = createCatalogUnitTx(ctx, tx, in, out)
	case "nomenclature":
		err = createCatalogNameTx(ctx, tx, in, out)
	case "library":
		err = createCatalogLibraryTx(ctx, tx, in, out)
	default:
		err = fmt.Errorf("unsupported catalog entity type")
	}
	if err != nil {
		return nil, err
	}
	if in.EntityType == "nomenclature" {
		n := pricing.CatalogName{ID: out.EntityID, Kind: out.Kind, Name: out.Name, UnitCode: out.UnitCode}
		n.Version = pricing.CatalogNameVersion(n)
		out.NomenclatureItem = &n
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE public.mcp_catalog_creation_requests SET result=$3::jsonb WHERE actor_id=$1 AND request_key=$2`, actor, key, raw); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func catalogIdentityLock(ctx context.Context, tx pgx.Tx, key string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "mcp-catalog:"+key)
	return err
}

func createCatalogUnitTx(ctx context.Context, tx pgx.Tx, in pricing.CatalogCreationInput, out *pricing.CatalogCreationResult) error {
	if err := catalogIdentityLock(ctx, tx, "unit:"+in.UnitCode); err != nil {
		return err
	}
	var code, name string
	var active bool
	err := tx.QueryRow(ctx, `SELECT code,name,COALESCE(is_active,false) FROM public.units WHERE code=$1 FOR SHARE NOWAIT`, in.UnitCode).Scan(&code, &name, &active)
	if err == nil {
		if !active {
			return ErrCatalogUnit
		}
		out.EntityID, out.Name, out.UnitCode = code, name, code
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO public.units(code,name,is_active) VALUES($1,$2,true)`, in.UnitCode, in.Name); err != nil {
		return err
	}
	out.EntityID, out.Created = in.UnitCode, true
	return nil
}

func createCatalogNameTx(ctx context.Context, tx pgx.Tx, in pricing.CatalogCreationInput, out *pricing.CatalogCreationResult) error {
	table, err := catalogNameTable(in.Kind)
	if err != nil {
		return err
	}
	if _, err := catalogActiveUnitTx(ctx, tx, in.UnitCode); err != nil {
		return err
	}
	var normalized string
	if err := tx.QueryRow(ctx, `SELECT lower(regexp_replace(btrim($1::text),'[[:space:]]+',' ','g'))`, in.Name).Scan(&normalized); err != nil {
		return err
	}
	if err := catalogIdentityLock(ctx, tx, "name:"+in.Kind+":"+in.UnitCode+":"+normalized); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, "SELECT id::text,name FROM "+table+" WHERE "+catalogNormalizedName+"=$1 AND unit=$2 ORDER BY id LIMIT 2 FOR SHARE NOWAIT", normalized, in.UnitCode)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		count++
		if err := rows.Scan(&out.EntityID, &out.Name); err != nil {
			rows.Close()
			return err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if count > 1 {
		return ErrCatalogAmbiguous
	}
	if count == 1 {
		return nil
	}
	if err := tx.QueryRow(ctx, "INSERT INTO "+table+"(name,unit) VALUES($1,$2) RETURNING id::text", in.Name, in.UnitCode).Scan(&out.EntityID); err != nil {
		return err
	}
	out.Created = true
	return nil
}

func createCatalogLibraryTx(ctx context.Context, tx pgx.Tx, in pricing.CatalogCreationInput, out *pricing.CatalogCreationResult) error {
	table, err := catalogNameTable(in.Kind)
	if err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, "SELECT name,unit FROM "+table+" WHERE id=$1 FOR SHARE NOWAIT", in.NameID).Scan(&out.Name, &out.UnitCode); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrCatalogName
		}
		return err
	}
	n := pricing.CatalogName{ID: in.NameID, Kind: in.Kind, Name: out.Name, UnitCode: out.UnitCode}
	n.Version = pricing.CatalogNameVersion(n)
	if n.Version != in.ExpectedNameVersion {
		return ErrCatalogNameStale
	}
	out.NomenclatureItem = &n
	if _, err := catalogActiveUnitTx(ctx, tx, out.UnitCode); err != nil {
		return err
	}
	// One lock per nomenclature serializes exact library duplicates created by
	// different MCP requests. Different confirmed recipes/rates remain variants.
	if err := catalogIdentityLock(ctx, tx, "library:"+in.Kind+":"+in.NameID); err != nil {
		return err
	}
	query := `SELECT id::text FROM public.works_library WHERE work_name_id=$1 AND item_type::text=$2 AND unit_rate=$3 AND currency_type::text=$4 ORDER BY id LIMIT 2 FOR SHARE NOWAIT`
	args := []any{in.NameID, in.ItemType, in.UnitRate, in.Currency}
	if in.Kind == "material" {
		query = `SELECT id::text FROM public.materials_library WHERE material_name_id=$1 AND item_type::text=$2 AND unit_rate=$3 AND currency_type::text=$4 AND material_type::text=$5 AND COALESCE(consumption_coefficient,1)=$6 AND delivery_price_type::text=$7 AND COALESCE(delivery_amount,0)=$8 ORDER BY id LIMIT 2 FOR SHARE NOWAIT`
		args = append(args, in.MaterialType, *in.ConsumptionCoefficient, in.DeliveryPriceType, *in.DeliveryAmount)
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	count := 0
	for rows.Next() {
		count++
		if err := rows.Scan(&out.EntityID); err != nil {
			rows.Close()
			return err
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if count > 1 {
		return ErrCatalogAmbiguous
	}
	if count == 0 {
		if in.Kind == "work" {
			err = tx.QueryRow(ctx, `INSERT INTO public.works_library(work_name_id,item_type,unit_rate,currency_type) VALUES($1,$2,$3,$4) RETURNING id::text`, args[:4]...).Scan(&out.EntityID)
		} else {
			err = tx.QueryRow(ctx, `INSERT INTO public.materials_library(material_name_id,item_type,unit_rate,currency_type,material_type,consumption_coefficient,delivery_price_type,delivery_amount) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id::text`, args...).Scan(&out.EntityID)
		}
		if err != nil {
			return err
		}
		out.Created = true
	}
	item, err := getLibraryPricingItem(ctx, tx, out.EntityID, in.Kind, true)
	if err != nil {
		return err
	}
	out.LibraryItem = item
	return nil
}
