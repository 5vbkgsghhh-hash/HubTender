// Тесты правил загрузки выгрузки 1С: node --test scripts/import-1c/

import test from 'node:test';
import assert from 'node:assert/strict';
import { splitCsvLine, num, date } from './csv.mjs';
import { parseTenderName, assignTenderNumbers, assignVersionNumbers, skipReason } from './tenders.mjs';
import { suggestUnitCode } from './units.mjs';
import { parseCostArticle, matchArticle } from './costs.mjs';
import { materialQuantities, boqItemType, versionRates, hierarchyLevel, buildVersion } from './model.mjs';
import { resolveDops } from './dop.mjs';

test('CSV: кавычки, «;» внутри поля и экранирование', () => {
  assert.deepEqual(splitCsvLine('a;"b;c";"d ""e""";NULL'), ['a', 'b;c', 'd "e"', 'NULL']);
  assert.equal(num('1 585'), 1585);
  assert.equal(num('2 007.50'), 2007.5);
  assert.equal(num('NULL'), null);
  assert.equal(date('2025-06-26 00:00:00.0000000'), '2025-06-26');
  assert.equal(date('0001-01-01 00:00:00.0000000'), null);
});

test('Номера тендеров: из начала названия, дубли → .1 .2', () => {
  assert.deepEqual(parseTenderName('263. ЖК События (Донстрой)'), { baseNumber: '263', title: 'ЖК События (Донстрой)' });
  assert.equal(parseTenderName('Тестовый 31.03.2025'), null);
  const fixed = assignTenderNumbers(['262. ЖК Дмитровское небо (этап 2.3)', '262. ЖК Дмитровское Небо, этапы 2.1, 2.2']);
  assert.equal(fixed.get('262. ЖК Дмитровское Небо, этапы 2.1, 2.2').number, '262');
  assert.equal(fixed.get('262. ЖК Дмитровское небо (этап 2.3)').number, '262.1');
  const m = assignTenderNumbers(['262. А', '263. Б', '262. В']);
  assert.equal(m.get('262. А').number, '262.1');
  assert.equal(m.get('262. В').number, '262.2');
  assert.equal(m.get('263. Б').number, '263');
  assert.equal(skipReason('297. ЖК События 6.2 (Донстрой)'), 'исключён пользователем');
  assert.equal(skipReason('272. Садовническая 76 (Балчуг Эстейт)'), 'исключён пользователем');
  assert.equal(skipReason('Тестовый 31.03.2025'), 'нет номера в начале названия');
  assert.equal(skipReason('269. ЖК Адмирал (ГАЛС)'), null);
});

test('Версии: «Версия N» → N, прочие — свободный порядковый', () => {
  const v = assignVersionNumbers(['Версия 1', 'Ub1+Ub2', 'Версия 3', 'Версия 4']);
  assert.deepEqual(v.map((x) => x.version), [1, 2, 3, 4]);
  assert.deepEqual(assignVersionNumbers(['Версия 2 (монолит)', 'Версия 3 (ГП)']).map((x) => x.version), [2, 3]);
  assert.deepEqual(assignVersionNumbers(['Ub', 'Версия 1']).map((x) => x.version), [2, 1]);
});

test('Единицы: свободный текст заказчика → код справочника', () => {
  const known = new Set(['шт', 'м', 'м2', 'м3', 'кг', 'т', 'л', 'компл', 'м.п.']);
  const cases = {
    'кв. м': 'м2', 'м²': 'м2', 'м2 прив. п': 'м2', 'куб. м': 'м3', 'м³': 'м3', 'пог. м': 'м.п.', 'п.м.': 'м.п.',
    'мп': 'м.п.', 'м.пог.': 'м.п.', 'м.п': 'м.п.', 'к-т': 'компл', 'комплект': 'компл', 'комп': 'компл', 'шт.': 'шт',
    'ед': 'шт', 'тн': 'т', 'мес': 'месяц', 'шт. / м': null, 'м2 / м3': null, '0': null, '%': null,
  };
  for (const [text, code] of Object.entries(cases)) assert.equal(suggestUnitCode(text, known), code, text);
});

test('Статьи затрат: разбор, обрезка, двойная точка', () => {
  const groups = ['Общестроительные работы', 'Монолитные работы', 'Благоустройство'];
  const a = parseCostArticle('000000013 10.01. Общестроительные работы/ Кровельные работы/ Нет', groups);
  assert.deepEqual([a.code, a.group, a.subgroup, a.location], ['000000013', 'Общестроительные работы', 'Кровельные работы', 'Нет']);
  const b = parseCostArticle('000000208 07.13. Монолитные работы/ Башенные краны (ППР) / Нет', groups);
  assert.deepEqual([b.subgroup, b.location], ['Башенные краны (ППР)', 'Нет']);
  const c = parseCostArticle('000000254 17.08.. Благоустройство/ Засыпка пазух/ Нет', groups);
  assert.equal(c.group, 'Благоустройство');
  const d = parseCostArticle('000000124 14.03.05. Монолитные работы/ Система учета энергоре', groups, ['Система учета энергоресурсов (АИИСКУЭ)']);
  assert.equal(d.subgroup, 'Система учета энергоресурсов (АИИСКУЭ)');
  assert.equal(d.location, null);
});

test('Статьи затрат: подбор вида и локации HUBTender', () => {
  const details = [
    { id: 'roof', name: 'Кровельные работы', location: 'Здание', categoryName: 'КРОВЛЯ' },
    { id: 'floor-tech', name: 'Отделка полов', location: 'Технические помещения', categoryName: 'ОТДЕЛОЧНЫЕ РАБОТЫ' },
    { id: 'floor-mop', name: 'Отделка полов', location: 'МОП НЧ', categoryName: 'ОТДЕЛОЧНЫЕ РАБОТЫ' },
    { id: 'floor-lobby', name: 'Отделка полов', location: 'Лобби', categoryName: 'ОТДЕЛОЧНЫЕ РАБОТЫ' },
  ];
  assert.equal(matchArticle({ group: 'Общестроительные работы', subgroup: 'Кровельные работы', location: 'Нет' }, details).id, 'roof');
  assert.equal(matchArticle({ group: 'Отделочные работы', subgroup: 'Отделка полов', location: 'МОПы-надземная часть' }, details).id, 'floor-mop');
  assert.equal(matchArticle({ group: 'Отделочные работы', subgroup: 'Отделка полов', location: '1-й этаж (Лобби)' }, details).id, 'floor-lobby');
  assert.equal(matchArticle({ group: 'Отделочные работы', subgroup: 'Отделка полов', location: 'Квартира' }, details).id, null);
  assert.equal(matchArticle({ group: 'Х', subgroup: 'Чего-то нет', location: 'Нет' }, details).how, 'нет');
});

test('Правила 1 и 4: привязка, перенос расхода < 1, нулевой объём', () => {
  // привязан: 440.53 × 1 × 1.26
  assert.deepEqual(materialQuantities(555.0678, 440.53, 1, 1.26), {
    linked: true, folded: false, quantity: 555.0678, base_quantity: null, conversion_coefficient: 1, consumption_coefficient: 1.26,
  });
  // привязан, расход < 1 → в перевод: 100 × 0.5 × 0.8 = 40
  const f = materialQuantities(40, 100, 0.5, 0.8);
  assert.equal(f.linked, true);
  assert.equal(f.consumption_coefficient, 1);
  assert.equal(f.conversion_coefficient, 0.4);
  // перевод 0 в 1С = 1
  assert.equal(materialQuantities(839.1, 839.1, 0, 1).conversion_coefficient, 1);
  // не привязан, перевод → в расход: 50 / (2 × 1.05)
  const u = materialQuantities(105, 999, 2, 1.05);
  assert.equal(u.linked, false);
  assert.equal(u.consumption_coefficient, 2.1);
  assert.equal(u.quantity, 50);
  // не привязан, перевод × расход < 1 → расход 1, разница — в количество
  const v = materialQuantities(30, null, 0.3, 1);
  assert.deepEqual([v.consumption_coefficient, v.quantity], [1, 30]);
  // нулевой объём → количество NULL, без привязки
  const z = materialQuantities(0, 4010.49, 1, 1);
  assert.deepEqual([z.linked, z.quantity], [false, null]);
});

test('Тип строки: комплект приоритетнее субподряда', () => {
  assert.equal(boqItemType({ row_type: 'Работа', kit: 'Да', subcontract: 'Да' }), 'раб-комп.');
  assert.equal(boqItemType({ row_type: 'Материал', kit: 'Нет', subcontract: 'Да' }), 'суб-мат');
  assert.equal(boqItemType({ row_type: 'Материал', kit: 'NULL', subcontract: 'Нет' }), 'мат');
});

test('Курсы: обратный счёт по версии без доставки', () => {
  const rows = [
    { row_type: 'Материал', currency: 'EUR', price_currency: '100.00', volume: '2', amount: '21000.00', delivery_amount: '0' },
    { row_type: 'Материал', currency: 'EUR', price_currency: '10.00', volume: '10', amount: '10815.00', delivery_amount: '31.50' },
  ];
  assert.equal(versionRates(rows).EUR.rate, 105);
  assert.equal(hierarchyLevel('01.000.01'), 2);
  assert.equal(hierarchyLevel('Код СУ-10'), 0);
});

const head = (o) => ({ row_type: 'Строка', row_name: 'Заказчик', qty_gp: '0', parent_line: 'NULL', ...o });

test('ДОП: ключ, дочерний номер, вложенный, parent_line, ручное решение', () => {
  const regular = [
    head({ pp_customer: '1', section_no: '12', name: 'Кровля', qty_gp: '0' }),
    head({ pp_customer: '2', section_no: '12.1', name: 'Газон', qty_gp: '35.8', parent_line: 'Кровля' }),
    head({ pp_customer: '3', section_no: '20', name: 'ПОС', qty_gp: '0' }),
  ];
  const dop = (o) => head({ row_name: 'Разделитель', pp_no: String(Math.random()).slice(2, 8), ...o });
  const dops = [
    dop({ pp_customer: '2', section_no: '12.1', name: 'Газон корпус 1', qty_gp: '35.8', parent_line: 'Кровля' }),
    dop({ pp_customer: '1', section_no: '12.1', name: 'Кровля ДОП', qty_gp: '7', parent_line: 'Кровля' }),
    dop({ pp_customer: '99', section_no: '77', name: 'Под-ДОП', qty_gp: '1', parent_line: 'Газон корпус 1' }),
    dop({ pp_customer: '700', section_no: '20', name: 'Ограждение', qty_gp: '5', parent_line: 'ПОС' }),
    dop({ pp_customer: '555', section_no: '99', name: 'Нечто', qty_gp: '9', parent_line: 'Нигде' }),
  ];
  const { decisions } = resolveDops({ regular, dops, tenderName: 'T', versionName: 'V', prev: new Map(), overrides: {} });
  assert.deepEqual([...decisions.values()].map((d) => [d.parent, d.how]), [
    [1, 'ключ'], [0, 'ключ'], [1, 'вложенный ДОП'], [2, 'parent_line'], [null, 'не определён'],
  ]);
  const key = decisions.get(4).key;
  const manual = resolveDops({ regular, dops, tenderName: 'T', versionName: 'V', prev: new Map(), overrides: { [key]: { parent: '20' } } });
  assert.deepEqual([manual.decisions.get(4).parent, manual.decisions.get(4).how], [2, 'вручную']);
});

test('Сборка версии: суммы сходятся с 1С по формуле сервера', () => {
  const rows = [
    head({ pp_no: '1', pp_customer: '1', section_no: '01', name: 'Раздел', qty_customer: '0', uom_customer: 'NULL' }),
    head({ pp_no: '2', pp_customer: '2', section_no: '01.01', name: 'Дорога', qty_customer: '4405.3', qty_gp: '4235', uom_customer: 'м2', note_gp: 'ГП' }),
    { row_type: 'Работа', pp_no: '3', name: 'Устройство дороги', uom: 'шт', volume: '839.1', price: '200.00', amount: '167820.00', currency: 'RUB', subcontract: 'Да', kit: 'Нет', price_currency: 'NULL', cost_category: 'NULL' },
    { row_type: 'Материал', pp_no: '4', name: 'Плита ПДП', uom: 'шт', volume: '839.1', price: '11330.00', amount: '9507003.00', currency: 'RUB', subcontract: 'Нет', kit: 'Нет', price_currency: '11000.00', delivery_amount: '330.00', delivery_where: 'Не в цене', coef_convert: '0', coef_consume: '1', parent_work_pp_no: '3', material_kind: 'Основной', cost_category: 'NULL' },
    { row_type: 'Материал', pp_no: '5', name: 'Песок', uom: 'м3', volume: '30', price: '100.00', amount: '3000.00', currency: 'RUB', subcontract: 'Нет', kit: 'Нет', price_currency: '100.00', delivery_amount: '0', delivery_where: 'В цене', coef_convert: '0.3', coef_consume: '1', parent_work_pp_no: '999', material_kind: 'Вспомогательный', cost_category: 'NULL' },
  ].map((r, i) => ({ _line: i + 2, ...r }));
  const built = buildVersion({ tenderName: 'T', versionName: 'V', rows }, {
    itemUnitOf: (x) => x, customerUnitOf: (x) => x, nameUnit: () => null, costOf: () => null, dopOverrides: {}, prevDop: new Map(),
  });
  assert.equal(built.positions.length, 2);
  assert.deepEqual(built.positions.map((p) => p.hierarchy_level), [0, 1]);
  assert.equal(built.positions[1].manual_volume, 4235);
  const items = built.units[1].items;
  const serverTotal = (it, parentLinked) => {
    if (it.kind === 'work') return it.quantity * it.unit_rate;
    const delivery = it.delivery_price_type === 'не в цене' ? it.unit_rate * 0.03 : (it.delivery_amount ?? 0);
    const cons = parentLinked ? 1 : it.consumption_coefficient;
    return it.quantity * cons * (it.unit_rate + delivery);
  };
  for (const it of items) {
    assert.ok(Math.abs(serverTotal(it, !!it.parent_work_temp_id) - it.total_amount) < 0.01, it.name);
  }
  assert.equal(items[0].boq_item_type, 'суб-раб');
  assert.equal(items[1].parent_work_temp_id, 'w_3');
  assert.equal(items[2].parent_work_temp_id, null);
  assert.equal(items[2].material_type, 'вспомогат.');
});

test('Разбор ДОП в Excel: шаблон, решения 1/2/3, номер раздела, «позиция»', async () => {
  const fs = await import('node:fs');
  const os = await import('node:os');
  const path = await import('node:path');
  const XLSX = await import('xlsx/xlsx.mjs');
  XLSX.set_fs(fs);
  const { writeDopReview, readDopReview, DOP_REVIEW_FILE } = await import('./dopreview.mjs');
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'dop-review-'));
  const cand = [{ section_no: '08.02.07.03', name: 'Кладка' }, { section_no: '12.1', name: 'Газон ' }];
  const unresolved = [
    { key: 'T|V|10', tender: '269', version: 3, name: 'ДОП А', section_no: '6.7.8', parent_line: 'X', candidates: cand },
    { key: 'T|V|11', tender: '269', version: 3, name: 'ДОП Б', section_no: '6.7.9', parent_line: 'X', candidates: cand },
    { key: 'T|V|12', tender: '269', version: 3, name: 'ДОП В', section_no: '6.7.9', parent_line: 'X', candidates: cand },
  ];
  const file = writeDopReview(dir, { unresolved, reviewDops: [] });
  assert.ok(file);
  assert.equal(writeDopReview(dir, { unresolved, reviewDops: [] }), null, 'заполненный файл не перезаписывается');
  const wb = XLSX.readFile(file);
  const ws = wb.Sheets['Не определены'];
  const header = XLSX.utils.sheet_to_json(ws, { header: 1 })[0];
  const col = header.indexOf('Решение');
  const put = (r, v) => { ws[XLSX.utils.encode_cell({ r, c: col })] = { t: 's', v }; };
  put(1, '2');
  put(2, '08.02');
  put(3, 'Позиция');
  XLSX.writeFile(wb, path.join(dir, DOP_REVIEW_FILE));
  assert.deepEqual(readDopReview(dir), {
    'T|V|10': { parent: '12.1', name: 'Газон' },
    'T|V|11': { parent: '08.02' },
    'T|V|12': 'position',
  });
});

test('Наименования: каноническая форма и порядок слов', async () => {
  const { canonKey, canonName } = await import('./names.mjs');
  assert.equal(canonName('Плита ПДП (3,0х1,75х0,17м)'), canonName('плита пдп 3.0x1.75x0.17 м'));
  assert.equal(canonName('Гвозди 100 (кг)'), canonName('Гвозди 100'));
  assert.equal(canonName('Дренаж QDrain или аналог'), canonName('Дренаж QDrain'));
  assert.equal(canonName('Бетон B25'), canonName('Бетон В25'), 'латинская B = кириллическая В');
  assert.notEqual(canonName('Труба 32, 40 мм'), canonName('Труба 32.40 мм'), '«32, 40» — список, не дробь');
  assert.equal(canonKey('material', 'Бетон В25 F150', 'м3'), canonKey('material', 'F150 Бетон B25', 'м3'));
  assert.notEqual(canonKey('material', 'Кладка ГСБ 250 мм', 'м3'), canonKey('material', 'Кладка ГСБ 200 мм', 'м3'));
  assert.notEqual(canonKey('work', 'Песок', 'м3'), canonKey('work', 'Песок', 'т'));
});

test('Единицы портала: т→тн, маш/час→м-час; 298 → 298.1; --replace', async () => {
  const { buildUnitMap } = await import('./units.mjs');
  const known = new Set(['шт', 'тн', 'м-час', 'м2']);
  const items = buildUnitMap(new Map([['т', 5], ['маш/час', 2], ['лист', 1]]), known, {}, { proposeNew: true });
  assert.deepEqual(Object.fromEntries(items.map), { 'т': 'тн', 'маш/час': 'м-час', 'лист': 'лист' });
  assert.equal(buildUnitMap(new Map([['тн', 1]]), known).map.get('тн'), 'тн');
  const m = assignTenderNumbers(['298. ЖК Сокольники (АЙСОРС)']);
  assert.deepEqual([m.get('298. ЖК Сокольники (АЙСОРС)').number, m.get('298. ЖК Сокольники (АЙСОРС)').title], ['298.1', 'ЖК Сокольники (претендер)']);
  const { parseReplace } = await import('./tenders.mjs');
  assert.deepEqual(parseReplace('269=Т-001-ГАЛС, 300'), [{ number: '269', old: 'Т-001-ГАЛС' }, { number: '300', old: '300' }]);
});

test('Часть 2: наценки — ₽/м² → %, нет показателя в 1С, соседняя версия', async () => {
  const { perM2ToPercent, buildMarkupValues, pickMarkupSource, numComma, versionKey } = await import('./extras-data.mjs');
  assert.equal(numComma('130251,14'), 130251.14);
  assert.equal(numComma('NULL'), null);
  assert.equal(versionKey('263. А', 'Версия 1 '), '263. А|Версия 1');
  assert.equal(perM2ToPercent(300, 2000, 6e6), 10);
  assert.equal(perM2ToPercent(300, 0, 1e6), 0);
  assert.equal(perM2ToPercent(300, 1000, 0), 0);
  const params = [
    { id: 'a', key: 'mechanization_service', default_value: 5 }, { id: 'b', key: 'works_16_markup', default_value: 60 },
    { id: 'c', key: 'subcontract_materials_cost_growth', default_value: 10 }, { id: 'd', key: 'nds_22', default_value: 22 },
    { id: 'e', key: 'profit_own_forces', default_value: 10 }, { id: 'f', key: 'warranty_period', default_value: 5 },
  ];
  const src = { area: 1000, byCode: {
    '000000009': { percent: 0, second: 300 }, '000000004': { percent: 60, second: 0 }, '000000011': { percent: 0, second: 70 },
  } };
  const { values, notes } = buildMarkupValues(params, src, 2000, 6e6);
  assert.deepEqual(values, {
    mechanization_service: 10, works_16_markup: 60, subcontract_materials_cost_growth: 0, nds_22: 20, profit_own_forces: 10,
    warranty_period: 11.667,
  }, 'гарантия ×5: 70 × 5 × 2000 / 6 млн × 100; НДС 20');
  assert.equal(notes.length, 1, 'прибыли нет в 1С — значение по умолчанию с пометкой');
  const none = buildMarkupValues(params, null, 1000, 1e6).values;
  assert.deepEqual([none.subcontract_materials_cost_growth, none.nds_22], [10, 20], 'без источника — по умолчанию, НДС 20');
  const index = new Map([
    ['T|Версия 1', { number: '1', version: 1 }], ['T|Версия 2', { number: '1', version: 2 }],
    ['T|Версия 3', { number: '1', version: 3 }], ['U|Версия 1', { number: '2', version: 1 }],
  ]);
  const mk = new Map([['T|Версия 1', {}], ['T|Версия 3', {}], ['U|Версия 1', {}]]);
  assert.deepEqual(pickMarkupSource('T|Версия 2', index.get('T|Версия 2'), index, mk), { key: 'T|Версия 1', how: 'из версии 1' });
  assert.equal(pickMarkupSource('T|Версия 3', index.get('T|Версия 3'), index, mk).how, 'своя версия');
  assert.equal(pickMarkupSource('X|Версия 1', { number: '9', version: 1 }, index, mk), null);
});

test('Часть 2: рост субподряда — большинство суммы строк с «1», исключения по типам', async () => {
  const { growthByMajority, exclusionRows } = await import('./extras-data.mjs');
  const resolve = (code) => ({ c1: 'D1', c2: 'D1', c3: 'D2', c4: 'D3', c5: null })[code];
  const byCode = new Map([
    ['c1', { raw: 'c1', total: 100, flagged: 100, rows: 2, flaggedRows: 2 }],
    ['c2', { raw: 'c2', total: 300, flagged: 0, rows: 3, flaggedRows: 0 }],
    ['c3', { raw: 'c3', total: 100, flagged: 60, rows: 2, flaggedRows: 1 }],
    ['c4', { raw: 'c4', total: 0, flagged: 0, rows: 3, flaggedRows: 2 }],
    ['c5', { raw: 'c5', total: 50, flagged: 50, rows: 1, flaggedRows: 1 }],
  ]);
  const { enabled, byDetail } = growthByMajority(byCode, resolve);
  assert.deepEqual([...enabled].sort(), ['D2', 'D3'], 'D1: 100 из 400 — нет; D2: 60 из 100; D3: нулевые суммы — по строкам');
  assert.equal(byDetail.get('D1').total, 400);
  const rows = exclusionRows('t', ['D1', 'D2', 'D3'], new Set(['D2']), new Set());
  assert.deepEqual(rows.map((r) => `${r.detail_cost_category_id}:${r.exclusion_type}`),
    ['D1:works', 'D3:works', 'D1:materials', 'D2:materials', 'D3:materials']);
});

test('Часть 2: «Затраты на строительство» и поля тендера', async () => {
  const { buildVolumeRows, groupKeyExists, buildFieldsPatch } = await import('./extras-data.mjs');
  const resolve = (code) => ({ '000000001': 'D1', '000000002': 'D1' })[code] ?? null;
  const { rows, skipped } = buildVolumeRows([
    { cost_code: '000000224', cost_name: '07. МОНОЛИТНЫЕ РАБОТЫ', qty: '1500.000', note: '' },
    { cost_code: '000000217', cost_name: '01. ПОДГОТОВИТЕЛЬНЫЕ РАБОТЫ', qty: '130251.140', note: 'NULL' },
    { cost_code: '000000001', cost_name: '01.01.01. Орг/ Ограждение/ Нет', qty: '756.000', note: 'п.м' },
    { cost_code: '000000002', cost_name: '01.01.02. Орг/ Ограждение 2/ Нет', qty: '44', note: '' },
    { cost_code: '000000099', cost_name: '99. Пусто', qty: '0.000', note: '' },
    { cost_code: '000000098', cost_name: '98. Неизвестно', qty: '5', note: '' },
  ], 't', resolve);
  assert.deepEqual(rows.map((r) => [r.group_key ?? r.detail_cost_category_id, r.volume, r.notes]),
    [['category-МОНОЛИТНЫЕ РАБОТЫ', 1500, null], ['D1', 800, 'п.м']]);
  assert.deepEqual(skipped.map((s) => s.code), ['000000217', '000000098']);
  const cats = [{ id: 'c1', name: 'ОТДЕЛОЧНЫЕ РАБОТЫ' }, { id: 'c2', name: 'МОНОЛИТНЫЕ РАБОТЫ' }];
  const det = [{ cost_category_id: 'c1', location: 'Лобби' }];
  assert.ok(groupKeyExists('location-ОТДЕЛОЧНЫЕ РАБОТЫ-Лобби', cats, det));
  assert.ok(!groupKeyExists('location-ОТДЕЛОЧНЫЕ РАБОТЫ-ЛК', cats, det));
  assert.ok(groupKeyExists('category-МОНОЛИТНЫЕ РАБОТЫ', cats, det));
  assert.ok(!groupKeyExists('category-КРОВЛЯ', cats, det));
  assert.deepEqual(buildFieldsPatch({
    url_bsm: 'https://a', url_kp_folder: '', url_qa: 'NULL', url_tz: 'https://t', url_extra: '',
    area_customer: '130251.140', area_sp: 'NULL', version_note: ' Примечание ', date_to: '2025-01-24',
  }), { qa_form_link: 'https://a', tz_link: 'https://t', area_sp: 130251.14, description: 'Примечание', submission_deadline: '2025-01-24T00:00:00+03:00' });
});
