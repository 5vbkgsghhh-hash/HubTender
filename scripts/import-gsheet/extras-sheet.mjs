// Шапка расчёта (поля тендера), лист «Анализ» (проценты наценок) и лист
// «Затраты на строительство» (количества и комментарии по видам затрат).

import path from 'node:path';
import { sheetAccess, findLabel, normLabel, colName } from './sheet.mjs';
import { parseTenderName, parenthetical } from '../import-1c/tenders.mjs';
import { FIXED_VALUES, GROUP_KEYS } from '../import-1c/extras-data.mjs';

/**
 * «213. Мосфильмовская 31А (Октябрь Групп).xlsx» + шапка «Расчета» + площади «Анализа».
 * opts.extras — книга с листом «Анализ», если он в отдельном файле; number/title/client —
 * вместо выведенных из имени файла.
 */
export const parseHeader = (wb, file, { extras = wb, number, title, client } = {}) => {
  const base = path.basename(file).replace(/\.xlsx$/i, '');
  const parsed = parseTenderName(base);
  if (!parsed && !(number && title)) throw new Error(`имя файла «${base}»: в начале нет номера тендера («213. Название (Заказчик).xlsx»)`);
  const calc = sheetAccess(wb, 'Расчет');
  const an = sheetAccess(extras, 'Анализ');
  const at = (s, re, to) => {
    const hit = findLabel(s, re, { to });
    if (!hit?.valueCol) return null;
    return { num: s.num(hit.valueCol, hit.row), text: s.text(hit.valueCol, hit.row), link: s.link(hit.valueCol, hit.row) };
  };
  const positive = (x) => (x?.num && x.num > 0 ? x.num : null);
  const link = (re) => {
    const x = at(calc, re, 10);
    return x?.link ?? (x?.text && /^https?:\/\//i.test(x.text) ? x.text : null);
  };
  return {
    number: number ?? parsed.baseNumber,
    title: title ?? parsed.title,
    client: client ?? parenthetical(title ?? parsed.title),
    rates: { USD: positive(at(calc, /^курс доллара/, 6)), EUR: positive(at(calc, /^курс евро/, 6)), CNY: positive(at(calc, /^курс юаня/, 6)) },
    fields: {
      area_sp: positive(at(an, /^площадь.*по сп/, 30)),
      area_client: positive(at(an, /^площадь.*заказчик/, 30)),
      qa_form_link: link(/вопрос/),
      upload_folder: link(/папк.*кп/),
      bsm_link: link(/бсм/),
      tz_link: link(/уточнени.*тз/),
    },
  };
};

/** Подписи «Анализа» → ключи параметров наценок HUBTender. */
export const MARKUP_LABELS = [
  [/^работы$/, 'works_16_markup'],
  [/^служба механизации/, 'mechanization_service'],
  [/^мбп/, 'mbp_gsm'],
  [/^гарантийный период/, 'warranty_period'],
  [/^рост стоимости работ субподряда/, 'subcontract_works_cost_growth'],
  [/^рост стоимости материалов субподряда/, 'subcontract_materials_cost_growth'],
  [/^рост стоимости работ$/, 'works_cost_growth'],
  [/^рост стоимости материалов$/, 'material_cost_growth'],
  [/^непредвиденные/, 'contingency_costs'],
  [/^ооз субподряд/, 'overhead_subcontract'],
  [/^ооз$/, 'overhead_own_forces'],
  [/^офз/, 'general_costs_without_subcontract'],
  [/^прибыль$/, 'profit_own_forces'],
  [/^коэф\.? генподряда на субподряд/, 'profit_subcontract'],
  [/^прибыль субподряд/, 'profit_subcontract'],
];

/** В «Анализе» нет показателя → значение (как в переносе из 1С). */
const MISSING = { subcontract_works_cost_growth: 0, subcontract_materials_cost_growth: 0 };

/** Проценты «Анализа»: { found: {key: %}, unknown: [подписи с числом без соответствия] }. */
export const parseMarkups = (wb) => {
  const s = sheetAccess(wb, 'Анализ');
  const found = {};
  const unknown = [];
  for (let r = 1; r <= s.lastRow; r += 1) {
    let c = 0;
    while (c <= s.lastCol && !s.text(colName(c), r)) c += 1;
    const label = normLabel(s.text(colName(c), r));
    if (!label || /^площадь/.test(label)) continue;
    let v = c + 1;
    while (v <= s.lastCol && s.num(colName(v), r) === null) v += 1;
    if (v > s.lastCol) continue;
    const value = s.num(colName(v), r);
    const isPct = /%/.test(s.format(colName(v), r) ?? '') || /%\s*$/.test(s.shown(colName(v), r) ?? '');
    const pct = isPct ? value * 100 : value;
    const hit = MARKUP_LABELS.find(([re]) => re.test(label));
    if (hit) found[hit[1]] = Math.round(pct * 1e6) / 1e6;
    else unknown.push(`${s.text(colName(c), r)} = ${pct}`);
  }
  return { found, unknown };
};

/** Значения всех активных параметров портала; notes — что взято по умолчанию. */
export const buildMarkupValues = (params, found) => {
  const values = {};
  const notes = [];
  for (const p of params) {
    if (Object.prototype.hasOwnProperty.call(FIXED_VALUES, p.key)) values[p.key] = FIXED_VALUES[p.key];
    else if (Object.prototype.hasOwnProperty.call(found, p.key)) values[p.key] = found[p.key];
    else if (Object.prototype.hasOwnProperty.call(MISSING, p.key)) values[p.key] = MISSING[p.key];
    else {
      values[p.key] = Number(p.default_value ?? 0);
      notes.push(`${p.key}: нет в «Анализе» — по умолчанию ${values[p.key]}`);
    }
  }
  return { values, notes };
};

/** Номер группы листа затрат → код группы 1С (ключи групп — GROUP_KEYS из extras-data.mjs). */
export const GROUP_CODE_BY_NUMBER = {
  1: '000000217', 1.1: '000000218', 2: '000000219', 3: '000000220', 4: '000000221', 5: '000000222', 6: '000000223',
  7: '000000224', 8: '000000225', 9: '000000226', 10: '000000227', 11: '000000228', 12: '000000229',
  12.1: '000000230', 12.2: '000000231', 12.3: '000000232', 12.4: '000000233', 12.5: '000000234', 12.6: '000000235',
  12.7: '000000236', 12.8: '000000237', 12.9: '000000238', 13: '000000239', 13.1: '000000241', 13.2: '000000242',
  13.3: '000000243', 14: '000000240', 14.1: '000000244', 14.2: '000000245', 14.3: '000000246', 15: '000000247',
  16: '000000248', 17: '000000249', 18: '000000250', 18.1: '000000251', 18.2: '000000252', 19: '000000253',
};

/**
 * Группы листа без колонки «Код»: название → [код группы 1С, подгруппы]. Название
 * сначала ищется среди подгрупп текущей группы («Автостоянка» есть и в отделке, и в
 * дверях; «Отделочные работы» — и группа, и подгруппа отделки квартир), по началу строки.
 */
const GROUPS_BY_NAME = [
  ['подготовительные работы', '000000217', [['организация строительной площадки', '000000218']]],
  ['земляные работы', '000000219'], ['водоотведение и водопонижение', '000000220'], ['устройство котлована', '000000221'],
  ['гидроизоляционные работы', '000000222'], ['устройство виброзащиты', '000000223'], ['монолитные работы', '000000224'],
  ['металлические конструкции', '000000225'], ['кладочные работы', '000000226'], ['кровля', '000000227'], ['фасадные работы', '000000228'],
  ['отделочные работы', '000000229', [['автостоянка', '000000230'], ['технические помещения', '000000231'], ['кладовки', '000000232'],
    ['мопы-подземная', '000000233'], ['техпространство', '000000234'], ['1-й этаж', '000000235'],
    ['мопы-надземная часть (лестницы)', '000000237'], ['мопы-надземная', '000000236']]],
  ['мокап', '000000238'],
  ['двери, люки, ворота', '000000239', [['автостоянка', '000000241'], ['мопы', '000000242'], ['1-й этаж', '000000243']]],
  ['внутренние инженерные системы', '000000240', [['механические инженерные системы', '000000244'],
    ['электрические системы', '000000245'], ['слаботочные системы', '000000246']]],
  ['технология', '000000247'], ['наружные сети', '000000248'], ['благоустройство', '000000249'],
  ['отделка квартир', '000000250', [['отделочные работы', '000000251'], ['разводка вис', '000000252']]],
  ['разработка', '000000253'],
];

/** Код группы по названию; top — текущая группа верхнего уровня (её подгруппы — в приоритете). */
const groupByName = (name, top) => {
  const n = normLabel(name);
  const sub = (top?.[2] ?? []).find(([prefix]) => n.startsWith(prefix));
  if (sub) return { code: sub[1], top };
  const hit = GROUPS_BY_NAME.find(([prefix]) => n.startsWith(prefix));
  return hit ? { code: hit[1], top: hit } : { code: null, top };
};

/** Локализации листа затрат → локации портала (по началу строки; порядок важен). */
const PORTAL_LOCATIONS = [
  ['автостоянка', 'Автостоянка'], ['технические помещения', 'Технические помещения'], ['кладовки', 'Кладовки Помещения не БКТ'],
  ['мопы-подземная', 'МОП ПЧ'], ['техпространство', 'Техпространство'], ['1-й этаж', 'Лобби'],
  ['мопы-надземная часть (лестницы)', 'ЛК'], ['мопы-надземная', 'МОП НЧ'],
];
const VIS_GROUPS = ['механические инженерные системы', 'электрические системы', 'слаботочные системы'];

/**
 * Группа листа «вид работ + локализация» (тендер 214) → group_key портала. Ключи — как на
 * странице «Затраты на строительство» (buildCostRows.ts): у категории несколько локаций —
 * `location-<категория>-<локация>`, иначе `category-<категория>`. ВИС по локализации:
 * «Здание» → «ВИС / …», «Наружные сети» → «НАРУЖНИЕ ВИС / …», «Квартира» → отделка квартир.
 * @param {{categoryName: string, location: string|null}[]} details виды затрат портала
 */
export const makeGroupKeyOf = (details) => {
  const locations = new Map();
  for (const d of details) {
    if (!locations.has(d.categoryName)) locations.set(d.categoryName, new Set());
    if (d.location) locations.get(d.categoryName).add(d.location);
  }
  const byName = new Map([...locations.keys()].map((c) => [normLabel(c), c]));
  const flats = [...locations.keys()].find((c) => normLabel(c).startsWith('отделка квартир')) ?? null;
  return (group, location) => {
    const g = normLabel(group);
    const l = normLabel(location);
    let category = byName.get(g) ?? null;
    if (VIS_GROUPS.some((v) => g.startsWith(v))) {
      category = l === 'квартира' ? flats : byName.get(`${l === 'наружные сети' ? 'наружние вис' : 'вис'} / ${g}`) ?? null;
    } else if (g === 'отделочные работы' && l === 'квартира') category = flats;
    if (!category) return null;
    const set = locations.get(category);
    if (set.size <= 1) return `category-${category}`;
    const portal = PORTAL_LOCATIONS.find(([prefix]) => l.startsWith(prefix))?.[1];
    return portal && set.has(portal) ? `location-${category}-${portal}` : null;
  };
};

/**
 * Строки «Затрат на строительство» → строки construction_cost_volumes.
 * Группа — строка без детализации: ключ по номеру, по названию (колонки «Код» нет) или
 * по «виду работ + локализации» (opts.groupKeyOf); детальная — сопоставление тройки
 * «Вид работ | Детализация | Локализация» (costOf). Пропускаем строки без количества и
 * примечания. Несколько строк в одну строку портала складываются, но один и тот же объём
 * (площадь объекта, повторённая в десятке строк) — один раз; у групп объёмы не складываются.
 */
export const parseVolumes = (wb, tenderId, costOf, { groupKeyOf } = {}) => {
  const s = sheetAccess(wb, 'Затраты на строительство');
  const first = (r) => normLabel(s.text('A', r));
  let header = 1;
  while (header <= Math.min(10, s.lastRow) && first(header) !== 'код' && first(header) !== 'вид работ') header += 1;
  if (header > 10) throw new Error('лист «Затраты на строительство»: не найдена строка заголовков («Код» или «Вид работ»)');
  // С колонкой «Код» тройка статьи начинается с B и количество в G; без неё — с A и в F.
  const col = first(header) === 'код'
    ? { code: 'A', group: 'B', detail: 'C', location: 'D', note: 'E', qty: 'G' }
    : { code: null, group: 'A', detail: 'B', location: 'C', note: 'D', qty: 'F' };
  /** Объём; число текстом («18016,00») — тоже число. */
  const numOf = (c, r) => {
    const n = s.num(c, r);
    if (n !== null) return n;
    const t = (s.text(c, r) ?? '').replace(/\s/g, '').replace(',', '.');
    return /^-?\d+(\.\d+)?$/.test(t) ? Number(t) : 0;
  };
  const rows = [];
  let top = null;
  for (let r = header + 1; r <= s.lastRow; r += 1) {
    const a = col.code ? s.text(col.code, r) : null;
    const [b, c, d] = [col.group, col.detail, col.location].map((x) => s.text(x, r));
    if (!b && !c) continue;
    const row = { qty: numOf(col.qty, r), note: s.text(col.note, r) };
    if (c) {
      rows.push({ ...row, name: `${a ?? ''} ${b} | ${c} | ${d ?? ''}`.trim(), detail: [b, c, d] });
    } else if (col.code) {
      const number = (a ?? '').replace(/\.$/, '');
      rows.push({ ...row, name: `${a ?? ''} ${b}`.trim(), group: GROUP_KEYS[GROUP_CODE_BY_NUMBER[number]] ?? null });
    } else {
      const plain = !d || normLabel(d) === 'нет';
      let group = d && groupKeyOf ? groupKeyOf(b, d) : null;
      if (!group && plain) {
        const hit = groupByName(b, top);
        top = hit.top;
        group = GROUP_KEYS[hit.code] ?? null;
      }
      rows.push({ ...row, name: plain ? b : `${b} | ${d}`, group });
    }
  }
  const targets = new Map();
  const skipped = [];
  for (const r of rows) {
    if (!r.qty && !r.note) continue;
    const id = r.detail ? costOf(...r.detail).id : null;
    const target = r.detail ? (id ? { detail_cost_category_id: id } : null) : (r.group ? { group_key: r.group } : null);
    if (!target) {
      skipped.push({ code: r.detail ? JSON.stringify(r.detail) : r.name, name: r.name, qty: r.qty, note: r.note });
      continue;
    }
    const key = target.group_key ?? target.detail_cost_category_id;
    const prev = targets.get(key);
    if (!prev) {
      targets.set(key, { tender_id: tenderId, detail_cost_category_id: null, group_key: null, ...target, volume: r.qty, notes: r.note, seen: [r.qty] });
      continue;
    }
    if (r.note && !String(prev.notes ?? '').includes(r.note)) prev.notes = [prev.notes, r.note].filter(Boolean).join(' | ');
    if (!r.qty || prev.seen.includes(r.qty)) continue;
    if (target.group_key) {
      skipped.push({ code: key, name: `${r.name} — у группы портала уже есть другой объём`, qty: r.qty, note: null });
      continue;
    }
    prev.volume += r.qty;
    prev.seen.push(r.qty);
    prev.merged = (prev.merged ?? 1) + 1;
  }
  return { rows: [...targets.values()].map(({ seen, ...row }) => row), skipped };
};
