/**
 * Выравнивание двух упорядоченных списков по точному ключу с сохранением порядка (как diff).
 *
 * Строки ВОР между версиями тендера почти всегда идут в том же порядке: заказчик вставляет,
 * удаляет и правит строки, но не перемешивает их. Поэтому одинаковые наименования
 * («Пусконаладочные работы», «Фиксированная часть…») различает их место в документе,
 * а не номер раздела — его заказчик может перенумеровать целиком.
 *
 * Алгоритм (patience diff):
 * 1. Общий префикс и суффикс диапазона сопоставляются сразу.
 * 2. Ключи, уникальные и в старом, и в новом диапазоне, — кандидаты в якоря; из них берётся
 *    наибольшая возрастающая по старому индексу подпоследовательность (LIS), чтобы якоря
 *    не пересекались.
 * 3. «Дыры» между якорями обрабатываются так же (там ключ может оказаться уникальным).
 * 4. Дыра без уникальных ключей выравнивается классическим LCS, если она небольшая.
 */

/** Потолок клеток таблицы LCS на одну дыру (~8 МБ); более крупные дыры остаются невыровненными. */
const LCS_CELL_LIMIT = 4_000_000;

type Range = [oldStart: number, oldEnd: number, newStart: number, newEnd: number];

/**
 * Наибольшая возрастающая по старому индексу подпоследовательность пар [old, new]
 * (пары уже упорядочены по new). Patience sorting, O(k log k).
 */
function longestIncreasingByOld(pairs: Array<[number, number]>): Array<[number, number]> {
  const tailValues: number[] = [];
  const tailIndexes: number[] = [];
  const previous = new Int32Array(pairs.length).fill(-1);

  for (let k = 0; k < pairs.length; k++) {
    const value = pairs[k][0];
    let lo = 0;
    let hi = tailValues.length;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      if (tailValues[mid] < value) lo = mid + 1;
      else hi = mid;
    }
    tailValues[lo] = value;
    tailIndexes[lo] = k;
    previous[k] = lo > 0 ? tailIndexes[lo - 1] : -1;
  }

  const result: Array<[number, number]> = [];
  let k = tailIndexes.length > 0 ? tailIndexes[tailIndexes.length - 1] : -1;
  while (k >= 0) {
    result.push(pairs[k]);
    k = previous[k];
  }
  return result.reverse();
}

/** Якоря диапазона: ключи, встречающиеся ровно один раз и в старой, и в новой части. */
function findUniqueAnchors(
  oldKeys: ReadonlyArray<string | null>,
  newKeys: ReadonlyArray<string | null>,
  [oldStart, oldEnd, newStart, newEnd]: Range
): Array<[number, number]> {
  const oldCount = new Map<string, number>();
  const oldIndex = new Map<string, number>();
  for (let i = oldStart; i < oldEnd; i++) {
    const key = oldKeys[i];
    if (key === null) continue;
    oldCount.set(key, (oldCount.get(key) ?? 0) + 1);
    oldIndex.set(key, i);
  }

  const newCount = new Map<string, number>();
  for (let j = newStart; j < newEnd; j++) {
    const key = newKeys[j];
    if (key === null) continue;
    newCount.set(key, (newCount.get(key) ?? 0) + 1);
  }

  const candidates: Array<[number, number]> = [];
  for (let j = newStart; j < newEnd; j++) {
    const key = newKeys[j];
    if (key !== null && newCount.get(key) === 1 && oldCount.get(key) === 1) {
      candidates.push([oldIndex.get(key)!, j]);
    }
  }

  return longestIncreasingByOld(candidates);
}

/** Классический LCS внутри небольшой дыры. */
function alignRangeByLcs(
  oldKeys: ReadonlyArray<string | null>,
  newKeys: ReadonlyArray<string | null>,
  [oldStart, oldEnd, newStart, newEnd]: Range,
  newToOld: Int32Array
): void {
  const rows = oldEnd - oldStart;
  const cols = newEnd - newStart;
  if (rows * cols > LCS_CELL_LIMIT) {
    return;
  }

  const same = (i: number, j: number) => {
    const key = oldKeys[oldStart + i];
    return key !== null && key === newKeys[newStart + j];
  };

  // Длина LCS не превышает min(rows, cols) ≤ 2000 при лимите клеток — Uint16 хватает.
  const width = cols + 1;
  const table = new Uint16Array((rows + 1) * width);
  for (let i = rows - 1; i >= 0; i--) {
    for (let j = cols - 1; j >= 0; j--) {
      table[i * width + j] = same(i, j)
        ? table[(i + 1) * width + j + 1] + 1
        : Math.max(table[(i + 1) * width + j], table[i * width + j + 1]);
    }
  }

  let i = 0;
  let j = 0;
  while (i < rows && j < cols) {
    if (same(i, j)) {
      newToOld[newStart + j] = oldStart + i;
      i++;
      j++;
    } else if (table[(i + 1) * width + j] >= table[i * width + j + 1]) {
      i++;
    } else {
      j++;
    }
  }
}

/**
 * Выровнять списки ключей. `null` — строка без ключа, ни с чем не совпадает.
 *
 * @returns для каждой новой строки индекс старой строки или -1
 */
export function alignByKey(
  oldKeys: ReadonlyArray<string | null>,
  newKeys: ReadonlyArray<string | null>
): Int32Array {
  const newToOld = new Int32Array(newKeys.length).fill(-1);
  const same = (i: number, j: number) => oldKeys[i] !== null && oldKeys[i] === newKeys[j];

  // Явный стек вместо рекурсии: глубина вложенности дыр не упирается в стек вызовов.
  const stack: Range[] = [[0, oldKeys.length, 0, newKeys.length]];

  while (stack.length > 0) {
    let [oldStart, oldEnd, newStart, newEnd] = stack.pop()!;

    while (oldStart < oldEnd && newStart < newEnd && same(oldStart, newStart)) {
      newToOld[newStart++] = oldStart++;
    }
    while (oldStart < oldEnd && newStart < newEnd && same(oldEnd - 1, newEnd - 1)) {
      newToOld[--newEnd] = --oldEnd;
    }
    if (oldStart >= oldEnd || newStart >= newEnd) {
      continue;
    }

    const range: Range = [oldStart, oldEnd, newStart, newEnd];
    const anchors = findUniqueAnchors(oldKeys, newKeys, range);

    if (anchors.length === 0) {
      alignRangeByLcs(oldKeys, newKeys, range, newToOld);
      continue;
    }

    let prevOld = oldStart;
    let prevNew = newStart;
    for (const [oldIdx, newIdx] of anchors) {
      newToOld[newIdx] = oldIdx;
      stack.push([prevOld, oldIdx, prevNew, newIdx]);
      prevOld = oldIdx + 1;
      prevNew = newIdx + 1;
    }
    stack.push([prevOld, oldEnd, prevNew, newEnd]);
  }

  return newToOld;
}
