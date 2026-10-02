// Чистые билдеры payload'а для POST /api/v1/imports/boq и анализ расхождений
// после атомарного импорта. Вынесено из useMassBoqImport без изменений логики.
// Импортируем соседей напрямую (не через '../utils' — барель реэкспортирует
// и этот файл, self-import создал бы цикл).
//
// Этап 0-F1: клиент передаёт ТОЛЬКО исходные данные (quantity, unit_rate,
// currency, коэффициенты, доставка, связи). total_amount больше не отправляется —
// авторитетную сумму каждой строки считает сервер (calc.CalculateBoqItemTotalAmount)
// по фактическим курсам тендера; поле в API осталось только для обратной
// совместимости старых клиентов как диагностическое контрольное значение.
import { ParsedBoqItem, PositionUpdateData, ParsedAdditionalPosition } from './massBoqImportUtils';
import {
  buildAdditionalPositionPayload,
  type AdditionalPositionPayload,
} from '../../../utils/boq/additionalImport';

// Билдер элементов общий с одиночным импортом (PositionItems).
export { buildBoqItemsPayload } from '../../../utils/boq/importPayload';

export const buildPositionUpdatesPayload = (
  positionUpdates: Map<string, PositionUpdateData>,
): Record<string, unknown>[] => {
  return Array.from(positionUpdates.values())
    .filter(posData =>
      posData.positionId &&
      (posData.manualVolume !== undefined || posData.manualNote !== undefined)
    )
    .map((posData) => {
      const payload: Record<string, unknown> = {
        position_id: posData.positionId,
        position_number: posData.positionNumber,
      };

      if (posData.manualVolume !== undefined) {
        payload.manual_volume = posData.manualVolume;
      }
      if (posData.manualNote !== undefined) {
        payload.manual_note = posData.manualNote;
      }

      return payload;
    });
};

/** Строки «доп» с найденной позицией-родителем (заполняет валидация). */
export const buildAdditionalPositionsPayload = (
  additionalPositions: ParsedAdditionalPosition[],
): AdditionalPositionPayload[] =>
  additionalPositions
    .filter((dop) => dop.parentPositionId)
    .map((dop) => buildAdditionalPositionPayload(dop, dop.parentPositionId as string));

/** Диагностическая запись отчёта сервера: legacy контрольное значение
 *  total_amount разошлось с авторитетным серверным расчётом. Warning, не ошибка;
 *  в БД всегда сохранён server total. */
export interface ImportTotalMismatch {
  row_number: number;
  item_name: string;
  client_total_amount: number;
  server_total_amount: number;
  absolute_difference: number;
  relative_difference_percent: number;
}

export interface ImportMismatchAnalysis {
  expectedItems: number;
  expectedPositions: number;
  droppedItems: number;
  mismatch: boolean;
  droppedRows: number[];
  mismatchMsg: string;
}

/**
 * Сверка количеств после импорта: бэк атомарен и вставляет ровно то, что мы
 * отправили, поэтому любое расхождение = тихая потеря данных. droppedItems —
 * элементы, выброшенные фильтром matchedPositionId (не сопоставлены с позицией).
 * Побочные эффекты (message.error / setImportError) остаются в хуке.
 */
export const analyzeImportMismatch = (
  insertedItemsCount: number,
  updatedPositionsCount: number,
  itemsPayloadLength: number,
  positionUpdatesPayloadLength: number,
  data: ParsedBoqItem[],
  createdAdditionalCount = 0,
  additionalPayloadLength = 0,
): ImportMismatchAnalysis => {
  const expectedItems = itemsPayloadLength;
  const expectedPositions = positionUpdatesPayloadLength;
  const droppedItems = data.length - expectedItems;

  const mismatch =
    insertedItemsCount !== expectedItems ||
    updatedPositionsCount !== expectedPositions ||
    createdAdditionalCount !== additionalPayloadLength ||
    droppedItems > 0;

  const droppedRows = mismatch
    ? data.filter(item => !item.matchedPositionId && !item.additionalTempId).map(item => item.rowIndex)
    : [];

  const mismatchMsg =
    `Импортировано ${insertedItemsCount} из ${expectedItems} элементов, ` +
    `обновлено ${updatedPositionsCount} из ${expectedPositions} позиций` +
    (additionalPayloadLength > 0 ? `, создано ${createdAdditionalCount} из ${additionalPayloadLength} ДОП` : '') +
    (droppedItems > 0 ? `; пропущено строк без позиции: ${droppedItems}` : '') +
    ' — часть данных не загружена. Проверьте позиции.';

  return { expectedItems, expectedPositions, droppedItems, mismatch, droppedRows, mismatchMsg };
};
