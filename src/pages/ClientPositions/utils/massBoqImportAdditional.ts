// Валидация строк «доп» массового импорта: родитель — ближайшая строка с
// номером выше (существующая ДОП заменяется своей позицией заказчика), поля как
// в модалке «Добавить ДОП работу», дубли запрещены — и с уже существующими ДОП
// родителя, и внутри файла (повторная загрузка того же файла).
import {
  ParsedAdditionalPosition,
  ClientPosition,
  ValidationError,
  normalizePositionNumber,
  normalizeForLookup,
} from './massBoqImportUtils';
import {
  findDuplicateAdditionalRows,
  validateAdditionalRowBasics,
} from '../../../utils/boq/additionalImport';

/** Существующая ДОП позиции: ключ сравнения наименования + номер для сообщений. */
export interface ExistingAdditionalRef {
  key: string;
  number: string;
  workName: string;
}

export interface AdditionalValidationMaps {
  clientPositionsMap: Map<string, ClientPosition>;
  positionsById: Map<string, ClientPosition>;
  existingAdditionalByParent: Map<string, ExistingAdditionalRef[]>;
  unitCodes: Set<string>;
}

/** Проверяет строки «доп» и проставляет parentPositionId / parentPositionLabel. */
export const validateAdditionalPositions = (
  additionalPositions: ParsedAdditionalPosition[],
  { clientPositionsMap, positionsById, existingAdditionalByParent, unitCodes }: AdditionalValidationMaps,
): ValidationError[] => {
  const errors: ValidationError[] = [];
  const push = (rowIndex: number, message: string) => {
    errors.push({ rowIndex, type: 'additional_error', field: 'additional', message, severity: 'error' });
  };

  additionalPositions.forEach((dop) => {
    dop.parentPositionId = undefined;
    dop.parentPositionLabel = undefined;

    if (dop.columnB) {
      push(dop.rowIndex,
        `У строки «доп» колонка 2 (№ п/п) должна быть пустой — номер ДОП присваивается при загрузке (указано «${dop.columnB}»)`);
    }
    validateAdditionalRowBasics(dop, unitCodes).forEach((message) => push(dop.rowIndex, message));

    if (!dop.parentPositionNumber) {
      push(dop.rowIndex, 'Строка «доп» стоит выше первой позиции заказчика — не к чему привязать ДОП');
      return;
    }
    let parent = clientPositionsMap.get(dop.parentPositionNumber);
    if (!parent) {
      push(dop.rowIndex, `Позиция «${dop.parentPositionNumber}» для ДОП не найдена в тендере`);
      return;
    }
    // «доп» под существующей ДОП (5.1 из выгрузки) — ДОП к той же позиции заказчика.
    if (parent.is_additional) {
      const customerPosition = parent.parent_position_id ? positionsById.get(parent.parent_position_id) : undefined;
      if (!customerPosition) {
        push(dop.rowIndex, `Не найдена позиция заказчика для ДОП ${dop.parentPositionNumber}`);
        return;
      }
      parent = customerPosition;
    }

    const parentNumber = normalizePositionNumber(parent.position_number);
    dop.parentPositionId = parent.id;
    dop.parentPositionLabel = `${parentNumber} ${parent.work_name}`;

    const nameKey = normalizeForLookup(dop.workName);
    const existing = dop.workName
      ? existingAdditionalByParent.get(parent.id)?.find((ref) => ref.key === nameKey)
      : undefined;
    if (existing) {
      push(dop.rowIndex,
        `У позиции ${parentNumber} уже есть ДОП ${existing.number} «${existing.workName}» — повторно создать её нельзя`);
    }
  });

  findDuplicateAdditionalRows(additionalPositions, (dop) => dop.parentPositionId)
    .forEach(({ row, firstRow }) => {
      push(row.rowIndex,
        `ДОП «${row.workName}» для этой позиции уже есть в файле (строка ${firstRow.rowIndex})`);
    });

  return errors;
};
