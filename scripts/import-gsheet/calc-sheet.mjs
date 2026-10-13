// Лист «Расчет» из Google Таблиц → позиции и строки BOQ в формате buildVersion
// (scripts/import-1c/model.mjs), чтобы заполнять тендер тем же fillVersion.
// Правила согласованы с пользователем 08.10 (план replicated-foraging-naur.md):
//  - строка «Заказчик» — позиция, ниже — её раб/мат/суб-раб/суб-мат;
//  - материал привязан к работе, если формула объёма ссылается на объём работы
//    той же позиции; перевод = N ÷ (N работы × M), расход = M (≥ 1 — как в 1С);
//  - у работы итог = цена × объём × M → коэффициент уходит в цену;
//  - «обнулённая» строка (итог пуст/0 при кол-ве и цене) — цена 0, исходное в примечание;
//  - итог ≠ кол-во × цена — «как в таблице»: цена за ед. = итог ÷ кол-во (решение
//    пользователя 08.10); правило {mode: 'formula'} или {currency} в gs-overrides.json.
// Макет с «№ раздела» (тендер 209, 12.10): номер → item_no и уровень иерархии;
// «Ответственный сотрудник» и «Комментарий» не грузятся (решение пользователя).
// Тендер 214 (12.10): «Разделитель» не грузится (его строки остаются в позиции выше);
// «ДОП» — доп. работа к строке заказчика выше, пустой ДОП-заголовок не грузится;
// номер раздела с буквами («К6.1.2», «ГЛ.4.1.8») грузится как есть, уровень — по
// числу частей через точку.
// Тендер 256 (13.10): третий макет — первой идёт колонка «Интервал захода подряда на
// площадку»; «1» у суб-раб копится по видам затрат для роста субподряда (как в 1С).

import { materialQuantities } from '../import-1c/model.mjs';
import { normLabel } from './sheet.mjs';

const ITEM_TYPES = {
  'раб': { kind: 'work', type: 'раб' },
  'суб-раб': { kind: 'work', type: 'суб-раб' },
  'раб-комп.': { kind: 'work', type: 'раб-комп.' },
  'мат': { kind: 'material', type: 'мат' },
  'суб-мат': { kind: 'material', type: 'суб-мат' },
  'мат-комп.': { kind: 'material', type: 'мат-комп.' },
};
const CURRENCIES = ['RUB', 'USD', 'EUR', 'CNY'];
const DEFAULT_UNIT = 'шт';
const NOT_IN_PRICE = 0.03;
/** Сдвиг row_index у частей расчёта: «часть × 100000 + строка» (на сервере — только для сообщений). */
const PART_ROWS = 100000;

/** Колонки A…I, общие для первых двух макетов. */
const COMMON_COLS = { interval: null, matType: 'B', kind: 'C', group: 'D', detail: 'E', location: 'F', clientQty: 'G', quote: 'H', name: 'I' };

/**
 * Макеты листа: head — ожидаемые заголовки (колонка → шаблон), cols — роль → колонка.
 * Во втором вместо «Характеристики» — «Ответственный сотрудник» и «Комментарий», поэтому
 * всё от «Ед. изм» сдвинуто на колонку вправо; в третьем перед всеми колонками добавлен
 * «Интервал захода подряда на площадку» — сдвинуто всё.
 */
const LAYOUTS = [
  {
    head: {
      A: /^код$/, B: /^тип материала/, C: /^наименование строки/, D: /вид работ/, E: /детализация/,
      F: /локализация/, G: /кол-во заказчика/, H: /откуда взята стоимость/, I: /^наименование работ/,
      J: /^характеристика/, K: /^ед\.? ?изм/, L: /кол-во гп/, M: /^коэф/, N: /^объем/, O: /^цена работы/,
      P: /^rub$/, Q: /^usd$/, R: /^eur$/, S: /^cny$/, T: /^выбрать/, U: /^доставка/, V: /^цена мат/,
      W: /^итого/, X: /примечания заказчика/, Y: /примечания су-10/,
    },
    cols: {
      ...COMMON_COLS, itemNo: null, spec: 'J', unit: 'K', gpQty: 'L', coef: 'M', qty: 'N', workPrice: 'O', RUB: 'P', USD: 'Q', EUR: 'R', CNY: 'S',
      choose: 'T', matPrice: 'V', total: 'W', clientNote: 'X', ownNote: 'Y',
    },
  },
  {
    head: {
      A: /^№ раздела/, B: /^тип материала/, C: /^наименование строки/, D: /вид работ/, E: /детализация/,
      F: /локализация/, G: /кол-во заказчика/, H: /ссылка на кп/, I: /^наименование работ/,
      J: /^ответственный/, K: /^комментарий/, L: /^ед\.? ?изм/, M: /кол-во гп/, N: /^коэф/, O: /^объем/, P: /^цена работы/,
      Q: /^rub$/, R: /^usd$/, S: /^eur$/, T: /^cny$/, U: /^выбрать/, V: /^доставка/, W: /^цена мат/,
      X: /^итого/, Y: /примечания заказчика/, Z: /примечания су-10/,
    },
    cols: {
      ...COMMON_COLS, itemNo: 'A', spec: null, unit: 'L', gpQty: 'M', coef: 'N', qty: 'O', workPrice: 'P', RUB: 'Q', USD: 'R', EUR: 'S', CNY: 'T',
      choose: 'U', matPrice: 'W', total: 'X', clientNote: 'Y', ownNote: 'Z',
    },
  },
  {
    head: {
      A: /^интервал захода/, B: /^№ раздела/, C: /^тип материала/, D: /^наименование строки/, E: /вид работ/, F: /детализация/,
      G: /локализация/, H: /кол-во заказчика/, I: /ссылка на кп/, J: /^наименование работ/,
      K: /^ответственный/, L: /^комментарий/, M: /^ед\.? ?изм/, N: /кол-во гп/, O: /^коэф/, P: /^объем/, Q: /^цена работы/,
      R: /^rub$/, S: /^usd$/, T: /^eur$/, U: /^cny$/, V: /^выбрать/, W: /^доставка/, X: /^цена мат/,
      Y: /^итого/, Z: /примечания заказчика/, AA: /примечания су-10/,
    },
    cols: {
      interval: 'A', itemNo: 'B', matType: 'C', kind: 'D', group: 'E', detail: 'F', location: 'G', clientQty: 'H', quote: 'I', name: 'J',
      spec: null, unit: 'M', gpQty: 'N', coef: 'O', qty: 'P', workPrice: 'Q', RUB: 'R', USD: 'S', EUR: 'T', CNY: 'U',
      choose: 'V', matPrice: 'X', total: 'Y', clientNote: 'Z', ownNote: 'AA',
    },
  },
];

const round10 = (x) => Math.round(x * 1e10) / 1e10;
const round2 = (x) => Math.round(x * 100) / 100;
const fmt = (x) => Number(x).toLocaleString('ru-RU', { maximumFractionDigits: 2 });
const join = (...parts) => parts.filter((p) => p !== null && p !== undefined && String(p).trim() !== '').join('\n') || null;

/** Строка заголовков и колонки макета; при незнакомом макете — ошибка с перечнем расхождений. */
export const checkLayout = (s) => {
  for (let r = 1; r <= Math.min(30, s.lastRow); r += 1) {
    const layout = LAYOUTS.find((l) => l.head.A.test(normLabel(s.text('A', r))));
    if (!layout) continue;
    const bad = Object.entries(layout.head).filter(([col, re]) => !re.test(normLabel(s.text(col, r))))
      .map(([col]) => `${col}=«${s.text(col, r) ?? ''}»`);
    if (bad.length) throw new Error(`лист «Расчет»: другой макет колонок (${bad.join(', ')})`);
    return { header: r, cols: layout.cols };
  }
  throw new Error('лист «Расчет»: не найдена строка заголовков («КОД», «№ раздела» или «Интервал захода…» в колонке A)');
};

/**
 * «№ раздела» без конечной точки. Номер, который Google превратил в дату
 * («24.5.» → 24 мая, «24.5.1.» → 24.05.2001), восстанавливается из показанного текста.
 */
const itemNoOf = (s, col, r) => {
  const raw = s.raw(col, r);
  if (raw === undefined || raw === null) return null;
  let text = String(raw).trim();
  if (typeof raw === 'number' && /[dmy]/i.test(s.format(col, r) ?? '')) {
    text = String(s.shown(col, r) ?? '').split('.').map((p) => p.trim()).filter(Boolean)
      .map((p) => (p.length === 4 ? String(Number(p) % 100) : p)).join('.');
  }
  return text.replace(/\.+$/, '') || null;
};

/** Число из ячейки; количество, которое Google отдал текстом («19 175,40»), — тоже число. */
const looseNum = (s, col, r) => {
  const n = s.num(col, r);
  if (n !== null) return n;
  const t = (s.text(col, r) ?? '').replace(/\s/g, '').replace(',', '.');
  return /^-?\d+(\.\d+)?$/.test(t) ? Number(t) : null;
};

/** Глубина номера раздела: «20.1.1» → 3, «К6.1.2» → 3, «ГЛ» → 1; не номер — 0. */
const depthOf = (no) => (/^[\p{L}\d]+(\.\d+)*$/u.test(no ?? '') ? no.split('.').length : 0);

/**
 * Уровни иерархии по «№ раздела»: глубина номера − 1. Заголовок без номера (нет строк)
 * перед позицией с известным уровнем — на уровень выше неё. Прочие позиции без номера —
 * уровень предыдущей, а под заголовком (нет строк и объёмов) — на уровень глубже.
 * Позиция со строками остаётся листом: более глубокие за ней поднимаются на её уровень,
 * иначе портал покажет её разделом и строки станут недоступны.
 * @param {number[]} itemCount число строк в блоке каждой позиции (блоки ДОП не в счёт)
 */
const assignLevels = (positions, itemCount, anomalies, rowLabel) => {
  const known = positions.map((p) => { const d = depthOf(p.item_no); return d ? d - 1 : null; });
  for (let i = positions.length - 2; i >= 0; i -= 1) {
    if (known[i] === null && !itemCount[i] && known[i + 1] !== null) known[i] = Math.max(0, known[i + 1] - 1);
  }
  let prev = null;
  let leaf = null;
  positions.forEach((p, i) => {
    const hasItems = itemCount[i] > 0;
    let level = known[i] ?? (prev ? prev.level + (prev.header ? 1 : 0) : 0);
    if (leaf && level > leaf.level) {
      level = leaf.level;
      if (!leaf.reported) {
        anomalies.push({ row: rowLabel(leaf.row), name: leaf.name, reason: 'позиция со строками, под ней вложенные позиции — вложенные подняты на её уровень' });
        leaf.reported = true;
      }
    } else leaf = hasItems ? { level, row: p.row, name: p.work_name } : null;
    p.hierarchy_level = Math.min(10, level);
    prev = { level, header: !hasItems && p.volume === null && p.manual_volume === null };
  });
};

const rowKind = (s, col, r) => {
  const raw = s.raw(col, r);
  if (raw === undefined || raw === null || String(raw).trim() === '') return null;
  const t = normLabel(raw);
  if (t === 'заказчик') return { kind: 'position' };
  if (t === 'разделитель') return { kind: 'separator' };
  if (t === 'доп') return { kind: 'dop' };
  if (ITEM_TYPES[t]) return { kind: 'item', itemKind: ITEM_TYPES[t].kind, type: ITEM_TYPES[t].type };
  return { kind: 'odd', value: String(raw).trim() };
};

/** Тексты единиц позиций и строк (для карт единиц до разбора). */
export const collectUnitTexts = (s) => {
  const { header, cols } = checkLayout(s);
  const items = new Map();
  const customer = new Map();
  for (let r = header + 1; r <= s.lastRow; r += 1) {
    const k = rowKind(s, cols.kind, r);
    const u = s.text(cols.unit, r);
    if (!k || !u || k.kind === 'separator') continue;
    const map = k.kind === 'item' ? items : customer;
    map.set(u, (map.get(u) ?? 0) + 1);
  }
  return { items, customer };
};

/** Ссылки формулы на ячейки колонки col (кроме своей строки). */
const refsTo = (formula, col, self) => [...String(formula ?? '').matchAll(new RegExp(`(?:^|[^A-Z$])\\$?${col}\\$?(\\d+)(?!\\d)`, 'g'))]
  .map((m) => Number(m[1])).filter((x) => x !== self);

/**
 * @param {ReturnType<import('./sheet.mjs').sheetAccess>} s лист «Расчет»
 * @param {{rates: {USD?: number, EUR?: number, CNY?: number}, itemUnitOf: Function, customerUnitOf: Function,
 *   costOf: (group: string|null, detail: string|null, location: string|null, name?: string) => {id: string|null, key: string},
 *   overrides?: Record<string, {currency?: string}>,
 *   nameUnitOf?: (kind: string, name: string) => {code: string|null, how: string}|null,
 *   part?: number}} ctx nameUnitOf — единица строки без «Ед. изм» по наименованию; part — номер части,
 *   когда расчёт разбит на несколько файлов (строки в отчёте и правилах — «часть:строка»)
 */
export const parseCalcSheet = (s, ctx) => {
  const { header, cols } = checkLayout(s);
  const overrides = ctx.overrides ?? {};
  const rowLabel = (r) => (ctx.part ? `${ctx.part}:${r}` : r);
  const tempId = (r) => (ctx.part ? `w${ctx.part}_${r}` : `w_${r}`);
  const spec = (r) => (cols.spec ? s.text(cols.spec, r) : null);
  // Числа читаем терпимо: часть количеств и цен Google отдаёт текстом («330», «19 175,40»).
  const num = (col, r) => looseNum(s, col, r);
  const positions = [];
  const dops = [];
  const dopNames = new Map();
  const units = [];
  const anomalies = [];
  const blockers = [];
  const zeroed = [];
  const negatives = [];
  const corrections = [];
  const unitFills = [];
  const unmappedCosts = new Map();
  const costSplit = new Map();
  const growth = new Map();
  const visitOther = {};
  const st = {
    items: 0, works: 0, materials: 0, linked: 0, unlinked: 0, folded: 0, workFactor: 0, separators: 0, dops: 0, dopEmpty: 0,
    unitByName: 0, unitFallback: 0, unitDefault: 0, noCost: 0, sheetTotal: 0, expected: 0, types: {}, currencies: {}, delivery: {},
  };
  const rows = [];
  for (let r = header + 1; r <= s.lastRow; r += 1) {
    const k = rowKind(s, cols.kind, r);
    if (k) rows.push({ r, k });
  }

  let unit = null;
  let workQty = new Map();
  const startPosition = (r, odd) => {
    const name = odd
      ? [s.text(cols.detail, r), spec(r) ? `(${spec(r)})` : null].filter(Boolean).join(' ')
      : s.text(cols.name, r);
    positions.push({
      position_number: positions.length + 1, item_no: cols.itemNo ? itemNoOf(s, cols.itemNo, r) : null, work_name: name || '(без наименования)',
      unit_code: ctx.customerUnitOf(s.text(cols.unit, r)), volume: num(cols.clientQty, r), client_note: join(spec(r), s.text(cols.clientNote, r)),
      hierarchy_level: 0, manual_volume: num(cols.gpQty, r), manual_note: s.text(cols.ownNote, r), row: r,
    });
    unit = { target: { kind: 'pos', index: positions.length - 1 }, items: [] };
    units.push(unit);
    workQty = new Map();
  };

  /** ДОП к последней позиции заказчика; повтор имени под той же позицией сервер отклоняет — « (2)». */
  const startDop = (r) => {
    const parent = positions.length - 1;
    const taken = dopNames.get(parent) ?? new Set();
    const base = s.text(cols.name, r) ?? 'ДОП';
    let name = base;
    for (let n = 2; taken.has(name.toLowerCase()); n += 1) name = `${base} (${n})`;
    taken.add(name.toLowerCase());
    dopNames.set(parent, taken);
    dops.push({
      temp_id: ctx.part ? `dop${ctx.part}_${r}` : `dop_${r}`, parent_index: parent, work_name: name,
      unit_code: ctx.customerUnitOf(s.text(cols.unit, r)), manual_volume: num(cols.gpQty, r) ?? num(cols.clientQty, r),
      manual_note: s.text(cols.ownNote, r), row: r,
    });
    unit = { target: { kind: 'dop', index: dops.length - 1 }, items: [] };
    units.push(unit);
    workQty = new Map();
  };

  const addItem = (r, k) => {
    const head = unit.target.kind === 'dop' ? dops[unit.target.index] : positions[unit.target.index];
    const name = s.text(cols.name, r) ?? '(без наименования)';
    let unitCode = ctx.itemUnitOf(s.text(cols.unit, r));
    if (!unitCode) {
      const hit = ctx.nameUnitOf?.(k.itemKind, name) ?? null;
      let how = hit?.how ?? null;
      if (hit?.code) { unitCode = hit.code; st.unitByName += 1; }
      if (!unitCode && head.unit_code) { unitCode = head.unit_code; st.unitFallback += 1; how = join(how, 'единица позиции'); }
      if (!unitCode) { unitCode = DEFAULT_UNIT; st.unitDefault += 1; how = join(how, 'по умолчанию'); }
      unitFills.push({ row: rowLabel(r), kind: k.itemKind, type: k.type, name, text: s.text(cols.unit, r), unit: unitCode, how, byName: Boolean(hit?.code) });
    }
    const cost = ctx.costOf(s.text(cols.group, r), s.text(cols.detail, r), s.text(cols.location, r), name);
    if (!cost.id) {
      st.noCost += 1;
      const e = unmappedCosts.get(cost.key) ?? { rows: 0, sample: r, ...cost };
      e.rows += 1;
      unmappedCosts.set(cost.key, e);
    }
    const N = num(cols.qty, r);
    const M = num(cols.coef, r);
    const W = num(cols.total, r);
    if (W !== null) st.sheetTotal += W;
    const ov = overrides[String(rowLabel(r))] ?? {};
    const notes = [];
    const item = {
      kind: k.itemKind, boq_item_type: k.type, name, unit_code: unitCode, row_index: (ctx.part ?? 0) * PART_ROWS + r,
      detail_cost_category_id: cost.id, quote_link: s.link(cols.quote, r) ?? s.text(cols.quote, r), quote_price_date: null,
    };
    let calc;
    let price;
    let cur = 'RUB';
    let tol = 0.05;
    let effQty = 0;
    let mixed = null;
    if (k.itemKind === 'work') {
      const O = num(cols.workPrice, r) ?? 0;
      const factor = M && M > 0 && M !== 1 && refsTo(s.formula(cols.total, r), cols.coef, -1).includes(r) ? M : 1;
      if (factor !== 1) { st.workFactor += 1; notes.push(`Цена ${fmt(O)} ₽ × коэф. ${fmt(factor)}`); }
      const quantity = N !== null && N > 0 ? N : null;
      price = round10(O * factor);
      Object.assign(item, { temp_id: tempId(r), quantity, base_quantity: quantity, unit_rate: price, currency_type: 'RUB' });
      workQty.set(r, quantity);
      effQty = quantity ?? 0;
      calc = effQty * price;
      st.works += 1;
    } else {
      // Итог листа считается от V «Цена мат-ла с НДС» (= RUB + валюта × курс);
      // V бывает введена вручную поверх колонки RUB — тогда берём V.
      const prices = CURRENCIES.map((c) => ({ cur: c, price: num(cols[c], r) })).filter((x) => x.price);
      const foreign = prices.filter((x) => x.cur !== 'RUB');
      const V = num(cols.matPrice, r);
      const near = (a, b) => Math.abs(a - b) <= Math.max(0.01, Math.abs(b) * 1e-6);
      let pick;
      if (ov.currency) {
        pick = prices.find((x) => x.cur === ov.currency) ?? { cur: ov.currency, price: 0 };
        const others = prices.filter((x) => x.cur !== ov.currency).map((x) => `${fmt(x.price)} ${x.cur}`);
        if (others.length) notes.push(`В таблице также цена ${others.join(', ')}; принята ${fmt(pick.price)} ${pick.cur}`);
      } else if (foreign.length && prices.length > 1) {
        pick = { cur: 'RUB', price: V ?? 0 };
        mixed = prices.map((x) => `${fmt(x.price)} ${x.cur}`).join(', ');
      } else if (foreign.length === 1 && ctx.rates[foreign[0].cur] && (V === null || near(V, foreign[0].price * ctx.rates[foreign[0].cur]))) {
        pick = foreign[0];
      } else {
        const column = prices[0] ?? null;
        pick = { cur: 'RUB', price: V ?? (column?.cur === 'RUB' ? column.price : 0) };
        if (column && V !== null && !near(V, column.cur === 'RUB' ? column.price : column.price * (ctx.rates[column.cur] ?? 0))) {
          notes.push(`В колонке ${column.cur} ${fmt(column.price)}, цена с НДС ${fmt(V)} ₽ — взята цена с НДС`);
        }
      }
      cur = pick.cur;
      const fx = cur === 'RUB' ? 1 : ctx.rates[cur];
      if (!fx) blockers.push(`строка ${rowLabel(r)} «${name}»: нет курса ${cur} в шапке`);
      const tNum = num(cols.choose, r);
      const tText = normLabel(s.text(cols.choose, r));
      let dw = 'в цене';
      let damount = null;
      if (tNum !== null) {
        if (tNum !== 0) { dw = 'суммой'; damount = tNum; } // число в «ВЫБРАТЬ» — доставка ₽ за единицу
      } else if (tText === 'не в цене') dw = 'не в цене';
      else if (tText !== 'в цене' && pick.price) anomalies.push({ row: rowLabel(r), name, reason: 'доставка не указана — принято «в цене»' });
      const parentRow = refsTo(s.formula(cols.qty, r), cols.qty, r).find((x) => workQty.has(x));
      const wq = parentRow ? workQty.get(parentRow) : null;
      const cons = M && M > 0 ? M : 1;
      const conv = wq && N ? N / (wq * cons) : 1;
      const q = materialQuantities(N, wq, conv, cons);
      if (q.linked) st.linked += 1; else st.unlinked += 1;
      if (q.folded) st.folded += 1;
      price = pick.price;
      Object.assign(item, {
        parent_work_temp_id: q.linked ? tempId(parentRow) : null, quantity: q.quantity, base_quantity: q.base_quantity,
        conversion_coefficient: q.conversion_coefficient, consumption_coefficient: q.consumption_coefficient,
        unit_rate: price, currency_type: cur, delivery_price_type: dw, delivery_amount: damount,
        material_type: /^всп/i.test(s.text(cols.matType, r) ?? '') ? 'вспомогат.' : 'основн.',
      });
      effQty = q.quantity === null ? 0 : (q.linked ? q.quantity : q.quantity * q.consumption_coefficient);
      const unitCost = price * (fx ?? 0) + (dw === 'не в цене' ? price * (fx ?? 0) * NOT_IN_PRICE : 0) + (dw === 'суммой' ? damount : 0);
      calc = effQty * unitCost;
      if (dw === 'не в цене') tol += 0.0051 * effQty; // в таблице доставка округлена до копеек за единицу
      st.delivery[dw] = (st.delivery[dw] ?? 0) + 1;
      st.materials += 1;
    }
    st.currencies[cur] = (st.currencies[cur] ?? 0) + 1;
    st.types[k.type] = (st.types[k.type] ?? 0) + 1;

    if (W !== null && W < 0) {
      // Отрицательную стоимость портал не хранит (расчёт КП отклоняет её) — цена 0, как на 298.1.
      notes.push(`В таблице итог ${fmt(W)} ₽ (кол-во × цена = ${fmt(calc)} ₽, цена ${fmt(price)} ${cur}) — отрицательную стоимость портал не хранит, цена обнулена`);
      Object.assign(item, { unit_rate: 0, total_amount: 0 });
      if (k.itemKind === 'material') Object.assign(item, { delivery_price_type: 'в цене', delivery_amount: null });
      negatives.push({ row: rowLabel(r), type: k.type, name, position: head.work_name, sheet: W, sheetFormula: s.formula(cols.total, r) });
    } else if ((W === null || W === 0) && Math.abs(calc) > 0.005) {
      const dNote = item.delivery_price_type === 'суммой' ? `, доставка ${fmt(item.delivery_amount)} ₽/ед.`
        : item.delivery_price_type === 'не в цене' ? ', доставка не в цене' : '';
      notes.push(`Обнулено в расчёте: цена ${fmt(price)} ${cur}${dNote}; кол-во × цена = ${fmt(calc)} ₽`);
      Object.assign(item, { unit_rate: 0, total_amount: 0 });
      if (k.itemKind === 'material') Object.assign(item, { delivery_price_type: 'в цене', delivery_amount: null });
      zeroed.push({ row: rowLabel(r), type: k.type, name, position: head.work_name, sum: round2(calc) });
    } else {
      let expected = round2(calc);
      const off = W !== null && Math.abs(W - calc) > Math.max(tol, Math.abs(calc) * 1e-6);
      if (off && !ov.currency && ov.mode !== 'formula' && effQty > 0) {
        // Как в таблице: цена за единицу = итог ÷ количество, в рублях, доставка внутри цены.
        notes.push(`Итог как в таблице: ${fmt(W)} ₽ (кол-во × цена = ${fmt(calc)} ₽, цена ${fmt(price)} ${cur}${mixed ? `; цены ${mixed}` : ''}) — цена за ед. = итог ÷ кол-во`);
        Object.assign(item, { unit_rate: round10(W / effQty), currency_type: 'RUB' });
        if (k.itemKind === 'material') Object.assign(item, { delivery_price_type: 'в цене', delivery_amount: null });
        corrections.push({ row: rowLabel(r), type: k.type, name, position: head.work_name, sheet: W, formula: round2(calc), loaded: W,
          how: 'как в таблице', sheetFormula: s.formula(cols.total, r) });
        expected = W;
      } else if (off) {
        corrections.push({ row: rowLabel(r), type: k.type, name, position: head.work_name, sheet: W, formula: expected, loaded: expected,
          how: ov.currency ? `цена в ${ov.currency}` : 'по формуле кол-во × цена', sheetFormula: s.formula(cols.total, r) });
        notes.push(`В таблице итог ${fmt(W)} ₽ — исправлено ${ov.currency ? `(цена в ${ov.currency})` : 'по формуле'}`);
      }
      if (mixed && W !== null && !off && !ov.currency) {
        notes.push(`Цены в нескольких валютах (${mixed}) — взята цена с НДС ${fmt(price)} ₽`);
      } else if (mixed && !(off && !ov.currency && ov.mode !== 'formula' && effQty > 0) && !ov.currency) {
        blockers.push(`строка ${rowLabel(r)} «${name}»: цены в нескольких валютах (${mixed}) — нужно правило в gs-overrides.json`);
      }
      item.total_amount = expected;
      st.expected += expected;
    }
    item.description = join(spec(r), s.text(cols.ownNote, r), ...notes);
    if (cols.interval) {
      // «1» в «Интервале захода»: по суб-раб копим суммы по виду затрат (рост субподряда,
      // правило большинства — в load.mjs); «1» у прочих строк на портал не переносится.
      const visit = num(cols.interval, r) === 1;
      const amount = Math.abs(item.total_amount ?? 0);
      if (k.type === 'суб-раб' && cost.id) {
        const e = growth.get(cost.id) ?? { total: 0, flagged: 0, rows: 0, flaggedRows: 0 };
        e.total += amount;
        e.rows += 1;
        if (visit) { e.flagged += amount; e.flaggedRows += 1; }
        growth.set(cost.id, e);
      } else if (visit) {
        const o = (visitOther[k.type] ??= { rows: 0, sum: 0, details: {} });
        o.rows += 1;
        o.sum += amount;
        if (cost.id) o.details[cost.id] = (o.details[cost.id] ?? 0) + 1;
      }
    }
    if (cost.split && cost.id) {
      // Статья, разнесённая по наименованию строки: суммы по видам затрат — в отчёт.
      const e = costSplit.get(`${cost.key}→${cost.id}`) ?? { text: cost.text, id: cost.id, rows: 0, sum: 0 };
      e.rows += 1;
      e.sum += item.total_amount ?? 0;
      costSplit.set(`${cost.key}→${cost.id}`, e);
    }
    unit.items.push(item);
    st.items += 1;
  };

  rows.forEach(({ r, k }, i) => {
    if (k.kind === 'position') { startPosition(r, false); return; }
    if (k.kind === 'separator') {
      st.separators += 1;
      const no = cols.itemNo ? itemNoOf(s, cols.itemNo, r) : null;
      if (no) anomalies.push({ row: rowLabel(r), name: s.text(cols.name, r), reason: `разделитель с номером раздела «${no}» — удалён, его строки остались в позиции выше` });
      return;
    }
    if (k.kind === 'dop') {
      const next = rows.slice(i + 1).find((x) => x.k.kind !== 'separator');
      if (!positions.length) {
        anomalies.push({ row: rowLabel(r), name: s.text(cols.name, r), reason: 'ДОП до первой позиции заказчика — пропущен' });
        unit = null;
      } else if (next?.k.kind !== 'item') {
        st.dopEmpty += 1;
        anomalies.push({ row: rowLabel(r), name: s.text(cols.name, r), reason: 'ДОП без строк (заголовок) — не загружен' });
      } else startDop(r);
      return;
    }
    if (k.kind === 'odd') {
      const next = rows[i + 1];
      if (next?.k.kind === 'item') {
        anomalies.push({ row: rowLabel(r), name: s.text(cols.name, r), reason: `в «Наименование строки» значение «${k.value}», под строкой работы — загружено как позиция` });
        startPosition(r, true);
      } else {
        anomalies.push({ row: rowLabel(r), name: s.text(cols.name, r), reason: `в «Наименование строки» значение «${k.value}», строк под ней нет — пропущено` });
      }
      return;
    }
    if (!unit) { anomalies.push({ row: rowLabel(r), name: s.text(cols.name, r), reason: 'строка до первой позиции — пропущена' }); return; }
    addItem(r, k);
  });

  if (cols.itemNo) {
    const itemCount = positions.map(() => 0);
    for (const u of units) if (u.target.kind === 'pos') itemCount[u.target.index] += u.items.length;
    assignLevels(positions, itemCount, anomalies, rowLabel);
  }
  st.sheetTotal = round2(st.sheetTotal);
  st.expected = round2(st.expected);
  st.positions = positions.length;
  st.dops = dops.length;
  return {
    built: { positions, dops, units, stats: { amount1c: st.expected } },
    stats: st, zeroed, negatives, corrections, anomalies, blockers, unitFills, unmappedCosts, costSplit,
    hasInterval: Boolean(cols.interval), growth, visitOther,
  };
};

const addStats = (into, from) => {
  for (const [k, v] of Object.entries(from)) into[k] = typeof v === 'number' ? (into[k] ?? 0) + v : addStats(into[k] ?? {}, v);
  return into;
};

/** Части одного расчёта (несколько файлов) → один расчёт: позиции подряд, нумерация сквозная. */
export const mergeParsed = (parts) => {
  if (parts.length === 1) return parts[0];
  const positions = [];
  const dops = [];
  const units = [];
  const stats = {};
  const unmappedCosts = new Map();
  const costSplit = new Map();
  const growth = new Map();
  const visitOther = {};
  for (const p of parts) {
    const shift = positions.length;
    const dopShift = dops.length;
    for (const pos of p.built.positions) positions.push({ ...pos, position_number: positions.length + 1 });
    for (const d of p.built.dops) dops.push({ ...d, parent_index: d.parent_index + shift });
    for (const u of p.built.units) units.push({ ...u, target: { ...u.target, index: u.target.index + (u.target.kind === 'dop' ? dopShift : shift) } });
    addStats(stats, p.stats);
    for (const [k, e] of p.unmappedCosts) {
      const prev = unmappedCosts.get(k);
      unmappedCosts.set(k, prev ? { ...prev, rows: prev.rows + e.rows } : e);
    }
    for (const [k, e] of p.costSplit) {
      const prev = costSplit.get(k);
      costSplit.set(k, prev ? { ...prev, rows: prev.rows + e.rows, sum: prev.sum + e.sum } : { ...e });
    }
    for (const [k, e] of p.growth) growth.set(k, addStats(growth.get(k) ?? {}, e));
    addStats(visitOther, p.visitOther);
  }
  stats.sheetTotal = round2(stats.sheetTotal);
  stats.expected = round2(stats.expected);
  const all = (key) => parts.flatMap((p) => p[key]);
  return {
    built: { positions, dops, units, stats: { amount1c: stats.expected } },
    stats, partStats: parts.map((p) => p.stats), zeroed: all('zeroed'), negatives: all('negatives'), corrections: all('corrections'), anomalies: all('anomalies'),
    blockers: all('blockers'), unitFills: all('unitFills'), unmappedCosts, costSplit,
    hasInterval: parts.some((p) => p.hasInterval), growth, visitOther,
  };
};
