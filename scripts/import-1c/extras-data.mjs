// Часть 2 переноса из 1С: разбор четырёх CSV (наценки, рост субподряда, ссылки,
// количества в «Затратах на строительство») и подготовка данных для API.
// Правила согласованы с пользователем (план replicated-foraging-naur.md, 07.10).

import fs from 'node:fs';
import { splitCsvLine, readVersions, num, str } from './csv.mjs';
import { boqItemType } from './model.mjs';
import { parseCostArticle, matchArticle } from './costs.mjs';

/** Небольшой CSV целиком: массив объектов по заголовку. */
export const readCsv = (file) => {
  const lines = fs.readFileSync(file, 'utf8').replace(/^﻿/, '').split(/\r?\n/).filter(Boolean);
  const header = splitCsvLine(lines[0]);
  return lines.slice(1).map((l) => {
    const cells = splitCsvLine(l);
    return Object.fromEntries(header.map((h, i) => [h, cells[i]]));
  });
};

/** «130251,14» / «130251.140» / NULL → число или null. */
export const numComma = (v) => (str(v) === null ? null : num(String(v).replace(',', '.')));

export const versionKey = (tenderName, versionName) => `${tenderName}|${String(versionName ?? '').trim()}`;

/**
 * (тендер 1С, версия) → { number, version, id } по инвентаризации загрузки и манифесту.
 * @param {{tenders: {name: string, number: string, versions: {name: string, version: number}[]}[]}} inv
 */
export const buildVersionIndex = (inv, manifest) => {
  const index = new Map();
  for (const t of inv.tenders) {
    for (const v of t.versions) {
      const entry = manifest.tenders[t.number]?.versions?.[v.version];
      if (entry?.status !== 'loaded') continue;
      index.set(versionKey(t.name, v.name), { tenderName: t.name, number: t.number, version: v.version, id: entry.id });
    }
  }
  return index;
};

// ─── Наценки ─────────────────────────────────────────────────────────────────

/** Показатели 1С с процентом → ключ параметра HUBTender. */
export const PERCENT_KEYS = {
  '000000004': 'works_16_markup',
  '000000012': 'works_cost_growth',
  '000000013': 'material_cost_growth',
  '000000014': 'subcontract_works_cost_growth',
  '000000015': 'subcontract_materials_cost_growth',
  '000000016': 'contingency_costs',
  '000000018': 'overhead_own_forces',
  '000000019': 'overhead_subcontract',
  '000000020': 'general_costs_without_subcontract',
  '000000022': 'profit_own_forces',
  '000000023': 'profit_subcontract',
};

/** Показатели 1С в ₽/м² (второе значение) → ключ; в HUBTender это % от собственных работ. */
export const PER_M2_KEYS = {
  '000000009': 'mechanization_service',
  '000000010': 'mbp_gsm',
  '000000011': 'warranty_period',
};

/** В 1С нет показателя → значение (решение пользователя: рост материалов субподряда 0). */
const MISSING_IN_1C = { subcontract_materials_cost_growth: 0 };

/** Множитель к ₽/м²: гарантия — за 5 лет (так заведён 297, решение пользователя 07.10). */
export const PER_M2_FACTOR = { warranty_period: 5 };

/** Параметры без аналога в 1С: НДС 20 % — тендеры 2025 года, как на 297. */
export const FIXED_VALUES = { nds_22: 20 };

/** Наценки по версиям: key → { area, byCode: { code: { percent, second } } }. */
export const readMarkups = (file) => {
  const out = new Map();
  for (const r of readCsv(file)) {
    const k = versionKey(r.tender_name, r.version_name);
    if (!out.has(k)) out.set(k, { area: numComma(r.object_area), byCode: {} });
    out.get(k).byCode[r.indicator_code] = { percent: numComma(r.percent_value) ?? 0, second: numComma(r.second_value) ?? 0 };
  }
  return out;
};

/** ₽/м² → % от прямых собственных работ версии (3 знака); без работ — 0. */
export const perM2ToPercent = (rubPerM2, area, worksSum) => {
  if (!rubPerM2 || !area || !worksSum || worksSum <= 0) return 0;
  return Math.round((rubPerM2 * area / worksSum) * 100 * 1000) / 1000;
};

/**
 * Источник наценок версии: свои данные, иначе ближайшая версия того же тендера
 * (при равенстве — младшая), иначе null (значения по умолчанию).
 * @param {{number: string, version: number}} target
 * @param {Map<string, {number: string, version: number}>} index
 */
export const pickMarkupSource = (targetKey, target, index, markups) => {
  if (markups.has(targetKey)) return { key: targetKey, how: 'своя версия' };
  const siblings = [...index.entries()]
    .filter(([k, v]) => v.number === target.number && k !== targetKey && markups.has(k))
    .sort(([, a], [, b]) => Math.abs(a.version - target.version) - Math.abs(b.version - target.version) || a.version - b.version);
  if (siblings.length) return { key: siblings[0][0], how: `из версии ${siblings[0][1].version}` };
  return null;
};

/**
 * Записи процентов для всех активных параметров портала.
 * @param {{id: string, key: string, default_value: number}[]} params
 * @param {{area: number, byCode: object}|null} source данные 1С (своя или соседняя версия)
 * @param {number} area площадь этой версии; worksSum — Σ «раб» этой версии на портале
 */
export const buildMarkupValues = (params, source, area, worksSum) => {
  const values = {};
  const notes = [];
  for (const p of params) {
    if (Object.prototype.hasOwnProperty.call(FIXED_VALUES, p.key)) {
      values[p.key] = FIXED_VALUES[p.key];
      continue;
    }
    if (!source) {
      values[p.key] = Number(p.default_value ?? 0);
      continue;
    }
    const pctCode = Object.keys(PERCENT_KEYS).find((c) => PERCENT_KEYS[c] === p.key);
    const m2Code = Object.keys(PER_M2_KEYS).find((c) => PER_M2_KEYS[c] === p.key);
    if (pctCode) {
      const v = source.byCode[pctCode];
      values[p.key] = v ? v.percent : (MISSING_IN_1C[p.key] ?? Number(p.default_value ?? 0));
      if (!v && !(p.key in MISSING_IN_1C)) notes.push(`${p.key}: нет в 1С — по умолчанию`);
    } else if (m2Code) {
      const v = source.byCode[m2Code];
      values[p.key] = perM2ToPercent((v?.second ?? 0) * (PER_M2_FACTOR[p.key] ?? 1), area ?? source.area, worksSum);
    } else {
      values[p.key] = Number(p.default_value ?? 0); // прочие — как при создании тендера в UI
    }
  }
  return { values, notes };
};

// ─── Рост субподряда ─────────────────────────────────────────────────────────

/** Файл зафиксированных затрат: key → Set(код статьи 1С). */
export const readFixedCosts = (file) => {
  const out = new Map();
  for (const r of readCsv(file)) {
    const k = versionKey(r.tender_name, r.version_name);
    if (!out.has(k)) out.set(k, new Map());
    if (str(r.growth_enabled)?.toLowerCase() === 'да') out.get(k).set(r.cost_code, r.cost_name);
  }
  return out;
};

/**
 * Флаги «интервала захода на площадку» из Результаты.csv: по версии и коду
 * статьи — сумма субподрядных работ всего и в строках с visit_interval = 1.
 * Плюс счётчик «1» у собственных работ (на портале не переносится).
 */
export const collectVisitFlags = async (csvPath, keep = () => true) => {
  const flags = new Map();
  const ownFlagged = new Map();
  const rabSum = new Map();
  for await (const batch of readVersions(csvPath)) {
    const k = versionKey(batch.tenderName, batch.versionName);
    if (!keep(k)) continue;
    const byCode = new Map();
    let rab = 0;
    for (const r of batch.rows) {
      if (r.row_type !== 'Работа') continue;
      const flagged = str(r.visit_interval) === '1';
      const type = boqItemType(r);
      if (type === 'раб') rab += num(r.amount) ?? 0;
      if (type !== 'суб-раб') {
        if (flagged) {
          const o = ownFlagged.get(k) ?? { rows: 0, amount: 0 };
          o.rows += 1;
          o.amount += num(r.amount) ?? 0;
          ownFlagged.set(k, o);
        }
        continue;
      }
      const code = (str(r.cost_category) ?? '').split(/\s+/)[0] || null;
      const raw = str(r.cost_category);
      const e = byCode.get(code) ?? { raw, total: 0, flagged: 0, rows: 0, flaggedRows: 0 };
      const amount = Math.abs(num(r.amount) ?? 0);
      e.total += amount;
      e.rows += 1;
      if (flagged) {
        e.flagged += amount;
        e.flaggedRows += 1;
      }
      byCode.set(code, e);
    }
    flags.set(k, byCode);
    rabSum.set(k, rab);
  }
  return { flags, ownFlagged, rabSum };
};

/**
 * Виды затрат с ростом по флагам: агрегируем статьи 1С по виду затрат портала;
 * рост включён, если больше половины суммы суб-раб — в строках с «1»
 * (при нулевых суммах — по числу строк).
 * @param {Map<string, {raw, total, flagged, rows, flaggedRows}>} byCode
 * @param {(code: string, raw: string) => string|null} resolve
 */
export const growthByMajority = (byCode, resolve) => {
  const byDetail = new Map();
  for (const [code, e] of byCode) {
    if (!e.flaggedRows) continue;
    const id = resolve(code, e.raw);
    if (!id) continue;
    const agg = byDetail.get(id) ?? { total: 0, flagged: 0, rows: 0, flaggedRows: 0 };
    agg.total += e.total;
    agg.flagged += e.flagged;
    agg.rows += e.rows;
    agg.flaggedRows += e.flaggedRows;
    byDetail.set(id, agg);
  }
  // Сумму по виду затрат считаем по всем статьям, попавшим в этот вид (и без «1»).
  for (const [code, e] of byCode) {
    if (e.flaggedRows) continue;
    const id = resolve(code, e.raw);
    if (id && byDetail.has(id)) {
      const agg = byDetail.get(id);
      agg.total += e.total;
      agg.rows += e.rows;
    }
  }
  const enabled = new Set();
  for (const [id, a] of byDetail) {
    const on = a.total > 0 ? a.flagged > a.total / 2 : a.flaggedRows > a.rows / 2;
    if (on) enabled.add(id);
  }
  return { enabled, byDetail };
};

/** Исключения: все виды затрат минус набор с ростом, по каждому типу. */
export const exclusionRows = (tenderId, allDetailIds, enabledWorks, enabledMaterials) => [
  ...allDetailIds.filter((id) => !enabledWorks.has(id))
    .map((id) => ({ tender_id: tenderId, detail_cost_category_id: id, exclusion_type: 'works' })),
  ...allDetailIds.filter((id) => !enabledMaterials.has(id))
    .map((id) => ({ tender_id: tenderId, detail_cost_category_id: id, exclusion_type: 'materials' })),
];

// ─── Статьи 1С → виды затрат портала ─────────────────────────────────────────

/**
 * Резолвер «код статьи 1С → detail_cost_category_id»: ручные решения
 * (out/cost-map.json) → сопоставление, с которым грузились строки (known) →
 * автоподбор по названию. Неизвестный порталу id — как «нет сопоставления».
 * @param {{overrides: object, known: Map<string, string|null>, details: {id: string}[], groups: string[], subgroups: string[]}} p
 */
export const makeCostResolver = ({ overrides, known = new Map(), details, groups, subgroups }) => {
  const ids = new Set(details.map((d) => d.id));
  const cache = new Map();
  return (code, raw) => {
    if (!code) return null;
    if (cache.has(code)) return cache.get(code);
    let id = null;
    if (Object.prototype.hasOwnProperty.call(overrides, code)) id = overrides[code];
    else if (known.has(code)) id = known.get(code);
    else if (raw) id = matchArticle(parseCostArticle(raw.startsWith(code) ? raw : `${code} ${raw}`, groups, subgroups), details).id;
    if (id && !ids.has(id)) id = null;
    cache.set(code, id);
    return id;
  };
};

// ─── «Затраты на строительство» ──────────────────────────────────────────────

const OTD = 'ОТДЕЛОЧНЫЕ РАБОТЫ';
const DLV = 'ДВЕРИ, ЛЮКИ, ВОРОТА';

/** Группы 1С (код) → group_key портала; null — аналога нет (в отчёт). */
export const GROUP_KEYS = {
  '000000217': null, // 01. ПОДГОТОВИТЕЛЬНЫЕ РАБОТЫ
  '000000218': 'category-Организация строительной площадки',
  '000000219': 'category-ЗЕМЛЯНЫЕ РАБОТЫ',
  '000000220': 'category-ВОДООТВЕДЕНИЕ И ВОДОПОНИЖЕНИЕ',
  '000000221': 'category-УСТРОЙСТВО КОТЛОВАНА',
  '000000222': 'category-ГИДРОИЗОЛЯЦИОННЫЕ РАБОТЫ',
  '000000223': 'category-УСТРОЙСТВО ВИБРОЗАЩИТЫ',
  '000000224': 'category-МОНОЛИТНЫЕ РАБОТЫ',
  '000000225': 'category-МЕТАЛЛИЧЕСКИЕ КОНСТРУКЦИИ',
  '000000226': 'category-КЛАДОЧНЫЕ РАБОТЫ',
  '000000227': 'category-КРОВЛЯ',
  '000000228': 'category-ФАСАДНЫЕ РАБОТЫ',
  '000000229': `category-${OTD}`,
  '000000230': `location-${OTD}-Автостоянка`,
  '000000231': `location-${OTD}-Технические помещения`,
  '000000232': `location-${OTD}-Кладовки Помещения не БКТ`,
  '000000233': `location-${OTD}-МОП ПЧ`,
  '000000234': `location-${OTD}-Техпространство`,
  '000000235': `location-${OTD}-Лобби`,
  '000000236': `location-${OTD}-МОП НЧ`,
  '000000237': `location-${OTD}-ЛК`,
  '000000238': 'category-МОКАП',
  '000000239': `category-${DLV}`,
  '000000241': `location-${DLV}-Автостоянка`,
  '000000242': `location-${DLV}-МОП НЧ`,
  '000000243': `location-${DLV}-Лобби`,
  '000000240': 'group-ВНУТРЕННИЕ ИНЖЕНЕРНЫЕ СИСТЕМЫ',
  '000000244': 'category-ВИС / Механические инженерные системы',
  '000000245': 'category-ВИС / Электрические системы',
  '000000246': 'category-ВИС / Слаботочные системы, автоматика и диспетчеризация',
  '000000247': 'category-ТЕХНОЛОГИЯ (ТХ)',
  '000000248': null, // 16. НАРУЖНЫЕ СЕТИ — на портале три «НАРУЖНИЕ ВИС / …» без общего ключа
  '000000249': 'category-БЛАГОУСТРОЙСТВО',
  '000000250': 'category-ОТДЕЛКА КВАРТИР MR BASE (предчистовая отделка)',
  '000000251': null, // 18.01. Отделочные работы (внутри MR BASE)
  '000000252': null, // 18.02. Разводка ВИС (внутри MR BASE)
  '000000253': 'category-ПРОЕКТНЫЕ РАБОТЫ', // 19. РАЗРАБОТКА / КОРРЕКТИРОВКА РД
};

/** Проверка, что ключ группы существует на портале (категория / локация категории). */
export const groupKeyExists = (key, categories, details) => {
  if (key.startsWith('group-')) return true;
  if (key.startsWith('category-')) return categories.some((c) => c.name === key.slice(9));
  const m = /^location-(.+)-([^-]+)$/.exec(key);
  if (!m) return false;
  const cat = categories.find((c) => c.name === m[1]);
  return Boolean(cat) && details.some((d) => d.cost_category_id === cat.id && d.location === m[2]);
};

/**
 * Строки «Затрат на строительство» версии: детальные статьи → detail id,
 * группы → group_key. Пропускаем строки без количества и без примечания.
 * Совпавшие цели складываются (с отметкой в отчёте).
 */
export const buildVolumeRows = (rows, tenderId, resolve) => {
  const targets = new Map();
  const skipped = [];
  for (const r of rows) {
    const qty = numComma(r.qty) ?? 0;
    const note = str(r.note);
    if (!qty && !note) continue;
    const isGroup = Object.prototype.hasOwnProperty.call(GROUP_KEYS, r.cost_code);
    let target = null;
    if (isGroup) target = GROUP_KEYS[r.cost_code] ? { group_key: GROUP_KEYS[r.cost_code] } : null;
    else {
      const id = resolve(r.cost_code, `${r.cost_code} ${r.cost_name}`);
      target = id ? { detail_cost_category_id: id } : null;
    }
    if (!target) {
      skipped.push({ code: r.cost_code, name: r.cost_name, qty, note });
      continue;
    }
    const tk = target.group_key ?? target.detail_cost_category_id;
    const prev = targets.get(tk);
    if (prev) {
      prev.volume += qty;
      prev.notes = [prev.notes, note].filter(Boolean).join(' | ') || null;
      prev.merged = (prev.merged ?? 1) + 1;
    } else {
      targets.set(tk, { tender_id: tenderId, detail_cost_category_id: null, group_key: null, ...target, volume: qty, notes: note });
    }
  }
  return { rows: [...targets.values()], skipped };
};

// ─── Поля тендера ────────────────────────────────────────────────────────────

/** Патч admin-fields из файла ссылок (без курсов — они не меняются). */
export const buildFieldsPatch = (r) => {
  const patch = {};
  const link = (src, dst) => {
    const v = str(r[src]);
    if (v) patch[dst] = v;
  };
  // В выгрузке колонки БСМ и «вопрос-ответ» перепутаны (проверено пользователем 08.10).
  link('url_qa', 'bsm_link');
  link('url_kp_folder', 'upload_folder');
  link('url_bsm', 'qa_form_link');
  link('url_tz', 'tz_link');
  link('url_extra', 'project_folder_link');
  // Площади заказчика и СП в выгрузке тоже перепутаны (пользователь, 08.10).
  const ac = numComma(r.area_sp);
  const asp = numComma(r.area_customer);
  if (ac) patch.area_client = ac;
  if (asp) patch.area_sp = asp;
  const note = str(r.version_note);
  if (note) patch.description = note;
  const d = /^(\d{4})-(\d{2})-(\d{2})/.exec(str(r.date_to) ?? '');
  if (d && d[1] !== '0001') patch.submission_deadline = `${d[1]}-${d[2]}-${d[3]}T00:00:00+03:00`;
  return patch;
};
