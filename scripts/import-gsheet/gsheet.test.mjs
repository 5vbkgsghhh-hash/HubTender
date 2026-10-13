// Тесты разбора расчётов из Google Таблиц: node --test scripts/import-gsheet/*.test.mjs

import test from 'node:test';
import assert from 'node:assert/strict';
import * as XLSX from 'xlsx/xlsx.mjs';
import { sheetAccess } from './sheet.mjs';
import { parseCalcSheet, collectUnitTexts, mergeParsed } from './calc-sheet.mjs';
import { makeNameUnitOf } from './name-units.mjs';
import { parseHeader, parseMarkups, buildMarkupValues, parseVolumes, makeGroupKeyOf } from './extras-sheet.mjs';
import { costKey, makeCostOf } from './cost-map.mjs';
import { growthByMajority, exclusionRows } from '../import-1c/extras-data.mjs';
import { fillVersion } from '../import-1c/commit.mjs';
import { nameKey } from '../import-1c/prepare.mjs';

const cellOf = (x) => {
  if (x !== null && typeof x === 'object') return { t: typeof x.v === 'number' ? 'n' : 's', ...x };
  return { t: typeof x === 'number' ? 'n' : 's', v: x };
};
const makeSheet = (rows) => {
  const ws = {};
  let maxR = 0;
  let maxC = 0;
  for (const [row, cols] of Object.entries(rows)) {
    for (const [col, x] of Object.entries(cols)) {
      ws[`${col}${row}`] = cellOf(x);
      maxR = Math.max(maxR, Number(row) - 1);
      maxC = Math.max(maxC, XLSX.utils.decode_col(col));
    }
  }
  ws['!ref'] = XLSX.utils.encode_range({ s: { r: 0, c: 0 }, e: { r: maxR, c: maxC } });
  return ws;
};
const book = (sheets) => ({ SheetNames: Object.keys(sheets), Sheets: Object.fromEntries(Object.entries(sheets).map(([n, rows]) => [n, makeSheet(rows)])) });

const HEADER = {
  A: 'КОД', B: 'Тип Материала (Основ/вспом)', C: 'Наименование строки', D: 'Затраты на строительство Вид работ',
  E: 'Затраты на строительство Детализация вида работ', F: 'Локализация уточненная', G: 'Кол-во Заказчика',
  H: 'Откуда взята стоимость Р и М?', I: 'Наименование работ', J: 'Характеристика', K: 'Ед. изм.', L: 'кол-во ГП',
  M: 'Коэф. расхода мат-ла', N: 'Объем работ/ материала', O: 'Цена работы, руб за ед.', P: 'RUB', Q: 'USD', R: 'EUR',
  S: 'CNY', T: 'ВЫБРАТЬ', U: 'Доставка материала', V: 'Цена мат-ла с НДС, руб за ед.', W: 'Итого',
  X: 'Примечания заказчика', Y: 'Примечания СУ-10',
};
const D = { D: 'Организация строительной площадки', E: 'Ограждение строительной площадки', F: 'Нет' };

const CALC = {
  1: { M: 'Курс доллара', N: 89, O: 'Курс евро', P: 96.54, Q: 'Курс юаня', R: 12.54 },
  3: { I: 'Ссылка на форму вопрос ответ', J: { v: '213 (Вопрос-ответ)', l: { Target: 'https://q' } } },
  4: { I: 'Ссылка на папку для загрузки КП', J: { v: 'Папка КП', l: { Target: 'https://kp' } } },
  5: { I: 'Ссылка на БСМ', J: { v: '213 (БСМ)', l: { Target: 'https://bsm' } } },
  6: { I: 'Ссылка на уточнения по ТЗ', J: { v: 'ТЗ', l: { Target: 'https://tz' } } },
  7: HEADER,
  10: { C: 'Заказчик', I: 'Ограждение', K: 'м.п.', G: 270, L: 273.6, X: 'прим. заказчика', Y: 'прим. СУ-10' },
  11: { ...D, C: 'раб', I: 'Монтаж ограждения', K: 'м.п.', N: { v: 273.6, f: 'L10' }, O: 200, W: { v: 54720, f: 'O11*N11' } },
  12: { ...D, C: 'мат', B: 'Основ', I: 'Профлист', K: 'м2', M: 1.02, N: { v: 279.072, f: 'N11*M12' }, P: 3600, T: 584.8,
    H: { v: 'КП.pdf', l: { Target: 'https://drive/kp' } }, W: { v: (3600 + 584.8) * 279.072, f: '(U12+V12)*N12' } },
  13: { ...D, C: 'мат', B: 'Вспом', I: 'Пропитка', K: 'л', M: 0.15, N: { v: 41.04, f: 'M13*N11' }, P: 569.67, T: 'не в цене',
    W: { v: (569.67 + 17.09) * 41.04, f: '(U13+V13)*N13' } },
  14: { ...D, C: 'раб', I: 'Демонтаж', K: 'м.п.', M: 1.25, N: { v: 273.6, f: 'L10' }, O: 100, W: { v: 34200, f: 'O14*N14*M14' } },
  15: { C: 'Заказчик', I: 'Бытовой городок', K: 'шт', L: 2 },
  16: { ...D, C: 'раб', I: 'Монтаж бытовки', K: 'шт', N: 2, O: 5000 },
  17: { ...D, C: 'мат', I: 'Бытовка', K: 'шт', M: 1, N: { v: 2, f: 'L15*M17' }, P: 100000, T: 5000 },
  18: { ...D, C: 'раб', I: 'Демонтаж КПП', K: 'шт', N: 1, O: 10000, W: 25 },
  19: { ...D, C: 'мат', I: 'Станция', K: 'шт', N: 1, P: 14673892, Q: 93214.41, T: 'в цене', W: { v: 23672362.31, f: 'V19*N19+Q19*P1' } },
  21: { A: 'ООО Альфа', C: 32, E: 'Фундаментная плита', J: 'В40', I: 'не отвечают', K: 'м3' },
  22: { ...D, C: 'раб', I: 'Бетонирование', K: 'м3', N: 10, O: 2500, W: { v: 25000, f: 'N22*O22' } },
  23: { A: 'ООО Бета', C: 31, I: 'журнал обзвона' },
  24: { C: 'Заказчик', I: 'Заголовок без строк' },
  25: { ...D, C: 'суб-мат', I: 'Двухвалютный', K: 'шт', N: 1, P: 10, Q: 1, T: 'В цене', W: 99 },
  26: { ...D, C: 'мат', I: 'Табличка', K: 'шт', N: 20, P: 3045, V: 690, T: 'в цене', W: { v: 13800, f: '(U26+V26)*N26' } },
  27: { ...D, C: 'мат', I: 'Импорт', K: 'шт', N: 2, Q: 100, V: { v: 8900, f: 'P27+Q27*$N$1' }, T: 'в цене', W: 17800 },
};

const ctx = (overrides = {}) => ({
  rates: { USD: 89, EUR: 96.54, CNY: 12.54 },
  itemUnitOf: (t) => t,
  customerUnitOf: (t) => t,
  costOf: (g, d, l) => ({ id: g ? 'D1' : null, key: costKey(g, d, l), how: g ? 'авто' : 'нет', text: `${g} | ${d} | ${l}`, candidates: [] }),
  overrides,
});

test('Google Таблицы: позиции, привязка по формуле, коэффициенты, доставка', () => {
  const s = sheetAccess(book({ Расчет: CALC }), 'Расчет');
  assert.deepEqual([...collectUnitTexts(s).customer.keys()], ['м.п.', 'шт', 'м3']);
  const res = parseCalcSheet(s, ctx({ 19: { currency: 'USD' } }));
  const { positions, units } = res.built;
  assert.deepEqual(positions.map((p) => p.work_name), ['Ограждение', 'Бытовой городок', 'Фундаментная плита (В40)', 'Заголовок без строк']);
  assert.equal(positions[0].manual_volume, 273.6);
  assert.equal(positions[0].client_note, 'прим. заказчика');
  const item = (row) => units.flatMap((u) => u.items).find((x) => x.row_index === row);
  assert.equal(item(12).parent_work_temp_id, 'w_11');
  assert.deepEqual([item(12).quantity, item(12).consumption_coefficient, item(12).conversion_coefficient], [279.072, 1.02, 1]);
  assert.deepEqual([item(12).delivery_price_type, item(12).delivery_amount, item(12).quote_link, item(12).material_type],
    ['суммой', 584.8, 'https://drive/kp', 'основн.']);
  assert.deepEqual([item(13).parent_work_temp_id, item(13).conversion_coefficient, item(13).consumption_coefficient, item(13).delivery_price_type],
    ['w_11', 0.15, 1, 'не в цене'], 'расход < 1 у привязанного — в перевод');
  assert.equal(item(13).material_type, 'вспомогат.');
  assert.equal(item(14).unit_rate, 125, 'итог = цена × объём × M → коэффициент в цену');
  assert.match(item(14).description, /коэф/);
  assert.equal(res.stats.workFactor, 1);
});

test('Google Таблицы: обнулённые строки, исправления итогов, валюты, особые строки', () => {
  const s = sheetAccess(book({ Расчет: CALC }), 'Расчет');
  const res = parseCalcSheet(s, ctx({ 19: { currency: 'USD' } }));
  const item = (row) => res.built.units.flatMap((u) => u.items).find((x) => x.row_index === row);
  assert.deepEqual([item(16).quantity, item(16).unit_rate, item(16).total_amount], [2, 0, 0]);
  assert.match(item(16).description, /Обнулено в расчёте: цена 5\s?000 RUB/);
  assert.deepEqual([item(17).unit_rate, item(17).delivery_price_type, item(17).delivery_amount, item(17).parent_work_temp_id],
    [0, 'в цене', null, null], 'обнулённый материал: доставка «суммой» снята');
  assert.deepEqual(res.zeroed.map((z) => z.row), [16, 17]);
  assert.deepEqual([item(18).unit_rate, item(18).total_amount], [25, 25], 'итог-число 25 ₽ — как в таблице');
  assert.match(item(18).description, /Итог как в таблице: 25 ₽/);
  assert.deepEqual([item(19).currency_type, item(19).unit_rate, item(19).total_amount], ['USD', 93214.41, 8296082.49]);
  assert.match(item(19).description, /В таблице также цена 14\s?673\s?892 RUB/);
  assert.deepEqual(res.corrections.map((c) => [c.row, c.how]).slice(0, 3), [[18, 'как в таблице'], [19, 'цена в USD'], [25, 'как в таблице']]);
  assert.deepEqual([item(25).unit_rate, item(25).currency_type, item(25).total_amount], [99, 'RUB', 99], 'две валюты + итог в листе — как в таблице');
  assert.equal(res.blockers.length, 0);
  const noW = parseCalcSheet(sheetAccess(book({ Расчет: { ...CALC, 25: { ...CALC[25], W: undefined } } }), 'Расчет'), ctx({ 19: { currency: 'USD' } }));
  assert.match(noW.blockers[0] ?? '', /строка 25.*нескольких валютах/, 'две валюты без итога — нужно правило');
  assert.deepEqual(res.anomalies.filter((a) => /значение/.test(a.reason)).map((a) => a.row), [21, 23]);
  assert.equal(item(22).unit_rate, 2500);
  assert.deepEqual([item(26).unit_rate, item(26).currency_type, item(26).total_amount], [690, 'RUB', 13800], 'цена с НДС введена вручную');
  assert.match(item(26).description, /взята цена с НДС/);
  assert.deepEqual([item(27).unit_rate, item(27).currency_type, item(27).total_amount], [100, 'USD', 17800]);
  assert.ok(!res.corrections.some((c) => c.row === 26 || c.row === 27));
  const total = res.built.units.flatMap((u) => u.items).reduce((t, x) => t + x.total_amount, 0);
  assert.ok(Math.abs(total - res.stats.expected) < 0.01);
});

test('Google Таблицы: шапка, проценты наценок, затраты на строительство', () => {
  const wb = book({
    Расчет: CALC,
    Анализ: {
      5: { B: 'Площадь общая (по СП), м2', C: 16770.65 },
      6: { B: 'Площадь общая (от Заказчика), м2', C: 16836.1 },
      11: { B: 'Работы', C: { v: 0.6, z: '0%' } },
      13: { B: 'Служба механизации раб (бурильщики)', C: { v: 0.0534, z: '0.00%' } },
      16: { B: 'Рост стоимости РАБОТ ', C: { v: 0.1, z: '0%' } },
      19: { B: 'ООЗ', C: { v: 0.15, z: '0%' } },
      20: { B: 'ООЗ Субподряд', C: { v: 0.15, z: '0%' } },
      22: { B: 'Прибыль', C: { v: 0.1245, z: '0.00%' } },
      23: { B: 'Коэф. генподряда на СУБПОДРЯД', C: { v: 0.16, z: '0%' } },
      24: { B: 'Новый показатель', C: { v: 0.05, z: '0%' } },
    },
    'Затраты на строительство': {
      2: { A: 'Код', B: 'Вид работ', C: 'Детализация вида работ', D: 'Локализация уточненная', E: 'Комментарии', F: 'Ед. изм.', G: 'Объем' },
      3: { A: '1.', B: 'ПОДГОТОВИТЕЛЬНЫЕ РАБОТЫ', G: 100 },
      4: { A: '1.1.', B: 'Организация строительной площадки', G: 100 },
      5: { A: '1.1.1.', B: 'Организация строительной площадки', C: 'Ограждение', D: 'Нет', E: 'Занулено' },
      6: { A: '12.4.', B: 'МОПы-подземная часть', G: 50 },
      7: { A: '7.1.', B: 'Монолитные работы', C: 'Бетонная подготовка', D: 'Нет' },
    },
  });
  const h = parseHeader(wb, '/x/213. Мосфильмовская 31А (Октябрь Групп).xlsx');
  assert.deepEqual([h.number, h.title, h.client], ['213', 'Мосфильмовская 31А (Октябрь Групп)', 'Октябрь Групп']);
  assert.deepEqual(h.rates, { USD: 89, EUR: 96.54, CNY: 12.54 });
  assert.deepEqual(h.fields, { area_sp: 16770.65, area_client: 16836.1, qa_form_link: 'https://q', upload_folder: 'https://kp', bsm_link: 'https://bsm', tz_link: 'https://tz' });

  const m = parseMarkups(wb);
  assert.deepEqual(m.found, { works_16_markup: 60, mechanization_service: 5.34, works_cost_growth: 10, overhead_own_forces: 15,
    overhead_subcontract: 15, profit_own_forces: 12.45, profit_subcontract: 16 });
  assert.deepEqual(m.unknown, ['Новый показатель = 5']);
  const params = ['works_16_markup', 'subcontract_works_cost_growth', 'nds_22', 'contingency_costs'].map((key) => ({ key, default_value: 3 }));
  const mv = buildMarkupValues(params, m.found);
  assert.deepEqual(mv.values, { works_16_markup: 60, subcontract_works_cost_growth: 0, nds_22: 20, contingency_costs: 3 });
  assert.equal(mv.notes.length, 1);

  const costOf = (g, d) => ({ id: d === 'Ограждение' ? 'D1' : null });
  const v = parseVolumes(wb, 't', costOf);
  assert.deepEqual(v.rows.map((r) => [r.group_key ?? r.detail_cost_category_id, r.volume, r.notes]), [
    ['category-Организация строительной площадки', 100, null], ['D1', 0, 'Занулено'], ['location-ОТДЕЛОЧНЫЕ РАБОТЫ-МОП ПЧ', 50, null],
  ]);
  assert.deepEqual(v.skipped.map((s) => s.name), ['1. ПОДГОТОВИТЕЛЬНЫЕ РАБОТЫ']);
});

// ─── Макет с «№ раздела» (тендер 209): расчёт из двух файлов, «Анализ» и «Затраты» отдельно ───

const HEADER2 = {
  A: '№ раздела', B: 'Тип Материала (Основ/вспом)', C: 'Наименование строки', D: 'Затраты на строительство Вид работ',
  E: 'Затраты на строительство Детализация вида работ', F: 'Локализация уточненная', G: 'Кол-во Заказчика', H: 'Ссылка на КП',
  I: 'Наименование работ', J: 'Ответственный сотрудник', K: 'Комментарий', L: 'Ед. изм ', M: 'кол-во ГП',
  N: 'Коэф. расхода мат-ла и работы', O: 'Объем работ/ материала', P: 'Цена работы, руб за ед.', Q: 'RUB', R: 'USD', S: 'EUR',
  T: 'CNY', U: 'ВЫБРАТЬ', V: 'Доставка материала', W: 'Цена мат-ла с НДС, руб за ед.', X: 'Итого', Y: 'Примечания Заказчика',
  Z: 'Примечания СУ-10',
};

const CALC2 = {
  1: { N: 'Курс доллара', O: 94.15, P: 'Курс евро', Q: 100, R: 'Курс юаня', S: 13 },
  3: { I: 'Вопросы', J: { v: '209 Вопрос-ответ', l: { Target: 'https://q2' } } },
  7: HEADER2,
  10: { C: 'Заказчик', I: 'Общие' },
  11: { A: 20, C: 'Заказчик', I: 'Секция 1.7' },
  12: { A: '20.1.', C: 'Заказчик', I: 'Земляные работы' },
  13: { A: '20.1.1.', C: 'Заказчик', I: 'Обратная засыпка', J: 'Иванов', K: 'секция 1.7', L: 'м3', G: 750, M: 750, Y: 'прим. заказчика', Z: 'прим. СУ-10' },
  14: { ...D, C: 'суб-раб', I: 'Засыпка', J: 'Иванов', K: 'секция 1.7', L: 'м3', O: { v: 750, f: 'M13' }, P: 550, X: { v: 412500, f: 'O14*P14' } },
  15: { ...D, C: 'суб-мат', B: 'основ', I: 'Песок', L: 'м3', N: 1.26, O: { v: 945, f: 'O14*N15' }, Q: 900, U: 'не в цене', V: 27, W: 900,
    X: { v: 876015, f: '(V15+W15)*O15' } },
  16: { C: 'Заказчик', I: 'Срубка оголовков', L: 'м3', G: 5 },
  17: { ...D, C: 'раб', I: 'Срубка', N: 2, O: 5, P: 100, X: { v: 1000, f: 'O17*P17*N17' } },
  18: { A: { v: 45436, z: 'd\\.m', w: '24.5' }, C: 'Заказчик', I: 'ИТП' },
  19: { A: { v: 37035, z: 'd\\.m\\.yyyy\\.', w: '24.5.2001.' }, C: 'Заказчик', I: 'Монтаж ИТП', L: 'компл.', G: 1 },
  20: { ...D, C: 'мат', I: 'Анкер забивной М12 (цанга) (шт)', O: 4, Q: 10, U: 'в цене', W: 10, X: 40 },
  21: { ...D, C: 'мат', I: 'Тройник чугун SML Ду 100х50', O: 2, Q: 100, U: 'в цене', W: 100, X: 200 },
  22: { ...D, C: 'мат', I: 'Импорт', O: 1, S: 68, U: 'в цене', W: 6844.47, X: 6844.47 },
  23: { A: '24.5.1.1.', C: 'Заказчик', I: 'Вложенная', L: 'шт', G: 1 },
  24: { A: '25', C: 'Заказчик', I: 'Заголовок' },
  25: { C: 'Заказчик', I: 'Под заголовком', L: 'шт' },
  26: { ...D, C: 'раб', I: 'Неизвестная работа', O: 1, P: 10, X: 10 },
};

const nameUnitOf = makeNameUnitOf({
  materials: [{ name: 'Тройник SML чугун Ду 100x50', unit: 'шт.' }, { name: 'Двойной', unit: 'м' }, { name: 'двойной', unit: 'шт' }],
  manual: { 'material|Импорт': 'компл' },
  unitOf: (t) => (t === 'шт' ? 'шт' : null),
});

test('Макет «№ раздела»: сдвиг колонок, номера и уровни, комментарий не грузится', () => {
  const s = sheetAccess(book({ Расчет: CALC2 }), 'Расчет');
  assert.deepEqual([...collectUnitTexts(s).items.keys()], ['м3']);
  const res = parseCalcSheet(s, { ...ctx(), rates: { USD: 94.15, EUR: 100, CNY: 13 }, nameUnitOf });
  const { positions, units } = res.built;
  assert.deepEqual(positions.map((p) => p.item_no), [null, '20', '20.1', '20.1.1', null, '24.5', '24.5.1', '24.5.1.1', '25', null]);
  assert.deepEqual(positions.map((p) => p.hierarchy_level), [0, 0, 1, 2, 2, 1, 2, 2, 0, 1]);
  assert.deepEqual(res.anomalies.map((a) => [a.row, /вложенные подняты/.test(a.reason)]), [[19, true]]);
  assert.deepEqual([positions[3].volume, positions[3].manual_volume, positions[3].client_note, positions[3].manual_note, positions[3].unit_code],
    [750, 750, 'прим. заказчика', 'прим. СУ-10', 'м3']);
  const item = (row) => units.flatMap((u) => u.items).find((x) => x.row_index === row);
  assert.deepEqual([item(14).quantity, item(14).unit_rate, item(14).total_amount, item(14).description], [750, 550, 412500, null]);
  assert.deepEqual([item(15).parent_work_temp_id, item(15).quantity, item(15).consumption_coefficient, item(15).delivery_price_type, item(15).total_amount],
    ['w_14', 945, 1.26, 'не в цене', 876015]);
  assert.deepEqual([item(17).unit_rate, item(17).total_amount], [200, 1000], 'коэффициент в формуле итога — в цену');
  assert.deepEqual([item(22).currency_type, item(22).unit_rate, item(22).total_amount], ['RUB', 6844.47, 6844.47], 'валюта по другому курсу — в рублях');
  assert.match(item(22).description, /В колонке EUR 68/);
  assert.equal(res.corrections.length, 0);
  assert.equal(res.stats.expected, res.stats.sheetTotal);
});

test('Макет «№ раздела»: единица строки без «Ед. изм» — по наименованию', () => {
  assert.deepEqual(nameUnitOf('material', 'Двойной'), { code: null, how: 'в справочнике несколько единиц: м, шт' });
  assert.equal(nameUnitOf('work', 'Тройник чугун SML Ду 100х50'), null, 'другой вид — не совпадение');
  const res = parseCalcSheet(sheetAccess(book({ Расчет: CALC2 }), 'Расчет'), { ...ctx(), rates: { USD: 94.15, EUR: 100, CNY: 13 }, nameUnitOf });
  assert.deepEqual(res.unitFills.map((f) => [f.row, f.unit, f.how]), [
    [17, 'м3', 'единица позиции'], [20, 'шт', 'из наименования'], [21, 'шт.', 'справочник портала'],
    [22, 'компл', 'по смыслу (gs-units-map.json)'], [26, 'шт', 'единица позиции'],
  ]);
  assert.deepEqual([res.stats.unitByName, res.stats.unitFallback, res.stats.unitDefault], [3, 2, 0]);
});

test('Расчёт из двух частей: сквозная нумерация, уникальные temp_id', () => {
  const part = (n) => parseCalcSheet(sheetAccess(book({ Расчет: CALC2 }), 'Расчет'), { ...ctx(), rates: { USD: 94.15, EUR: 100, CNY: 13 }, nameUnitOf, part: n });
  const res = mergeParsed([part(1), part(2)]);
  assert.deepEqual(res.built.positions.map((p) => p.position_number), Array.from({ length: 20 }, (_, i) => i + 1));
  assert.deepEqual(res.built.units.map((u) => u.target.index), Array.from({ length: 20 }, (_, i) => i));
  const items = res.built.units.flatMap((u) => u.items);
  assert.deepEqual(items.filter((x) => x.name === 'Песок').map((x) => [x.row_index, x.parent_work_temp_id]), [[100015, 'w1_14'], [200015, 'w2_14']]);
  assert.equal(new Set(items.map((x) => x.temp_id).filter(Boolean)).size, 6);
  assert.deepEqual([res.stats.items, res.stats.positions, res.partStats.length, res.stats.types['мат']], [14, 20, 2, 6]);
  assert.equal(res.stats.expected, res.partStats[0].expected * 2);
  assert.deepEqual(res.anomalies.map((a) => a.row), ['1:19', '2:19']);
  assert.equal(res.built.stats.amount1c, res.stats.expected);
});

test('Шапка из флагов, «Анализ» и «Затраты» в отдельном файле без колонки «Код»', () => {
  const extras = book({
    Анализ: { 4: { B: 'Площадь общая (по СП), м2', C: 150436.5 }, 5: { B: 'Площадь общая (от Заказчика), м2', C: 147820 }, 9: { B: 'Работы', C: { v: 0.6, z: '0%' } } },
    'Затраты на строительство': {
      2: { A: 'Вид работ', B: 'Детализация вида работ', C: 'Локализация уточненная', D: 'Примечание', E: 'Ед. изм.', F: 'Объем' },
      3: { A: 'ПОДГОТОВИТЕЛЬНЫЕ РАБОТЫ', E: 'м2', F: 100 },
      4: { A: 'Организация строительной площадки ', E: 'м2', F: 100 },
      5: { A: 'Организация строительной площадки ', B: 'Ограждение', C: 'Нет', D: 'Не требуется', E: 'м.п.' },
      6: { A: 'ОТДЕЛОЧНЫЕ РАБОТЫ', E: 'м2', F: 0 },
      7: { A: 'Автостоянка', E: 'м2', F: 12 },
      8: { A: 'МОПы-надземная часть (лестницы)', F: 3 },
      9: { A: 'ДВЕРИ, ЛЮКИ, ВОРОТА', E: 'шт.', F: 5 },
      10: { A: 'Автостоянка', E: 'шт.', F: 7 },
      11: { A: 'ОТДЕЛКА КВАРТИР MR BASE (предчистовая отделка)', F: 9 },
      12: { A: 'Отделочные работы', F: 9 },
      13: { A: 'Неизвестная группа', F: 1 },
    },
  });
  const h = parseHeader(book({ Расчет: CALC2 }), '/x/209. ЖК Адмирала Макарова Секция 1.1-1.6.xlsx', { extras, number: '209', title: 'ЖК Injoy', client: 'Талан' });
  assert.deepEqual([h.number, h.title, h.client], ['209', 'ЖК Injoy', 'Талан']);
  assert.deepEqual(h.rates, { USD: 94.15, EUR: 100, CNY: 13 });
  assert.deepEqual([h.fields.area_sp, h.fields.area_client, h.fields.qa_form_link], [150436.5, 147820, 'https://q2']);
  assert.deepEqual(parseMarkups(extras).found, { works_16_markup: 60 });
  const v = parseVolumes(extras, 't', (g, d) => ({ id: d === 'Ограждение' ? 'D1' : null }));
  assert.deepEqual(v.rows.map((r) => [r.group_key ?? r.detail_cost_category_id, r.volume, r.notes]), [
    ['category-Организация строительной площадки', 100, null], ['D1', 0, 'Не требуется'],
    ['location-ОТДЕЛОЧНЫЕ РАБОТЫ-Автостоянка', 12, null], ['location-ОТДЕЛОЧНЫЕ РАБОТЫ-ЛК', 3, null],
    ['category-ДВЕРИ, ЛЮКИ, ВОРОТА', 5, null], ['location-ДВЕРИ, ЛЮКИ, ВОРОТА-Автостоянка', 7, null],
    ['category-ОТДЕЛКА КВАРТИР MR BASE (предчистовая отделка)', 9, null],
  ]);
  assert.deepEqual(v.skipped.map((x) => x.name), ['ПОДГОТОВИТЕЛЬНЫЕ РАБОТЫ', 'Отделочные работы', 'Неизвестная группа']);
});

// ─── Тендер 214: разделители, ДОП, номера разделов с буквами, статья по наименованию ───

const NVF = { D: 'Фасадные работы', E: 'Вентилируемый фасад', F: 'Нет' };
const CALC3 = {
  1: { N: 'Курс доллара', O: 90, P: 'Курс евро', Q: 100, R: 'Курс юаня', S: 13 },
  7: HEADER2,
  10: { A: 'ПР', C: 'Заказчик', I: 'Проектные работы' },
  11: { A: 'ПР.1', C: 'Заказчик', I: 'Разработка РД', L: 'компл', G: 1, M: 1 },
  12: { ...D, C: 'суб-раб', I: 'РД', L: 'м2', O: 10, P: 100, X: { v: 1000, f: 'O12*P12' } },
  13: { C: 'ДОП', I: 'Мониторинг', L: 'компл', M: 1, Z: 'прим. СУ-10' },
  14: { C: 'Разделитель', I: 'Этап 1', L: 'шт.', M: 3 },
  15: { ...D, C: 'суб-раб', I: 'Цикл', L: 'шт', O: { v: 3, f: 'M14' }, P: 10, X: { v: 30, f: 'O15*P15' } },
  16: { ...D, C: 'суб-мат', I: 'Датчик', L: 'шт', N: 2, O: { v: 6, f: 'O15*N16' }, Q: 5, U: 'в цене', W: 5, X: { v: 30, f: '(V16+W16)*O16' } },
  17: { C: 'ДОП', I: 'Стены' },
  18: { C: 'ДОП', I: 'Мониторинг', L: 'м2', G: '1 234,50' },
  19: { ...D, C: 'раб', I: 'Окраска', O: 2, P: 50, X: 100 },
  20: { C: 'Заказчик', I: 'Расчет отделки МОП' },
  21: { C: 'Заказчик', I: 'Корпус 1', G: '4 219,42', L: 'м2' },
  22: { A: 'К1.8.1', C: 'Заказчик', I: 'Отделка', L: 'м2', G: 5 },
  23: { A: 'Б.4.2.21', C: 'Разделитель', I: 'Диск', G: 3, M: 3, L: 'шт' },
  24: { ...D, C: 'мат', I: 'Плитка', L: 'м2', O: { v: 3, f: 'M23*1' }, Q: 100, U: 'в цене', W: 100, X: 300 },
  25: { ...NVF, C: 'мат', I: 'Фасадная подсистема', L: 'м2', O: 1, Q: 10, U: 'в цене', W: 10, X: 10 },
  26: { ...NVF, C: 'раб', I: 'Наружная облицовка панелями', L: 'м2', O: 1, P: 20, X: 20 },
};
const costOf3 = makeCostOf({
  details: [{ id: 'D1' }, { id: 'SUB' }, { id: 'CLAD' }].map((d) => ({ ...d, name: d.id, location: 'Здание', categoryName: 'К' })),
  manual: {
    'Организация строительной площадки | Ограждение строительной площадки | Нет': 'D1',
    'Фасадные работы | Вентилируемый фасад | Нет': { default: 'SUB', byName: [['облицовк|панел', 'CLAD']] },
  },
  oneC: new Map(),
});
const parse3 = (part) => parseCalcSheet(sheetAccess(book({ Расчет: CALC3 }), 'Расчет'), { ...ctx(), rates: { USD: 90, EUR: 100, CNY: 13 }, costOf: costOf3, part });

test('Разделитель пропускается, ДОП — доп. работа к позиции выше', () => {
  const s = sheetAccess(book({ Расчет: CALC3 }), 'Расчет');
  assert.deepEqual([...collectUnitTexts(s).customer.keys()], ['компл', 'м2'], 'единицы разделителей не учитываются');
  const res = parse3();
  const { positions, dops, units } = res.built;
  assert.deepEqual(positions.map((p) => p.work_name), ['Проектные работы', 'Разработка РД', 'Расчет отделки МОП', 'Корпус 1', 'Отделка']);
  assert.deepEqual(dops.map((d) => [d.temp_id, d.parent_index, d.work_name, d.unit_code, d.manual_volume, d.manual_note]), [
    ['dop_13', 1, 'Мониторинг', 'компл', 1, 'прим. СУ-10'], ['dop_18', 1, 'Мониторинг (2)', 'м2', 1234.5, null],
  ]);
  assert.deepEqual(units.map((u) => [u.target.kind, u.target.index, u.items.map((x) => x.row_index)]), [
    ['pos', 0, []], ['pos', 1, [12]], ['dop', 0, [15, 16]], ['dop', 1, [19]], ['pos', 2, []], ['pos', 3, []], ['pos', 4, [24, 25, 26]],
  ]);
  const item = (row) => units.flatMap((u) => u.items).find((x) => x.row_index === row);
  assert.deepEqual([item(15).quantity, item(16).parent_work_temp_id, item(16).quantity, item(16).consumption_coefficient], [3, 'w_15', 6, 2]);
  assert.equal(item(19).unit_code, 'м2', 'строка без единицы — единица ДОП');
  assert.deepEqual([res.stats.separators, res.stats.dops, res.stats.dopEmpty, res.stats.items], [2, 2, 1, 7]);
  assert.deepEqual(res.anomalies.map((a) => [a.row, a.reason.slice(0, 22)]), [[17, 'ДОП без строк (заголов'], [23, 'разделитель с номером ']]);
  assert.equal(res.stats.expected, 1490);
});

test('Номер раздела с буквами: как есть, уровень по числу частей; заголовки без номера', () => {
  const { positions } = parse3().built;
  assert.deepEqual(positions.map((p) => p.item_no), ['ПР', 'ПР.1', null, null, 'К1.8.1']);
  assert.deepEqual(positions.map((p) => p.hierarchy_level), [0, 1, 0, 1, 2]);
  assert.equal(positions[3].volume, 4219.42, 'количество текстом «4 219,42»');
});

test('Статья расчёта, разнесённая по наименованию строки', () => {
  const res = parse3();
  const item = (row) => res.built.units.flatMap((u) => u.items).find((x) => x.row_index === row);
  assert.deepEqual([item(25).detail_cost_category_id, item(26).detail_cost_category_id, item(24).detail_cost_category_id], ['SUB', 'CLAD', 'D1']);
  assert.deepEqual([...res.costSplit.values()].map((e) => [e.id, e.rows, e.sum]), [['SUB', 1, 10], ['CLAD', 1, 20]]);
  assert.equal(costOf3('Фасадные работы', 'Вентилируемый фасад', 'Нет').id, 'SUB', 'без наименования — по умолчанию');
  assert.equal(res.unmappedCosts.size, 0);
});

test('Склейка частей с ДОП: сдвиг родителя и блоков', () => {
  const res = mergeParsed([parse3(1), parse3(2)]);
  assert.deepEqual(res.built.dops.map((d) => [d.temp_id, d.parent_index]), [['dop1_13', 1], ['dop1_18', 1], ['dop2_13', 6], ['dop2_18', 6]]);
  assert.deepEqual(res.built.units.filter((u) => u.target.kind === 'dop').map((u) => u.target.index), [0, 1, 2, 3]);
  assert.deepEqual(res.built.units.filter((u) => u.target.kind === 'pos').map((u) => u.target.index), [0, 1, 2, 3, 4, 5, 6, 7, 8, 9]);
  assert.deepEqual([res.stats.dops, res.stats.separators, res.built.positions.length], [4, 4, 10]);
  assert.deepEqual([...res.costSplit.values()].map((e) => [e.id, e.rows]), [['SUB', 2], ['CLAD', 2]]);
});

test('«Затраты на строительство»: группа = вид работ + локализация', () => {
  const wb = book({
    Анализ: { 20: { B: 'Прибыль', C: { v: 0.14235, z: '0.0000%' } }, 21: { B: 'Прибыль субподряд', C: { v: 0.13422, z: '0.0000%' } } },
    'Затраты на строительство': {
      2: { A: 'Вид работ', B: 'Детализация вида работ', C: 'Локализация уточненная', D: 'Детализация работ', E: 'Ед. изм.', F: 'Объем' },
      3: { A: 'Внутренние инженерные системы', E: 'м2', F: 1000 },
      4: { A: 'Механические инженерные системы', C: 'Здание', F: 1000 },
      5: { A: 'Механические инженерные системы', B: 'Отопление', C: 'Здание', F: 1000 },
      6: { A: 'Механические инженерные системы', C: 'Квартира', F: 200 },
      7: { A: 'Механические инженерные системы', B: 'Отопление', C: 'Квартира', F: 200 },
      8: { A: 'Электрические системы', C: 'Квартира', F: 200 },
      9: { A: 'Отделочные работы', C: 'Квартира', F: 50 },
      10: { A: 'Отделочные работы', C: 'Автостоянка', F: '15 868,10' },
      11: { A: 'Двери, люки, ворота', C: 'МОПы-подземная часть', F: 185 },
      12: { A: 'Монолитные работы', C: 'Нет', F: 700 },
      13: { A: 'Организация строительной площадки', B: 'Отопление', C: 'Нет', F: 1000 },
      14: { A: 'Общестроительные работы', C: 'Нет' },
    },
  });
  assert.deepEqual(parseMarkups(wb).found, { profit_own_forces: 14.235, profit_subcontract: 13.422 });
  const flats = 'ОТДЕЛКА КВАРТИР MR BASE (предчистовая отделка)';
  const groupKeyOf = makeGroupKeyOf([
    ['ВИС / Механические инженерные системы', 'Здание'], [flats, 'Квартира'], ['ОТДЕЛОЧНЫЕ РАБОТЫ', 'Автостоянка'], ['ОТДЕЛОЧНЫЕ РАБОТЫ', 'Лобби'],
    ['ДВЕРИ, ЛЮКИ, ВОРОТА', 'Автостоянка'], ['ДВЕРИ, ЛЮКИ, ВОРОТА', 'МОП НЧ'], ['МОНОЛИТНЫЕ РАБОТЫ', 'Здание'],
  ].map(([categoryName, location]) => ({ categoryName, location })));
  const v = parseVolumes(wb, 't', (g, d) => ({ id: d === 'Отопление' ? 'HEAT' : null }), { groupKeyOf });
  assert.deepEqual(v.rows.map((r) => [r.group_key ?? r.detail_cost_category_id, r.volume, r.merged ?? 1]), [
    ['group-ВНУТРЕННИЕ ИНЖЕНЕРНЫЕ СИСТЕМЫ', 1000, 1], ['category-ВИС / Механические инженерные системы', 1000, 1], ['HEAT', 1200, 2],
    [`category-${flats}`, 200, 1], ['location-ОТДЕЛОЧНЫЕ РАБОТЫ-Автостоянка', 15868.1, 1], ['category-МОНОЛИТНЫЕ РАБОТЫ', 700, 1],
  ]);
  assert.deepEqual(v.skipped.map((x) => x.name), ['Отделочные работы | Квартира — у группы портала уже есть другой объём', 'Двери, люки, ворота | МОПы-подземная часть']);
});

test('Числа текстом, отрицательный итог, две валюты с итогом', () => {
  const sheet = {
    1: CALC3[1],
    7: HEADER2,
    30: { A: '1', C: 'Заказчик', I: 'Позиция', L: 'шт', G: 1 },
    31: { ...D, C: 'раб', I: 'Объём текстом', L: 'шт', N: 2.3, O: { v: '2334', f: 'M99' }, P: 130, X: { v: 697866, f: 'N31*O31*P31' } },
    32: { ...D, C: 'раб', I: 'Цена текстом', L: 'м2', O: 10, P: '330', X: { v: 3300, f: 'P32*O32' } },
    33: { ...D, C: 'раб', I: 'Вычет', L: 'шт', N: 1, O: 74, P: 100, X: { v: -1000, f: 'N33*O33*P33-8400' } },
    34: { ...D, C: 'суб-мат', I: 'Две валюты', L: 'шт', O: 1, Q: 1000, S: 10, U: 'в цене', W: 2000, X: 2000 },
  };
  const res = parseCalcSheet(sheetAccess(book({ Расчет: sheet }), 'Расчет'), { ...ctx(), rates: { USD: 90, EUR: 100, CNY: 13 } });
  const item = (row) => res.built.units.flatMap((u) => u.items).find((x) => x.row_index === row);
  assert.deepEqual([item(31).quantity, item(31).unit_rate, item(31).total_amount], [2334, 299, 697866]);
  assert.deepEqual([item(32).unit_rate, item(32).total_amount], [330, 3300]);
  assert.deepEqual([item(33).quantity, item(33).unit_rate, item(33).total_amount], [74, 0, 0]);
  assert.match(item(33).description, /В таблице итог -1\s?000 ₽.*отрицательную стоимость портал не хранит/);
  assert.deepEqual(res.negatives.map((n) => [n.row, n.sheet]), [[33, -1000]]);
  assert.deepEqual([item(34).currency_type, item(34).unit_rate, item(34).total_amount], ['RUB', 2000, 2000]);
  assert.match(item(34).description, /Цены в нескольких валютах/);
  assert.deepEqual([res.corrections.length, res.blockers.length, res.stats.expected], [0, 0, 703166]);
});

// ─── Тендер 256: колонка «Интервал захода подряда на площадку», лист «Проценты наценок» ───

const HEADER3 = {
  A: 'Интервал захода подряда на площадку', B: '№ Раздела', C: 'Тип Материала\n(Основ/вспом)', D: 'Наименование строки',
  E: 'Затраты на строительство Вид работ', F: 'Затраты на строительство Детализация вида работ', G: 'Локализация уточненная',
  H: 'Кол-во Заказчика', I: 'Ссылка на КП', J: 'Наименование работ', K: 'Ответственный сотрудник', L: 'Комментарий', M: 'Ед. изм ',
  N: 'кол-во ГП', O: 'Коэф. расхода мат-ла и работы', P: 'Объем работ/ материала', Q: 'Цена работы, руб за ед.', R: 'RUB', S: 'USD',
  T: 'EUR', U: 'CNY', V: 'ВЫБРАТЬ', W: 'Доставка материала', X: 'Цена мат-ла с НДС, руб за ед.', Y: 'Итого', Z: 'Примечания Заказчика',
  AA: 'Примечания СУ-10',
};
const art = (detail) => ({ E: 'Фасадные работы', F: detail, G: 'Нет' });
const CALC4 = {
  1: { O: 'Курс доллара', P: 100, Q: 'Курс евро', R: 104, S: 'Курс юаня', T: 14 },
  5: { J: 'Вопросы', K: { v: '256 ВОПРОС-ОТВЕТ', l: { Target: 'https://q3' } } },
  9: HEADER3,
  12: { B: '1', D: 'Заказчик', J: 'Фасад' },
  13: { B: '1.1.', D: 'Заказчик', J: 'НВФ', K: 'Иванов', L: 'корпус', M: 'м2', H: 10, N: 10, Z: 'прим. заказчика', AA: 'прим. СУ-10' },
  14: { A: 1, ...art('НВФ'), D: 'суб-раб', J: 'Монтаж НВФ', M: 'м2', P: { v: 10, f: 'N13' }, Q: 100, Y: { v: 1000, f: 'P14*Q14' } },
  15: { ...art('НВФ'), C: 'вспом', D: 'суб-мат', I: { v: 'КП.pdf', l: { Target: 'https://kp3' } }, J: 'Кассета', M: 'м2', O: 1.1,
    P: { v: 11, f: 'P14*O15' }, R: 50, V: 'в цене', X: 50, Y: { v: 550, f: '(W15+X15)*P15' } },
  16: { A: '1', ...art('НВФ'), D: 'суб-раб', J: 'Леса', M: 'м2', P: 10, Q: 20, Y: 200 },
  17: { ...art('Мокрый'), D: 'суб-раб', J: 'Штукатурка', M: 'м2', P: 10, Q: 30, Y: 300 },
  18: { A: 1, ...art('Мокрый'), D: 'суб-раб', J: 'Окраска', M: 'м2', P: 10, Q: 10, Y: 100 },
  19: { A: 1, ...art('Двери'), D: 'раб', J: 'Монтаж двери', M: 'шт', P: 1, Q: 500, Y: 500 },
  20: { A: 1, ...art('Двери'), D: 'мат', J: 'Дверь', M: 'шт', P: 1, R: 900, V: 'в цене', X: 900, Y: 900 },
  21: { D: 'Разделитель', J: 'Этап', N: 5 },
};
const parse4 = (part) => parseCalcSheet(sheetAccess(book({ Расчет: CALC4 }), 'Расчет'), {
  ...ctx(), rates: { USD: 100, EUR: 104, CNY: 14 }, costOf: (g, d) => ({ id: d, key: d, text: d, candidates: [] }), part,
});

test('Третий макет: колонка интервала сдвигает все столбцы', () => {
  const res = parse4();
  const { positions, units } = res.built;
  assert.deepEqual(positions.map((p) => [p.item_no, p.hierarchy_level, p.work_name]), [['1', 0, 'Фасад'], ['1.1', 1, 'НВФ']]);
  assert.deepEqual([positions[1].volume, positions[1].manual_volume, positions[1].unit_code, positions[1].client_note, positions[1].manual_note],
    [10, 10, 'м2', 'прим. заказчика', 'прим. СУ-10']);
  const item = (row) => units.flatMap((u) => u.items).find((x) => x.row_index === row);
  assert.deepEqual([item(14).quantity, item(14).unit_rate, item(14).total_amount, item(14).detail_cost_category_id, item(14).description], [10, 100, 1000, 'НВФ', null]);
  assert.deepEqual([item(15).parent_work_temp_id, item(15).quantity, item(15).consumption_coefficient, item(15).quote_link, item(15).material_type, item(15).total_amount],
    ['w_14', 11, 1.1, 'https://kp3', 'вспомогат.', 550]);
  assert.deepEqual([res.stats.items, res.stats.separators, res.stats.expected, res.hasInterval], [7, 1, 3550, true]);
  assert.equal(parseCalcSheet(sheetAccess(book({ Расчет: CALC2 }), 'Расчет'), ctx()).hasInterval, false);
});

test('Интервал захода = 1: рост субподряда по большинству суммы суб-раб', () => {
  const res = parse4();
  assert.deepEqual([...res.growth.entries()], [
    ['НВФ', { total: 1200, flagged: 1200, rows: 2, flaggedRows: 2 }], ['Мокрый', { total: 400, flagged: 100, rows: 2, flaggedRows: 1 }],
  ]);
  assert.deepEqual(res.visitOther, { 'раб': { rows: 1, sum: 500, details: { Двери: 1 } }, 'мат': { rows: 1, sum: 900, details: { Двери: 1 } } });
  const { enabled } = growthByMajority(res.growth, (id) => id);
  assert.deepEqual([...enabled], ['НВФ'], '«1» у меньшей части суммы и у собственных строк рост не включает');
  assert.deepEqual(exclusionRows('t', ['НВФ', 'Мокрый', 'Двери'], enabled, new Set()).map((r) => `${r.exclusion_type}:${r.detail_cost_category_id}`),
    ['works:Мокрый', 'works:Двери', 'materials:НВФ', 'materials:Мокрый', 'materials:Двери']);
  const merged = mergeParsed([parse4(1), parse4(2)]);
  assert.deepEqual([merged.growth.get('НВФ'), merged.visitOther['мат'].rows, merged.hasInterval],
    [{ total: 2400, flagged: 2400, rows: 4, flaggedRows: 4 }, 2, true]);
});

test('Лист «Проценты наценок» вместо «Анализа», подпись «Работы 1,6»', () => {
  const extras = book({
    'Проценты наценок': {
      4: { B: 'Площадь общая (по СП), м2', C: 105424 }, 5: { B: 'Площадь общая (от Заказчика), м2', C: 105424 },
      9: { B: 'Работы 1,6', C: { v: 0.6, z: '0%' } }, 15: { B: 'Рост стоимости РАБОТ СУБПОДРЯДА', C: { v: 0, z: '0%' } },
      21: { B: 'Прибыль субподряда', C: { v: 0.01, z: '0.00%' } },
    },
  });
  const h = parseHeader(book({ Расчет: CALC4 }), '/x/Актуализация 256. Примавера К14 v2 Часть 1.xlsx', { extras, number: '256', title: 'ЖК Примавера К14', client: 'Спартак' });
  assert.deepEqual([h.number, h.title, h.client, h.fields.area_sp, h.fields.qa_form_link], ['256', 'ЖК Примавера К14', 'Спартак', 105424, 'https://q3']);
  assert.deepEqual(h.rates, { USD: 100, EUR: 104, CNY: 14 });
  assert.deepEqual(parseMarkups(extras).found, { works_16_markup: 60, subcontract_works_cost_growth: 0, profit_subcontract: 1 });
});

test('Импорт порциями заданного размера: блок позиции не разрывается', async () => {
  const built = parse3().built;
  const nameIds = new Map(built.units.flatMap((u) => u.items).map((it) => [nameKey(it.kind, it.name, it.unit_code), `n-${it.row_index}`]));
  const calls = [];
  const api = {
    bulkPositions: async () => {},
    listPositions: async () => built.positions.map((p) => ({ id: `p${p.position_number}`, position_number: p.position_number, total_material: 0, total_works: 0 })),
    importBoq: async (payload) => { calls.push(payload); return { inserted_items_count: payload.items.length }; },
  };
  const res = await fillVersion({ api, tenderId: 't', built, nameIds, log: () => {}, chunkItems: 3 });
  assert.deepEqual(calls.map((c) => c.items.map((x) => x.row_index)), [[12, 15, 16], [19], [24, 25, 26]]);
  assert.deepEqual(calls.map((c) => c.additional_positions.map((d) => d.temp_id)), [['dop_13'], ['dop_18'], []]);
  assert.equal(calls[0].additional_positions[0].parent_position_id, 'p2');
  assert.equal(res.inserted, 7);
});
