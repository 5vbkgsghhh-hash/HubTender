// Вид затрат строки расчёта («Вид работ | Детализация | Локализация» — те же
// статьи, что в 1С) → detail_cost_category_id HUBTender. Порядок:
//  1) ручные решения out/gs-cost-map.json (ключ — «Вид | Детализация | Локализация»;
//     значение — id либо {"default": id, "byName": [["шаблон", id], …]}, когда одна
//     статья расчёта расходится по нескольким видам затрат по наименованию строки);
//  2) итоговое сопоставление переноса из 1С (out/cost-map.auto.json + cost-map.json),
//     чтобы одна статья везде вела в один вид затрат (17.03 → БЛАГОУСТРОЙСТВО и т.п.);
//  3) автоподбор matchArticle (scripts/import-1c/costs.mjs).

import fs from 'node:fs';
import path from 'node:path';
import { matchArticle, normText } from '../import-1c/costs.mjs';

export const costKey = (group, detail, location) => [group, detail, location || 'Нет'].map(normText).join('|');

/** «Вид | Детализация | Локализация» → нормализованный ключ. */
const keyFromText = (text) => costKey(...String(text).split('|').map((x) => x.trim()));

const readJson = (file) => (fs.existsSync(file) ? JSON.parse(fs.readFileSync(file, 'utf8')) : null);

/** Итоговые решения переноса из 1С по тексту статьи (неоднозначные ключи отбрасываются). */
export const loadOneCMap = (dir1c) => {
  const auto = readJson(path.join(dir1c, 'cost-map.auto.json')) ?? [];
  const overrides = readJson(path.join(dir1c, 'cost-map.json')) ?? {};
  const map = new Map();
  const conflict = new Set();
  for (const a of auto) {
    const id = Object.prototype.hasOwnProperty.call(overrides, a.code) ? overrides[a.code] : a.detail_cost_category_id;
    if (!id || !a.subgroup) continue;
    const k = costKey(a.group, a.subgroup, a.location);
    if (map.has(k) && map.get(k) !== id) conflict.add(k);
    map.set(k, id);
  }
  for (const k of conflict) map.delete(k);
  return map;
};

/**
 * @param {{details: {id: string, name: string, location: string|null, categoryName: string}[],
 *   manual: Record<string, string|null>, oneC: Map<string, string>}} p
 */
export const makeCostOf = ({ details, manual, oneC }) => {
  const ids = new Set(details.map((d) => d.id));
  const manualMap = new Map();
  const splitMap = new Map();
  for (const [text, v] of Object.entries(manual ?? {})) {
    if (v && typeof v === 'object') {
      splitMap.set(keyFromText(text), { fallback: v.default ?? null, byName: (v.byName ?? []).map(([re, id]) => [new RegExp(re, 'iu'), id]) });
    } else manualMap.set(keyFromText(text), v);
  }
  const cache = new Map();
  return (group, detail, location, name) => {
    const key = costKey(group, detail, location);
    const text = `${group ?? ''} | ${detail ?? ''} | ${location || 'Нет'}`;
    const split = splitMap.get(key);
    if (split) {
      const id = split.byName.find(([re]) => re.test(name ?? ''))?.[1] ?? split.fallback;
      return { id: id && ids.has(id) ? id : null, how: 'вручную, по наименованию', key, text, candidates: [], split: true };
    }
    if (cache.has(key)) return cache.get(key);
    let res;
    if (manualMap.has(key)) res = { id: manualMap.get(key), how: 'вручную', key, text, candidates: [] };
    else if (oneC.has(key) && ids.has(oneC.get(key))) res = { id: oneC.get(key), how: 'как в 1С', key, text, candidates: [] };
    else {
      const m = matchArticle({ group: group ?? '', subgroup: detail ?? '', location: location || 'Нет' }, details);
      res = { id: m.id, how: m.how, key, text, candidates: m.candidates };
    }
    if (res.id && !ids.has(res.id)) res = { ...res, id: null, how: 'id нет на портале' };
    cache.set(key, res);
    return res;
  };
};
