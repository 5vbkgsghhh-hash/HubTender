/**
 * Комбинированный алгоритм оценки совпадения позиций заказчика
 *
 * Взвешенная система оценки:
 * - item_no совпадение: 30%
 * - work_name similarity: 50%
 * - unit_code совпадение: 10%
 * - volume близость: 10%
 *
 * item_no участвует, только если он есть у обеих строк и нумерация между версиями надёжна
 * (`useItemNo`). Иначе его 30 баллов пропорционально переходят к остальным признакам:
 * два пустых номера — не совпадение, а отсутствие данных.
 */

import { calculateStringSimilarity, calculateVolumeProximity, similarityFromNormalized } from './similarity';
import type { ClientPosition } from '../../lib/types';
import type { RichRuns } from '../../lib/types/types/boq';

/**
 * Структура данных распарсенной строки из Excel
 */
export interface ParsedRow {
  item_no: string;
  hierarchy_level: number;
  work_name: string;
  unit_code: string;
  volume: number | null;
  client_note: string;
  rich_runs?: RichRuns; // зачёркивание из Excel (для отображения)
}

/**
 * Детализация оценки совпадения
 */
export interface MatchScoreBreakdown {
  itemNoMatch: number;    // 0-30 баллов (0, если item_no не участвует)
  nameSimil: number;      // 0-50 баллов (до 71.4 без item_no)
  unitMatch: number;      // 0-10 баллов (до 14.3 без item_no)
  volumeProx: number;     // 0-10 баллов (до 14.3 без item_no)
  total: number;          // 0-100 баллов
}

/**
 * Параметры оценки
 */
export interface MatchScoreOptions {
  /** false — нумерация между версиями перебита, item_no не учитывается. По умолчанию true. */
  useItemNo?: boolean;
  /** Готовая схожесть наименований 0..1 — чтобы не считать Левенштейна повторно. */
  nameSimilarity?: number;
}

const ITEM_NO_WEIGHT = 30;

/**
 * Вычислить комбинированную оценку совпадения двух позиций
 *
 * @param oldPos - позиция из старой версии тендера
 * @param newPos - позиция из новой версии (Excel)
 * @param oldWorkNameNorm - (опц.) заранее нормализованное наименование старой позиции
 * @param newWorkNameNorm - (опц.) заранее нормализованное наименование новой позиции
 * @param options - (опц.) учёт item_no и готовая схожесть наименований
 * @returns детализированная оценка с общим score
 */
export function calculateMatchScore(
  oldPos: ClientPosition,
  newPos: ParsedRow,
  oldWorkNameNorm?: string,
  newWorkNameNorm?: string,
  options: MatchScoreOptions = {}
): MatchScoreBreakdown {
  // Нормализация строк для сравнения
  const normalizeString = (str: string | null | undefined): string => {
    return (str || '').trim().toLowerCase();
  };

  // 1. Совпадение номера раздела (item_no) - 30 баллов, если номер есть у обеих строк
  const oldItemNo = normalizeString(oldPos.item_no);
  const newItemNo = normalizeString(newPos.item_no);
  const itemNoApplicable = options.useItemNo !== false && oldItemNo !== '' && newItemNo !== '';
  const itemNoMatch = itemNoApplicable && oldItemNo === newItemNo ? ITEM_NO_WEIGHT : 0;
  // Без item_no остальные признаки делят все 100 баллов в прежней пропорции 50:10:10
  const scale = itemNoApplicable ? 1 : 100 / (100 - ITEM_NO_WEIGHT);

  // 2. Схожесть наименования работы - 50 баллов.
  // Если переданы заранее нормализованные имена — переиспользуем их (без повторного
  // прогона тяжёлой normalizeString). Guard по сырым строкам идентичен calculateStringSimilarity.
  let nameSimilarity: number;
  if (!oldPos.work_name || !newPos.work_name) {
    nameSimilarity = 0;
  } else if (options.nameSimilarity !== undefined) {
    nameSimilarity = options.nameSimilarity;
  } else if (oldWorkNameNorm !== undefined && newWorkNameNorm !== undefined) {
    nameSimilarity = similarityFromNormalized(oldWorkNameNorm, newWorkNameNorm);
  } else {
    nameSimilarity = calculateStringSimilarity(oldPos.work_name, newPos.work_name);
  }
  const nameSimil = nameSimilarity * 50 * scale;

  // 3. Совпадение единицы измерения - 10 баллов
  const oldUnitCode = normalizeString(oldPos.unit_code);
  const newUnitCode = normalizeString(newPos.unit_code);
  const unitMatch = oldUnitCode === newUnitCode ? 10 * scale : 0;

  // 4. Близость количества - 10 баллов
  const volumeProximity = calculateVolumeProximity(
    oldPos.volume ?? null,
    newPos.volume ?? null
  );
  const volumeProx = volumeProximity * 10 * scale;

  // Общая оценка
  const total = itemNoMatch + nameSimil + unitMatch + volumeProx;

  return {
    itemNoMatch,
    nameSimil,
    unitMatch,
    volumeProx,
    total,
  };
}

/**
 * Проверить, является ли score достаточным для автоматического сопоставления
 *
 * @param score - оценка совпадения
 * @param threshold - порог (по умолчанию 95)
 * @returns true если score >= threshold
 */
export function isAutoMatchScore(score: MatchScoreBreakdown, threshold: number = 95): boolean {
  return score.total >= threshold;
}

/**
 * Форматировать оценку для отображения пользователю
 *
 * @param score - оценка совпадения
 * @returns форматированная строка вида "95.5% (раздел: 30, название: 48, ед.: 10, кол.: 7.5)"
 */
export function formatMatchScore(score: MatchScoreBreakdown): string {
  const breakdown = `раздел: ${score.itemNoMatch.toFixed(0)}, ` +
    `название: ${score.nameSimil.toFixed(1)}, ` +
    `ед.: ${score.unitMatch.toFixed(0)}, ` +
    `кол.: ${score.volumeProx.toFixed(1)}`;

  return `${score.total.toFixed(1)}% (${breakdown})`;
}
