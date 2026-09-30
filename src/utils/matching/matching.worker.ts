/**
 * Web Worker для сопоставления позиций версий тендера.
 *
 * Выносит тяжёлый `findBestMatches` (выравнивание и расстояние Левенштейна по сотням/тысячам
 * позиций) с главного потока, чтобы UI модалки не замирал во время расчёта.
 *
 * Логика сопоставления не меняется — воркер лишь оборачивает `findBestMatches`.
 * Один воркер, без параллелизации: фазы сопоставления идут последовательно и опираются
 * на результат предыдущей, параллельный прогон изменил бы результат.
 */

import { findBestMatches, type MatchResult } from './findBestMatches';
import type { ParsedRow } from './calculateMatchScore';
import type { ClientPosition } from '../../lib/types';

export interface MatchWorkerRequest {
  oldPositions: ClientPosition[];
  newPositions: ParsedRow[];
  threshold?: number;
}

export interface MatchWorkerResponse {
  matches: MatchResult[];
}

// lib WebWorker не подключён в tsconfig — приводим self к минимальному контракту воркера.
const ctx = self as unknown as {
  onmessage: ((ev: MessageEvent<MatchWorkerRequest>) => void) | null;
  postMessage: (message: MatchWorkerResponse) => void;
};

ctx.onmessage = (ev: MessageEvent<MatchWorkerRequest>) => {
  const { oldPositions, newPositions, threshold } = ev.data;
  const matches = findBestMatches(oldPositions, newPositions, threshold);
  ctx.postMessage({ matches });
};
