package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors returned by CancelSession so handlers can map them to the
// right HTTP status via errors.Is.
var (
	ErrImportSessionForbidden        = errors.New("import session: not owner")
	ErrImportSessionNotFound         = errors.New("import session: not found")
	ErrImportSessionAlreadyCancelled = errors.New("import session: already cancelled")
)

// ImportLogRepo handles import_sessions reads + atomic session-cancel.
type ImportLogRepo struct {
	pool *pgxpool.Pool
}

// NewImportLogRepo creates an ImportLogRepo.
func NewImportLogRepo(pool *pgxpool.Pool) *ImportLogRepo {
	return &ImportLogRepo{pool: pool}
}

// ImportSessionRow mirrors the projection used by Admin/ImportLog.
type ImportSessionRow struct {
	ID                string          `json:"id"`
	UserID            string          `json:"user_id"`
	TenderID          string          `json:"tender_id"`
	FileName          *string         `json:"file_name,omitempty"`
	ItemsCount        int             `json:"items_count"`
	ImportedAt        *string         `json:"imported_at,omitempty"`
	CancelledAt       *string         `json:"cancelled_at,omitempty"`
	CancelledBy       *string         `json:"cancelled_by,omitempty"`
	PositionsSnapshot json.RawMessage `json:"positions_snapshot,omitempty"`
	// CreatedPositions — ДОП, созданные строками «доп» этого импорта
	// ([]CreatedAdditionalPosition как jsonb).
	CreatedPositions json.RawMessage `json:"created_positions,omitempty"`
}

// ListSessions returns up to 200 latest sessions, optionally filtered by
// tenderID and/or restrictUserID (when non-empty, only that user's sessions).
func (r *ImportLogRepo) ListSessions(ctx context.Context, tenderID, restrictUserID string) ([]ImportSessionRow, error) {
	var (
		where []string
		args  []any
	)
	if tenderID != "" {
		args = append(args, tenderID)
		where = append(where, fmt.Sprintf("tender_id = $%d", len(args)))
	}
	if restrictUserID != "" {
		args = append(args, restrictUserID)
		where = append(where, fmt.Sprintf("user_id = $%d", len(args)))
	}
	clause := ""
	if len(where) > 0 {
		clause = "WHERE " + strings.Join(where, " AND ")
	}
	query := fmt.Sprintf(`
		SELECT id::text, user_id::text, tender_id::text, file_name, items_count,
		       to_char(imported_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       to_char(cancelled_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"'),
		       cancelled_by::text, positions_snapshot, created_positions
		FROM public.import_sessions
		%s
		ORDER BY imported_at DESC
		LIMIT 200
	`, clause)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("importLogRepo.ListSessions: %w", err)
	}
	defer rows.Close()

	out := make([]ImportSessionRow, 0)
	for rows.Next() {
		var (
			rec      ImportSessionRow
			snapshot []byte
			created  []byte
		)
		if err := rows.Scan(&rec.ID, &rec.UserID, &rec.TenderID, &rec.FileName, &rec.ItemsCount,
			&rec.ImportedAt, &rec.CancelledAt, &rec.CancelledBy, &snapshot, &created); err != nil {
			return nil, fmt.Errorf("importLogRepo.ListSessions scan: %w", err)
		}
		if len(snapshot) > 0 {
			rec.PositionsSnapshot = snapshot
		}
		if len(created) > 0 {
			rec.CreatedPositions = created
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// ImportLogUserRow has the user fields surfaced by Admin/ImportLog.
type ImportLogUserRow struct {
	ID       string `json:"id"`
	FullName string `json:"full_name"`
	RoleCode string `json:"role_code"`
	Roles    *struct {
		Name  string  `json:"name"`
		Color *string `json:"color,omitempty"`
	} `json:"roles,omitempty"`
}

// UsersByIDs returns the embedded {role_code, roles{name,color}} for each id.
func (r *ImportLogRepo) UsersByIDs(ctx context.Context, ids []string) ([]ImportLogUserRow, error) {
	if len(ids) == 0 {
		return []ImportLogUserRow{}, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT u.id::text, u.full_name, u.role_code,
		       r.name, r.color
		FROM public.users u
		LEFT JOIN public.roles r ON r.code = u.role_code
		WHERE u.id = ANY($1::uuid[])
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("importLogRepo.UsersByIDs: %w", err)
	}
	defer rows.Close()
	out := make([]ImportLogUserRow, 0)
	for rows.Next() {
		var (
			rec       ImportLogUserRow
			roleName  *string
			roleColor *string
		)
		if err := rows.Scan(&rec.ID, &rec.FullName, &rec.RoleCode, &roleName, &roleColor); err != nil {
			return nil, fmt.Errorf("importLogRepo.UsersByIDs scan: %w", err)
		}
		if roleName != nil {
			rec.Roles = &struct {
				Name  string  `json:"name"`
				Color *string `json:"color,omitempty"`
			}{Name: *roleName, Color: roleColor}
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

type TenderShort struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	TenderNumber string `json:"tender_number"`
	Version      int    `json:"version"`
}

func (r *ImportLogRepo) TendersByIDs(ctx context.Context, ids []string) ([]TenderShort, error) {
	if len(ids) == 0 {
		return []TenderShort{}, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, COALESCE(title, ''), COALESCE(tender_number, ''), COALESCE(version, 1)
		FROM public.tenders
		WHERE id = ANY($1::uuid[])
	`, ids)
	if err != nil {
		return nil, fmt.Errorf("importLogRepo.TendersByIDs: %w", err)
	}
	defer rows.Close()
	out := make([]TenderShort, 0)
	for rows.Next() {
		var t TenderShort
		if err := rows.Scan(&t.ID, &t.Title, &t.TenderNumber, &t.Version); err != nil {
			return nil, fmt.Errorf("importLogRepo.TendersByIDs scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *ImportLogRepo) ListAllTendersForFilter(ctx context.Context) ([]TenderShort, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, COALESCE(title, ''), COALESCE(tender_number, ''), COALESCE(version, 1)
		FROM public.tenders
		ORDER BY title
	`)
	if err != nil {
		return nil, fmt.Errorf("importLogRepo.ListAllTendersForFilter: %w", err)
	}
	defer rows.Close()
	out := make([]TenderShort, 0)
	for rows.Next() {
		var t TenderShort
		if err := rows.Scan(&t.ID, &t.Title, &t.TenderNumber, &t.Version); err != nil {
			return nil, fmt.Errorf("importLogRepo.ListAllTendersForFilter scan: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CancelResult is returned by CancelSession to summarise what was undone.
type CancelResult struct {
	BoqDeleted        int `json:"boq_deleted"`
	PositionsRestored int `json:"positions_restored"`
	// AdditionalDeleted — удалённые ДОП, созданные импортом; AdditionalKept —
	// оставленные: в них есть элементы не из этого импорта.
	AdditionalDeleted int `json:"additional_deleted"`
	AdditionalKept    int `json:"additional_kept"`
	// TenderID — для инвалидации кэшей и пересчёта коммерции в сервисе.
	TenderID string `json:"-"`
}

// CancelSession atomically:
//  1. deletes boq_items rows tagged with import_session_id
//  2. deletes the ДОП the import created, if they are empty now
//  3. restores client_positions.manual_volume / manual_note from snapshot
//  4. re-aggregates the affected positions and the tender grand total
//  5. marks import_sessions row as cancelled
func (r *ImportLogRepo) CancelSession(ctx context.Context, sessionID, cancelledBy string, requireOwnership bool) (*CancelResult, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("importLogRepo.CancelSession: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Атрибутируем удаление импортированных строк отменившему пользователю
	// (иначе триггерный аудит запишет «Системную операцию»).
	if err := setAuditUser(ctx, tx, cancelledBy); err != nil {
		return nil, fmt.Errorf("importLogRepo.CancelSession: %w", err)
	}

	var (
		ownerID     string
		tenderID    *string
		snapshot    []byte
		createdRaw  []byte
		cancelledAt *time.Time
	)
	err = tx.QueryRow(ctx, `
		SELECT user_id::text, tender_id::text, positions_snapshot, created_positions, cancelled_at
		FROM public.import_sessions
		WHERE id = $1
		FOR UPDATE
	`, sessionID).Scan(&ownerID, &tenderID, &snapshot, &createdRaw, &cancelledAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrImportSessionNotFound
		}
		return nil, fmt.Errorf("importLogRepo.CancelSession: load: %w", err)
	}
	if requireOwnership && ownerID != cancelledBy {
		return nil, ErrImportSessionForbidden
	}
	// Повторная отмена снова накатила бы снимок ГП поверх более поздних правок.
	if cancelledAt != nil {
		return nil, ErrImportSessionAlreadyCancelled
	}

	res := &CancelResult{}
	if tenderID != nil {
		res.TenderID = *tenderID
		// Удаление строк меняет итоги: одна ревизия на команду, коммерция
		// пересчитывается асинхронно (как у импорта и удаления позиций).
		if _, err := MarkTenderFinancialInputsChangedTx(ctx, tx, *tenderID, "import_cancel"); err != nil {
			return nil, fmt.Errorf("importLogRepo.CancelSession: %w", err)
		}
	}

	// Позиции, чьи итоги изменятся, — определяем до удаления.
	var affected []string
	if err := pgxScanStrings(ctx, tx, &affected, `
		SELECT DISTINCT client_position_id::text FROM public.boq_items WHERE import_session_id = $1
	`, sessionID); err != nil {
		return nil, fmt.Errorf("importLogRepo.CancelSession: affected positions: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		DELETE FROM public.boq_items WHERE import_session_id = $1
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("importLogRepo.CancelSession: delete boq: %w", err)
	}
	res.BoqDeleted = int(tag.RowsAffected())

	if err := deleteImportCreatedPositionsTx(ctx, tx, createdRaw, tenderID, res); err != nil {
		return nil, err
	}

	if len(snapshot) > 0 {
		// Snapshot is JSONB array of {id, manual_volume, manual_note}.
		var snaps []struct {
			ID           string   `json:"id"`
			ManualVolume *float64 `json:"manual_volume"`
			ManualNote   *string  `json:"manual_note"`
		}
		if err := json.Unmarshal(snapshot, &snaps); err != nil {
			return nil, fmt.Errorf("importLogRepo.CancelSession: parse snapshot: %w", err)
		}
		for _, s := range snaps {
			_, err := tx.Exec(ctx, `
				UPDATE public.client_positions
				SET manual_volume = $1, manual_note = $2
				WHERE id = $3
			`, s.ManualVolume, s.ManualNote, s.ID)
			if err != nil {
				return nil, fmt.Errorf("importLogRepo.CancelSession: restore: %w", err)
			}
			res.PositionsRestored++
		}
	}

	if err := recomputePositionTotalsByIDsTx(ctx, tx, affected); err != nil {
		return nil, fmt.Errorf("importLogRepo.CancelSession: %w", err)
	}
	if tenderID != nil {
		if _, err := RecalculateTenderGrandTotalTx(ctx, tx, *tenderID); err != nil {
			return nil, fmt.Errorf("importLogRepo.CancelSession: grand total: %w", err)
		}
	}

	_, err = tx.Exec(ctx, `
		UPDATE public.import_sessions
		SET cancelled_at = NOW(), cancelled_by = $1
		WHERE id = $2
	`, cancelledBy, sessionID)
	if err != nil {
		return nil, fmt.Errorf("importLogRepo.CancelSession: mark cancelled: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("importLogRepo.CancelSession: commit: %w", err)
	}
	return res, nil
}

// deleteImportCreatedPositionsTx deletes the ДОП created by the cancelled
// import (import_sessions.created_positions) once they are empty. A ДОП that
// still has items — added by hand or by a later import — is kept and counted
// in res.AdditionalKept: удалить её значило бы каскадом стереть чужие строки.
func deleteImportCreatedPositionsTx(ctx context.Context, tx pgx.Tx, createdRaw []byte, tenderID *string, res *CancelResult) error {
	if len(createdRaw) == 0 || tenderID == nil {
		return nil
	}
	var created []CreatedAdditionalPosition
	if err := json.Unmarshal(createdRaw, &created); err != nil {
		return fmt.Errorf("importLogRepo.CancelSession: parse created positions: %w", err)
	}
	if len(created) == 0 {
		return nil
	}
	ids := make([]string, 0, len(created))
	for _, c := range created {
		ids = append(ids, c.ID)
	}
	tag, err := tx.Exec(ctx, `
		DELETE FROM public.client_positions cp
		WHERE cp.id = ANY($1::uuid[])
		  AND cp.tender_id = $2::uuid
		  AND cp.is_additional = true
		  AND NOT EXISTS (SELECT 1 FROM public.boq_items b WHERE b.client_position_id = cp.id)
	`, ids, *tenderID)
	if err != nil {
		return fmt.Errorf("importLogRepo.CancelSession: delete created positions: %w", err)
	}
	res.AdditionalDeleted = int(tag.RowsAffected())
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM public.client_positions WHERE id = ANY($1::uuid[])
	`, ids).Scan(&res.AdditionalKept); err != nil {
		return fmt.Errorf("importLogRepo.CancelSession: count kept positions: %w", err)
	}
	return nil
}

// pgxScanStrings runs a single-text-column query into dst.
func pgxScanStrings(ctx context.Context, tx pgx.Tx, dst *[]string, q string, args ...any) error {
	rows, err := tx.Query(ctx, q, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return err
		}
		*dst = append(*dst, s)
	}
	return rows.Err()
}
