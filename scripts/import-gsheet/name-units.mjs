// Единица строки расчёта, у которой «Ед. изм» пуста, — по наименованию:
//  1) решение из out/gs-units-map.json, раздел byName ({"material|Наименование": "шт"});
//  2) справочник наименований портала: то же наименование (каноническая форма) того же
//     вида с единственной единицей;
//  3) единица в хвосте наименования: «Анкер забивной М12 (цанга) (шт)».

import { canonKey } from '../import-1c/names.mjs';

const UNIT_TAIL = /\(\s*(шт|м|м2|м3|кг|т|тн|л|компл|м\.?п\.?|пог\.?\s?м|упак|лист)\.?\s*\)\s*$/iu;

/**
 * @param {{works?: {name: string, unit: string}[], materials?: {name: string, unit: string}[],
 *   manual?: Record<string, string>, unitOf: (text: string) => string|null}} p
 *   unitOf — текст единицы → код справочника (или null)
 * @returns {(kind: 'work'|'material', name: string) => {code: string|null, how: string}|null}
 */
export const makeNameUnitOf = ({ works = [], materials = [], manual = {}, unitOf }) => {
  const catalog = new Map();
  for (const [kind, list] of [['work', works], ['material', materials]]) {
    for (const n of list) {
      if (!n.unit) continue;
      const k = canonKey(kind, n.name, '');
      if (!catalog.has(k)) catalog.set(k, new Set());
      catalog.get(k).add(n.unit);
    }
  }
  const decided = new Map(Object.entries(manual).map(([text, code]) => {
    const i = text.indexOf('|');
    return [canonKey(text.slice(0, i).trim(), text.slice(i + 1), ''), code];
  }));
  return (kind, name) => {
    const k = canonKey(kind, name, '');
    if (decided.has(k)) return { code: decided.get(k), how: 'по смыслу (gs-units-map.json)' };
    const units = catalog.get(k);
    if (units?.size === 1) return { code: [...units][0], how: 'справочник портала' };
    const tail = UNIT_TAIL.exec(name);
    const code = tail ? unitOf(tail[1]) : null;
    if (code) return { code, how: 'из наименования' };
    return units ? { code: null, how: `в справочнике несколько единиц: ${[...units].join(', ')}` } : null;
  };
};
