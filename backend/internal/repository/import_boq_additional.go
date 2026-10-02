package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ImportAdditionalPosition — строка «доп» из Excel: новая ДОП-позиция к позиции
// заказчика. Создаётся в транзакции импорта ДО вставки работ/материалов;
// элементы ссылаются на неё через ImportBoqItem.ClientPositionTempID.
type ImportAdditionalPosition struct {
	RowIndex         *int     `json:"row_index"`
	TempID           string   `json:"temp_id"`
	ParentPositionID string   `json:"parent_position_id"`
	WorkName         string   `json:"work_name"`
	UnitCode         *string  `json:"unit_code"`
	ManualVolume     *float64 `json:"manual_volume"`
	ManualNote       *string  `json:"manual_note"`
}

// createImportAdditionalPositionsTx creates the import's ДОП in file order and
// returns temp_id → new position id. A parent that already has a ДОП with the
// same name — including one created earlier in this very import — aborts the
// whole import (повторная загрузка того же файла запрещена).
func createImportAdditionalPositionsTx(
	ctx context.Context,
	tx pgx.Tx,
	tenderID string,
	dops []ImportAdditionalPosition,
) (map[string]string, []CreatedAdditionalPosition, error) {
	tempToID := make(map[string]string, len(dops))
	created := make([]CreatedAdditionalPosition, 0, len(dops))

	for _, d := range dops {
		rowLabel := importRowLabel(d.RowIndex)
		name := strings.Join(strings.Fields(d.WorkName), " ")

		switch {
		case d.TempID == "":
			return nil, nil, &ErrBulkImport{Message: fmt.Sprintf("Строка %s: у строки «доп» нет temp_id", rowLabel)}
		case tempToID[d.TempID] != "":
			return nil, nil, &ErrBulkImport{Message: fmt.Sprintf("Строка %s: temp_id ДОП повторяется (%s)", rowLabel, d.TempID)}
		case name == "":
			return nil, nil, &ErrBulkImport{Message: fmt.Sprintf("Строка %s: у строки «доп» не заполнено наименование", rowLabel)}
		case d.ParentPositionID == "":
			return nil, nil, &ErrBulkImport{Message: fmt.Sprintf("Строка %s: не определена позиция заказчика для ДОП", rowLabel)}
		}

		c, err := insertAdditionalPositionTx(ctx, tx, CreateAdditionalPositionInput{
			ParentPositionID:    d.ParentPositionID,
			TenderID:            tenderID,
			WorkName:            name,
			UnitCode:            d.UnitCode,
			ManualVolume:        d.ManualVolume,
			ManualNote:          d.ManualNote,
			RejectDuplicateName: true,
		})
		if err != nil {
			return nil, nil, additionalInsertError(err, rowLabel)
		}
		tempToID[d.TempID] = c.ID
		created = append(created, *c)
	}
	return tempToID, created, nil
}

// additionalInsertError переводит ошибку создания ДОП в ErrBulkImport (400) с
// номером строки Excel; прочие ошибки — 500 с контекстом в логах.
func additionalInsertError(err error, rowLabel string) error {
	var dup *DuplicateAdditionalPositionError
	switch {
	case errors.As(err, &dup):
		return &ErrBulkImport{Message: fmt.Sprintf(
			"Строка %s: %s — повторно создать её нельзя", rowLabel, dup.Error())}
	case errors.Is(err, ErrParentPositionNotFound):
		return &ErrBulkImport{Message: fmt.Sprintf(
			"Строка %s: позиция заказчика для ДОП не найдена в тендере", rowLabel)}
	case errors.Is(err, ErrParentIsAdditional):
		return &ErrBulkImport{Message: fmt.Sprintf("Строка %s: %s", rowLabel, err.Error())}
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && len(pgErr.Code) >= 2 && (pgErr.Code[:2] == "22" || pgErr.Code[:2] == "23") {
		reason := pgErr.Message
		if pgErr.ConstraintName == "client_positions_unit_code_fkey" {
			reason = "единица измерения ДОП не найдена в справочнике"
		}
		return &ErrBulkImport{Message: fmt.Sprintf("Строка %s: %s", rowLabel, reason)}
	}
	return fmt.Errorf("importRepo.BulkImport: create additional position (row %s): %w", rowLabel, err)
}

// resolveItemPositionTempIDs returns a copy of items where every
// client_position_temp_id is replaced by the id of the ДОП created in this tx.
func resolveItemPositionTempIDs(items []ImportBoqItem, tempToID map[string]string) ([]ImportBoqItem, error) {
	out := make([]ImportBoqItem, len(items))
	copy(out, items)
	for i := range out {
		ref := out[i].ClientPositionTempID
		if ref == nil || *ref == "" {
			continue
		}
		rowLabel := importRowLabel(out[i].RowIndex)
		if out[i].ClientPositionID != "" {
			return nil, &ErrBulkImport{Message: fmt.Sprintf(
				"Строка %s: у элемента одновременно указаны позиция и ДОП", rowLabel)}
		}
		id, ok := tempToID[*ref]
		if !ok {
			return nil, &ErrBulkImport{Message: fmt.Sprintf(
				"Строка %s: не найдена ДОП для элемента (temp ref %s)", rowLabel, *ref)}
		}
		out[i].ClientPositionID = id
	}
	return out, nil
}

func importRowLabel(rowIndex *int) string {
	if rowIndex == nil {
		return "?"
	}
	return fmt.Sprintf("%d", *rowIndex)
}
