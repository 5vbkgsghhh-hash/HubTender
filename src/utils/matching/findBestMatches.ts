/**
 * Поиск лучших совпадений между позициями старой и новой версий тендера
 *
 * Строки ВОР между версиями идут в прежнем порядке, поэтому сопоставление — выравнивание
 * (как diff), а не поиск «лучшего кандидата по всему тендеру». Жадный глобальный поиск
 * давал каскад ошибок: новая строка одного раздела забирала похожую строку другого,
 * и дальше все одноимённые строки съезжали.
 *
 * 1. Якоря — строки с точно совпавшим наименованием, с сохранением порядка (alignByKey).
 *    Одинаковые наименования различает место в документе, а не номер раздела.
 * 2. Нечёткий поиск (переименования, смена объёма или ед.) — только внутри «дыры» между
 *    соседними якорями и только при схожести наименований не ниже 60%.
 * 3. Перенесённые блоки — точное наименование + ед. среди оставшихся строк
 *    с продолжением последовательности.
 *
 * item_no влияет только на оценку и только если нумерация между версиями не перебита.
 */

import {
  calculateMatchScore,
  isAutoMatchScore,
  type ParsedRow,
  type MatchScoreBreakdown,
} from './calculateMatchScore';
import { normalizeString, similarityFromNormalized } from './similarity';
import { alignByKey } from './alignByKey';
import type { ClientPosition } from '../../lib/types';

/** Минимальная схожесть наименований (0..1) для нечёткой пары. */
const MIN_FUZZY_NAME_SIMILARITY = 0.6;
/** Нечёткая пара принимается при оценке строго выше этого значения. */
const MIN_FUZZY_SCORE = 50;
/** Доля строк с тем же номером и наименованием, ниже которой нумерацию считаем перебитой. */
const MIN_ITEM_NO_RELIABILITY = 0.5;
/** Дыра крупнее (старые × новые строки) оценивается только в полосе вдоль диагонали. */
const MAX_FULL_GAP_PAIRS = 40_000;
/** Полуширина полосы для крупных дыр, строк. */
const GAP_BAND = 60;

interface RowMeta {
  itemNo: string;
  unitCode: string;
  workName: string;
}

/**
 * Результат сопоставления одной позиции
 */
export interface MatchResult {
  oldPositionId: string;
  newPositionIndex: number;
  score: MatchScoreBreakdown;
  matchType: 'auto' | 'low_confidence';
}

/** Как в calculateMatchScore: номер и ед. сравниваются без вычистки символов («1.11» ≠ «11.1»). */
function normalizeCode(value: string | null | undefined): string {
  return (value || '').trim().toLowerCase();
}

function buildMeta(position: ClientPosition | ParsedRow): RowMeta {
  return {
    itemNo: normalizeCode(position.item_no),
    unitCode: normalizeCode(position.unit_code),
    workName: normalizeString(position.work_name || ''),
  };
}

/**
 * Нумерация надёжна, если у большинства пронумерованных строк новой версии в старой есть
 * строка с тем же номером и тем же наименованием. Иначе заказчик перенумеровал разделы
 * (10.4.1 → 1.7.4.1), и случайное совпадение номеров («1.1» стал другим разделом) только вредит.
 */
function isItemNoReliable(oldMetas: RowMeta[], newMetas: RowMeta[]): boolean {
  const namesByItemNo = new Map<string, Set<string>>();
  for (const meta of oldMetas) {
    if (!meta.itemNo) continue;
    const names = namesByItemNo.get(meta.itemNo) ?? new Set<string>();
    names.add(meta.workName);
    namesByItemNo.set(meta.itemNo, names);
  }

  let numbered = 0;
  let confirmed = 0;
  for (const meta of newMetas) {
    if (!meta.itemNo) continue;
    numbered++;
    if (namesByItemNo.get(meta.itemNo)?.has(meta.workName)) confirmed++;
  }

  return numbered > 0 && confirmed / numbered >= MIN_ITEM_NO_RELIABILITY;
}

/** Схожесть наименований с дешёвым отсевом: расстояние Левенштейна не меньше разницы длин. */
function nameSimilarity(left: string, right: string): number {
  if (left === right) return 1;
  const maxLength = Math.max(left.length, right.length);
  if (Math.min(left.length, right.length) / maxLength < MIN_FUZZY_NAME_SIMILARITY) return 0;
  return similarityFromNormalized(left, right);
}

class Alignment {
  readonly newToOld: Int32Array;
  readonly oldUsed: Uint8Array;
  /** Схожесть наименований сопоставленной пары — чтобы не считать её повторно. */
  readonly similarity: Float64Array;

  constructor(oldCount: number, newCount: number) {
    this.newToOld = new Int32Array(newCount).fill(-1);
    this.oldUsed = new Uint8Array(oldCount);
    this.similarity = new Float64Array(newCount);
  }

  link(oldIdx: number, newIdx: number, similarity: number) {
    this.newToOld[newIdx] = oldIdx;
    this.oldUsed[oldIdx] = 1;
    this.similarity[newIdx] = similarity;
  }
}

interface MatchContext {
  oldPositions: ClientPosition[];
  newPositions: ParsedRow[];
  oldMetas: RowMeta[];
  newMetas: RowMeta[];
  useItemNo: boolean;
  alignment: Alignment;
}

function scorePair(ctx: MatchContext, oldIdx: number, newIdx: number, similarity: number) {
  return calculateMatchScore(
    ctx.oldPositions[oldIdx],
    ctx.newPositions[newIdx],
    ctx.oldMetas[oldIdx].workName,
    ctx.newMetas[newIdx].workName,
    { useItemNo: ctx.useItemNo, nameSimilarity: similarity }
  );
}

/** Фаза 2: нечёткие пары внутри дыры old [oldStart, oldEnd) × new [newStart, newEnd). */
function matchGap(ctx: MatchContext, oldStart: number, oldEnd: number, newStart: number, newEnd: number) {
  const { alignment, oldMetas, newMetas } = ctx;
  const oldSize = oldEnd - oldStart;
  const newSize = newEnd - newStart;
  const fullScan = oldSize * newSize <= MAX_FULL_GAP_PAIRS;
  const candidates: Array<{ total: number; offset: number; oldIdx: number; newIdx: number; similarity: number }> = [];

  for (let newIdx = newStart; newIdx < newEnd; newIdx++) {
    let from = oldStart;
    let to = oldEnd;
    if (!fullScan) {
      const center = oldStart + Math.round(((newIdx - newStart) * oldSize) / newSize);
      from = Math.max(oldStart, center - GAP_BAND);
      to = Math.min(oldEnd, center + GAP_BAND + 1);
    }

    for (let oldIdx = from; oldIdx < to; oldIdx++) {
      const similarity = nameSimilarity(oldMetas[oldIdx].workName, newMetas[newIdx].workName);
      if (similarity < MIN_FUZZY_NAME_SIMILARITY) continue;

      const { total } = scorePair(ctx, oldIdx, newIdx, similarity);
      if (total <= MIN_FUZZY_SCORE) continue;

      const offset = Math.abs((oldIdx - oldStart) - (newIdx - newStart));
      candidates.push({ total, offset, oldIdx, newIdx, similarity });
    }
  }

  // Сначала лучшие оценки; при равенстве — пара, сильнее сохраняющая порядок строк
  candidates.sort((left, right) => right.total - left.total || left.offset - right.offset);
  for (const candidate of candidates) {
    if (!alignment.oldUsed[candidate.oldIdx] && alignment.newToOld[candidate.newIdx] < 0) {
      alignment.link(candidate.oldIdx, candidate.newIdx, candidate.similarity);
    }
  }
}

/** Фаза 2: обойти все дыры между соседними якорями (якоря монотонны по обоим спискам). */
function matchGaps(ctx: MatchContext) {
  const { newToOld } = ctx.alignment;
  const oldCount = ctx.oldMetas.length;
  const newCount = ctx.newMetas.length;
  let oldStart = 0;
  let newStart = 0;

  for (let newIdx = 0; newIdx <= newCount; newIdx++) {
    const oldIdx = newIdx < newCount ? newToOld[newIdx] : oldCount;
    if (oldIdx < 0) continue;

    if (newIdx > newStart && oldIdx > oldStart) {
      matchGap(ctx, oldStart, oldIdx, newStart, newIdx);
    }
    oldStart = oldIdx + 1;
    newStart = newIdx + 1;
  }
}

/**
 * Фаза 3: перенесённые блоки. Среди оставшихся строк — то же наименование и ед.;
 * предпочтение строке сразу за предыдущей сопоставленной, иначе — единственной такой.
 */
function matchMovedRows(ctx: MatchContext) {
  const { alignment, oldMetas, newMetas } = ctx;
  const keyOf = (meta: RowMeta) => (meta.workName ? `${meta.workName}|${meta.unitCode}` : null);
  const oldKeys = oldMetas.map(keyOf);
  const remaining = new Map<string, number[]>();

  oldKeys.forEach((key, oldIdx) => {
    if (key === null || alignment.oldUsed[oldIdx]) return;
    const bucket = remaining.get(key) ?? [];
    bucket.push(oldIdx);
    remaining.set(key, bucket);
  });

  let lastOld = -2;
  for (let newIdx = 0; newIdx < newMetas.length; newIdx++) {
    if (alignment.newToOld[newIdx] >= 0) {
      lastOld = alignment.newToOld[newIdx];
      continue;
    }

    const key = keyOf(newMetas[newIdx]);
    const bucket = key === null ? undefined : remaining.get(key);
    if (!bucket) continue;

    const next = lastOld + 1;
    let pick = -1;
    if (next >= 0 && next < oldKeys.length && !alignment.oldUsed[next] && oldKeys[next] === key) {
      pick = next;
    } else {
      const free = bucket.filter(oldIdx => !alignment.oldUsed[oldIdx]);
      if (free.length === 1) pick = free[0];
    }

    if (pick >= 0) {
      alignment.link(pick, newIdx, 1);
      lastOld = pick;
    }
  }
}

/**
 * Найти лучшие совпадения для всех позиций
 *
 * @param oldPositions - позиции старой версии (доп. работы игнорируются)
 * @param newPositions - строки новой версии из Excel
 * @param threshold - оценка, начиная с которой пара считается точной ('auto')
 */
export function findBestMatches(
  oldPositions: ClientPosition[],
  newPositions: ParsedRow[],
  threshold: number = 80
): MatchResult[] {
  const activeOld = oldPositions.filter(position => !position.is_additional);
  const oldMetas = activeOld.map(buildMeta);
  const newMetas = newPositions.map(buildMeta);

  const ctx: MatchContext = {
    oldPositions: activeOld,
    newPositions,
    oldMetas,
    newMetas,
    useItemNo: isItemNoReliable(oldMetas, newMetas),
    alignment: new Alignment(activeOld.length, newPositions.length),
  };

  // Фаза 1: якоря по точному наименованию; пустое наименование якорем не бывает
  const anchors = alignByKey(
    oldMetas.map(meta => meta.workName || null),
    newMetas.map(meta => meta.workName || null)
  );
  anchors.forEach((oldIdx, newIdx) => {
    if (oldIdx >= 0) ctx.alignment.link(oldIdx, newIdx, 1);
  });

  matchGaps(ctx);
  matchMovedRows(ctx);

  const results: MatchResult[] = [];
  ctx.alignment.newToOld.forEach((oldIdx, newIdx) => {
    if (oldIdx < 0) return;

    const score = scorePair(ctx, oldIdx, newIdx, ctx.alignment.similarity[newIdx]);
    results.push({
      oldPositionId: activeOld[oldIdx].id,
      newPositionIndex: newIdx,
      score,
      matchType: isAutoMatchScore(score, threshold) ? 'auto' : 'low_confidence',
    });
  });

  return results;
}

/**
 * Получить не сопоставленные позиции старой версии (удаленные заказчиком)
 */
export function getUnmatchedOldPositions(
  oldPositions: ClientPosition[],
  matches: MatchResult[]
): ClientPosition[] {
  const matchedIds = new Set(matches.map(m => m.oldPositionId));

  return oldPositions.filter(pos =>
    !matchedIds.has(pos.id) &&
    !pos.is_additional
  );
}

/**
 * Получить индексы не сопоставленных позиций новой версии (новые позиции)
 */
export function getUnmatchedNewPositionIndices(
  newPositions: ParsedRow[],
  matches: MatchResult[]
): number[] {
  const matchedIndices = new Set(matches.map(m => m.newPositionIndex));

  return newPositions
    .map((_, idx) => idx)
    .filter(idx => !matchedIndices.has(idx));
}

/**
 * Статистика сопоставления
 */
export interface MatchingStatistics {
  totalOld: number;
  totalNew: number;
  autoMatched: number;
  lowConfidence: number;
  deleted: number;
  new: number;
  additionalWorks: number;
}

/**
 * Вычислить статистику сопоставления
 */
export function calculateMatchingStatistics(
  oldPositions: ClientPosition[],
  newPositions: ParsedRow[],
  matches: MatchResult[]
): MatchingStatistics {
  const autoMatched = matches.filter(m => m.matchType === 'auto').length;
  const lowConfidence = matches.filter(m => m.matchType === 'low_confidence').length;
  const matchedOldIds = new Set(matches.map(m => m.oldPositionId));
  const matchedNewIndices = new Set(matches.map(m => m.newPositionIndex));

  return {
    totalOld: oldPositions.filter(p => !p.is_additional).length,
    totalNew: newPositions.length,
    autoMatched,
    lowConfidence,
    deleted: oldPositions.filter(p => !p.is_additional && !matchedOldIds.has(p.id)).length,
    new: newPositions.filter((_, idx) => !matchedNewIndices.has(idx)).length,
    additionalWorks: oldPositions.filter(p => p.is_additional).length,
  };
}
