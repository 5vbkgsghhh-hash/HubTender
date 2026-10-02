// Строки «доп» в Excel-импорте работ/материалов: строка с «доп» в колонке
// «Тип элемента» создаёт новую ДОП-позицию (is_additional) к позиции заказчика,
// работы и материалы под ней уходят в эту ДОП. Номер ДОП присваивает сервер
// в транзакции импорта (POST /api/v1/imports/boq, additional_positions).
//
// Общие правила для массового (ClientPositions) и одиночного (PositionItems)
// импорта — в одном месте, чтобы две реализации не разъезжались
// (memory boq-import-dual-impl).

import { normalizeForLookup, normalizeString, parseNumber } from './importShared';

/** Колонка 5 «Тип элемента» со значением «доп» открывает новую ДОП. */
export const isAdditionalRowMarker = (raw: unknown): boolean =>
  raw != null && String(raw).trim().toLowerCase().replace(/\.$/, '') === 'доп';

export interface ParsedAdditionalRow {
  /** Ключ связи элементов с ДОП внутри импорта (уходит на сервер как temp_id). */
  tempId: string;
  rowIndex: number;
  /** Колонка 7 — наименование ДОП. */
  workName: string;
  /** Колонка 8 — единица измерения. */
  unitCode: string;
  /** Колонка 12 — количество ГП. */
  manualVolume?: number;
  /** Колонка 20 — примечание ГП. */
  manualNote?: string;
  /** Сколько строк работ/материалов попало в эту ДОП. */
  itemsCount: number;
}

export const parseAdditionalRow = (cells: unknown[], rowIndex: number): ParsedAdditionalRow => ({
  tempId: `dop_${rowIndex}`,
  rowIndex,
  workName: cells[6] ? normalizeString(String(cells[6])) : '',
  unitCode: cells[7] ? String(cells[7]).trim() : '',
  manualVolume: parseNumber(cells[11]),
  manualNote: cells[19] ? String(cells[19]).trim() || undefined : undefined,
  itemsCount: 0,
});

/** Проверки полей строки «доп» — те же, что в модалке «Добавить ДОП работу». */
export const validateAdditionalRowBasics = (
  row: ParsedAdditionalRow,
  unitCodes?: Set<string>,
): string[] => {
  const issues: string[] = [];
  if (!row.workName) {
    issues.push('Строка «доп»: не заполнено наименование ДОП (колонка 7)');
  }
  if (!row.unitCode) {
    issues.push('Строка «доп»: не заполнена единица измерения (колонка 8)');
  } else if (unitCodes && unitCodes.size > 0 && !unitCodes.has(row.unitCode)) {
    issues.push(`Строка «доп»: единица измерения «${row.unitCode}» не найдена в справочнике`);
  }
  if (row.manualVolume === undefined || !(row.manualVolume > 0)) {
    issues.push('Строка «доп»: количество ГП (колонка 12) должно быть больше 0');
  }
  return issues;
};

/**
 * Повторы ДОП у одного родителя внутри файла (сравнение без регистра и лишних
 * пробелов): повторная загрузка ДОП запрещена, в том числе внутри одного файла.
 */
export const findDuplicateAdditionalRows = <T extends ParsedAdditionalRow>(
  rows: T[],
  parentKey: (row: T) => string | undefined,
): Array<{ row: T; firstRow: T }> => {
  const seen = new Map<string, T>();
  const duplicates: Array<{ row: T; firstRow: T }> = [];
  rows.forEach((row) => {
    const parent = parentKey(row);
    if (!parent || !row.workName) return;
    const key = `${parent}|${normalizeForLookup(row.workName)}`;
    const firstRow = seen.get(key);
    if (firstRow) {
      duplicates.push({ row, firstRow });
    } else {
      seen.set(key, row);
    }
  });
  return duplicates;
};

export interface AdditionalPositionPayload {
  temp_id: string;
  row_index: number;
  parent_position_id: string;
  work_name: string;
  unit_code: string;
  manual_volume?: number;
  manual_note?: string;
}

export const buildAdditionalPositionPayload = (
  row: ParsedAdditionalRow,
  parentPositionId: string,
): AdditionalPositionPayload => ({
  temp_id: row.tempId,
  row_index: row.rowIndex,
  parent_position_id: parentPositionId,
  work_name: row.workName,
  unit_code: row.unitCode,
  ...(row.manualVolume !== undefined ? { manual_volume: row.manualVolume } : {}),
  ...(row.manualNote ? { manual_note: row.manualNote } : {}),
});

/** ДОП, созданная импортом (ответ сервера и import_sessions.created_positions). */
export interface CreatedAdditionalPosition {
  id: string;
  parent_position_id: string;
  position_number: number;
  work_name: string;
}

/** «5.1, 7.1 …» для сообщений об итогах импорта. */
export const formatCreatedAdditionalNumbers = (created: CreatedAdditionalPosition[], limit = 10): string => {
  const numbers = created.slice(0, limit).map((c) => String(c.position_number));
  return created.length > limit ? `${numbers.join(', ')} …` : numbers.join(', ');
};
