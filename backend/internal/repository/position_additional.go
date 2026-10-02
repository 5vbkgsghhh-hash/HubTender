package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ДОП-позиции (client_positions.is_additional): расчёт номера и вставка в
// транзакции вызывающего. Общий путь для модалки «Добавить ДОП работу»
// (CreateAdditionalPosition) и для строк «доп» импорта из Excel
// (import_boq_additional.go).

// ErrParentPositionNotFound is returned when the parent position is missing
// (or belongs to another tender).
var ErrParentPositionNotFound = errors.New("родительская позиция не найдена")

// ErrParentIsAdditional — ДОП нельзя создать под другой ДОП.
var ErrParentIsAdditional = errors.New("к ДОП-работе нельзя добавить ДОП")

// DuplicateAdditionalPositionError — у родителя уже есть ДОП с таким же
// наименованием (сравнение без регистра и лишних пробелов). Возвращается только
// при RejectDuplicateName: повторный импорт того же файла не должен плодить дубли.
type DuplicateAdditionalPositionError struct {
	ParentNumber   float64
	ExistingNumber float64
	WorkName       string
}

func (e *DuplicateAdditionalPositionError) Error() string {
	return fmt.Sprintf("у позиции %s уже есть ДОП %s «%s»",
		formatPositionNumber(e.ParentNumber), formatPositionNumber(e.ExistingNumber), e.WorkName)
}

// CreateAdditionalPositionInput drives the "additional work" create flow
// (AddAdditionalPositionModal.handleOk and the Excel «доп» rows).
type CreateAdditionalPositionInput struct {
	ParentPositionID string
	TenderID         string
	WorkName         string
	UnitCode         *string
	ManualVolume     *float64
	ManualNote       *string
	// RejectDuplicateName — отказ, если у родителя уже есть ДОП с тем же
	// наименованием (импорт). Модалка дубли по-прежнему допускает.
	RejectDuplicateName bool
}

// CreatedAdditionalPosition describes an inserted ДОП (import result and
// import_sessions.created_positions entry).
type CreatedAdditionalPosition struct {
	ID               string  `json:"id"`
	ParentPositionID string  `json:"parent_position_id"`
	PositionNumber   float64 `json:"position_number"`
	WorkName         string  `json:"work_name"`
}

// CreateAdditionalPosition inserts an is_additional child position in its own
// transaction. No created_by (column absent on client_positions; legacy code
// also omitted it).
func (r *PositionRepo) CreateAdditionalPosition(ctx context.Context, in CreateAdditionalPositionInput) (string, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", fmt.Errorf("positionRepo.CreateAdditionalPosition: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	created, err := insertAdditionalPositionTx(ctx, tx, in)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("positionRepo.CreateAdditionalPosition: commit: %w", err)
	}
	return created.ID, nil
}

// insertAdditionalPositionTx locks the parent row (serialises concurrent ДОП
// numbering under one parent), validates it, computes the next decimal-suffixed
// position_number and inserts the is_additional child inside the caller's tx.
func insertAdditionalPositionTx(ctx context.Context, tx pgx.Tx, in CreateAdditionalPositionInput) (*CreatedAdditionalPosition, error) {
	var (
		parentNumber   float64
		parentLevel    *int
		parentIsAdd    bool
		parentTenderID string
	)
	err := tx.QueryRow(ctx, `
		SELECT position_number, hierarchy_level, COALESCE(is_additional, false), tender_id::text
		FROM public.client_positions
		WHERE id = $1::uuid
		FOR UPDATE
	`, in.ParentPositionID).Scan(&parentNumber, &parentLevel, &parentIsAdd, &parentTenderID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && parentTenderID != in.TenderID) {
		return nil, ErrParentPositionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("insertAdditionalPositionTx: parent: %w", err)
	}
	if parentIsAdd {
		return nil, ErrParentIsAdditional
	}

	// Существующие ДОП родителя ниже следующей целой позиции: из них берём
	// последний номер. ДОП с номером (x+1).0 — след старого бага (прод: №306 v2)
	// — в расчёт не попадает и новый номер не уводит в чужой диапазон.
	rows, err := tx.Query(ctx, `
		SELECT position_number, work_name
		FROM public.client_positions
		WHERE parent_position_id = $1::uuid AND is_additional = true
		  AND position_number < floor($2::float8) + 1
	`, in.ParentPositionID, parentNumber)
	if err != nil {
		return nil, fmt.Errorf("insertAdditionalPositionTx: siblings: %w", err)
	}
	var last *float64
	wantName := normalizeAdditionalName(in.WorkName)
	var duplicate *DuplicateAdditionalPositionError
	for rows.Next() {
		var num float64
		var name string
		if err := rows.Scan(&num, &name); err != nil {
			rows.Close()
			return nil, fmt.Errorf("insertAdditionalPositionTx: siblings scan: %w", err)
		}
		if last == nil || num > *last {
			n := num
			last = &n
		}
		if in.RejectDuplicateName && duplicate == nil && normalizeAdditionalName(name) == wantName {
			duplicate = &DuplicateAdditionalPositionError{
				ParentNumber: parentNumber, ExistingNumber: num, WorkName: strings.TrimSpace(name),
			}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("insertAdditionalPositionTx: siblings: %w", err)
	}
	if duplicate != nil {
		return nil, duplicate
	}

	newNumber, err := nextAdditionalPositionNumber(parentNumber, last)
	if err != nil {
		return nil, err
	}
	level := 1
	if parentLevel != nil {
		level = *parentLevel + 1
	}

	created := &CreatedAdditionalPosition{
		ParentPositionID: in.ParentPositionID,
		PositionNumber:   newNumber,
		WorkName:         in.WorkName,
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO public.client_positions
			(tender_id, position_number, work_name, unit_code, manual_volume,
			 manual_note, hierarchy_level, is_additional, parent_position_id,
			 volume, client_note, item_no)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, true, $8::uuid, NULL, NULL, NULL)
		RETURNING id::text
	`, in.TenderID, newNumber, in.WorkName, in.UnitCode, in.ManualVolume,
		in.ManualNote, level, in.ParentPositionID).Scan(&created.ID); err != nil {
		return nil, fmt.Errorf("insertAdditionalPositionTx: insert: %w", err)
	}
	return created, nil
}

// nextAdditionalPositionNumber returns the position_number of the next ДОП.
// lastInRange is the largest number among the parent's ДОП below the next
// integer position (nil when the parent has none).
//
// Шаг 0.1 (5.1 … 5.9); после 5.9 шаг дробится — 5.91 … 5.99, 5.991 … — и
// номер никогда не доходит до следующей позиции. Прежний расчёт после x.9
// давал (x+1).0 — дубль номера соседней позиции, а импорт сопоставляет позиции
// по номеру. Двузначный суффикс (5.10) невозможен: position_number — numeric,
// 5.10 = 5.1.
func nextAdditionalPositionNumber(parentNumber float64, lastInRange *float64) (float64, error) {
	if lastInRange == nil {
		return roundDecimals(parentNumber+0.1, 6), nil
	}
	base := math.Floor(*lastInRange)
	frac := roundDecimals(*lastInRange-base, 6)
	step := 0.1
	for digits := 1; digits <= 6; digits++ {
		candidate := roundDecimals(frac+step, digits)
		if candidate > frac && candidate < 1 {
			return roundDecimals(base+candidate, digits), nil
		}
		step /= 10
	}
	return 0, fmt.Errorf("nextAdditionalPositionNumber: нет свободного номера после %s",
		formatPositionNumber(*lastInRange))
}

func roundDecimals(v float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(v*p) / p
}

// normalizeAdditionalName — ключ сравнения наименований ДОП: без регистра и
// лишних пробелов (как normalizeForLookup на фронте).
func normalizeAdditionalName(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// formatPositionNumber печатает номер позиции без хвостовых нулей (5.1, 1546.91).
func formatPositionNumber(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
