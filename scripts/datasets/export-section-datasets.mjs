#!/usr/bin/env node
// scripts/datasets/export-section-datasets.mjs
//
// READ-ONLY выгрузка датасетов по строительным разделам из Yandex prod.
// По каждому разделу (лист книги): строка позиции заказчика + ВСЕ её работы и
// материалы со всеми базовыми (ПЗ) данными, по всем объектам базы.
//
// Раздел определяется через boq_items.detail_cost_category_id →
// detail_cost_categories → cost_categories.name (покрытие ~99.9%).
// Версия объекта — последняя (DISTINCT ON tender_number ... ORDER BY version DESC),
// как в backend/internal/repository/archive_search.go (scopeLatestCTE).
//
// Никогда не печатает DSN/секреты — только host/db и агрегаты.
//
// Запуск:  node scripts/datasets/export-section-datasets.mjs [--out <path.xlsx>]
// Env:     DATABASE_URL берётся литерально из .env, CA — .certs/yandex-ca.pem.

import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import pg from 'pg';
import XLSX from 'xlsx-js-style';
import { unzipSync, zipSync, strToU8, strFromU8 } from 'fflate';

const { Client } = pg;

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const CA_PATH = path.join(REPO, '.certs/yandex-ca.pem');
// Один env-файл — .env; устаревший .env.prod читается, только если ещё лежит рядом.
const LEGACY_ENV_PATH = path.join(REPO, '.env.prod');
const ENV_PATH = fs.existsSync(LEGACY_ENV_PATH) ? LEGACY_ENV_PATH : path.join(REPO, '.env');

/** Разделы: имя листа ↔ точное имя в public.cost_categories. */
const SECTIONS = [
  { sheet: 'Монолит', category: 'МОНОЛИТНЫЕ РАБОТЫ' },
  { sheet: 'Гидроизоляция', category: 'ГИДРОИЗОЛЯЦИОННЫЕ РАБОТЫ' },
  { sheet: 'Устройство котлована', category: 'УСТРОЙСТВО КОТЛОВАНА' },
  { sheet: 'Кладка', category: 'КЛАДОЧНЫЕ РАБОТЫ' },
  { sheet: 'Кровля', category: 'КРОВЛЯ' },
  { sheet: 'Отделочные работы', category: 'ОТДЕЛОЧНЫЕ РАБОТЫ' },
];

const HEADERS = [
  'Объект', '№ тендера', 'Версия', 'Номер позиции', '№ п/п', 'Уровень', 'Тип строки',
  'Затрата на строительство', 'Привязка к работе', 'Тип элемента', 'Тип материала',
  'Наименование', 'Ед. изм.', 'Кол-во заказчика', 'Коэфф. перевода', 'Коэфф. расхода',
  'Кол-во ГП', 'Валюта', 'Тип доставки', 'Стоимость доставки', 'Цена за единицу',
  'Итоговая сумма (ПЗ)', 'Ссылка на КП', 'Примечание заказчика', 'Примечание ГП',
];

const COL_WIDTHS = [30, 12, 8, 15, 10, 8, 18, 35, 16, 12, 12, 50, 10, 15, 12, 12, 15, 10, 14, 15, 15, 16, 20, 25, 25]
  .map((wch) => ({ wch }));

// Форматы чисел — как в src/utils/excel/styles.ts (numFmt кладём в .s, не в .z).
const NUM_FMT_2 = '#,##0.00';
const NUM_FMT_4 = '#,##0.0000';
const NUM_FMT_2_PLAIN = '0.00';
const NUM_FMT_4_PLAIN = '0.0000';

/** Индекс колонки → numFmt. Остальные числовые колонки идут без формата. */
const NUM_FMT_BY_COL = {
  13: NUM_FMT_4_PLAIN, // Кол-во заказчика
  14: NUM_FMT_4_PLAIN, // Коэфф. перевода
  15: NUM_FMT_4_PLAIN, // Коэфф. расхода
  16: NUM_FMT_4,       // Кол-во ГП
  19: NUM_FMT_2_PLAIN, // Стоимость доставки
  20: NUM_FMT_2,       // Цена за единицу
  21: NUM_FMT_2,       // Итоговая сумма
};

const NAME_COL = 11; // «Наименование» — выравнивание влево

const BORDER = {
  top: { style: 'thin', color: { rgb: '000000' } },
  bottom: { style: 'thin', color: { rgb: '000000' } },
  left: { style: 'thin', color: { rgb: '000000' } },
  right: { style: 'thin', color: { rgb: '000000' } },
};

const HEADER_STYLE = {
  font: { bold: true },
  fill: { fgColor: { rgb: 'E0E0E0' } },
  alignment: { horizontal: 'center', vertical: 'center', wrapText: true },
  border: BORDER,
};

// Цвета строк по типу элемента — из src/utils/excel/styles.ts.
const FILL_BY_TYPE = {
  'раб': 'FFE6CC',
  'суб-раб': 'E6D9F2',
  'раб-комп.': 'FFDDDD',
  'мат': 'D9EAFF',
  'суб-мат': 'E8F5E0',
  'мат-комп.': 'CCF2EF',
};

const WORK_TYPES = new Set(['раб', 'суб-раб', 'раб-комп.']);

// ---------------------------------------------------------------- подключение

// Читаем DSN ЛИТЕРАЛЬНО из env-файла: значение содержит `&`, ломающий
// shell-sourcing, а в окружении может висеть унаследованный DATABASE_URL.
function readDsnFromEnvFile(file) {
  for (const raw of fs.readFileSync(file, 'utf8').split(/\r?\n/)) {
    const line = raw.trim();
    if (line.startsWith('#') || !line.startsWith('DATABASE_URL=')) continue;
    let v = line.slice('DATABASE_URL='.length).trim();
    if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) v = v.slice(1, -1);
    return v;
  }
  return null;
}

function clientFromDsn(dsn) {
  const u = new URL(dsn);
  if (!u.hostname.endsWith('mdb.yandexcloud.net')) {
    console.error(`[ABORT] host=${u.hostname} — это не Yandex Managed PG. Прерываю.`);
    process.exit(2);
  }
  return new Client({
    host: u.hostname,
    port: Number(u.port || 6432),
    user: decodeURIComponent(u.username),
    password: decodeURIComponent(u.password),
    database: (u.pathname || '/').replace(/^\//, ''),
    // sslrootcert в DSN — путь внутри контейнера; локально подставляем CA явно.
    ssl: { ca: fs.readFileSync(CA_PATH), rejectUnauthorized: true, servername: u.hostname },
    statement_timeout: 300000,
  });
}

// ----------------------------------------------------------------------- SQL

// Одна актуальная версия на объект — канон из archive_search.go (scopeLatestCTE).
const LATEST_CTE = `
  latest AS (
    SELECT DISTINCT ON (t.tender_number)
           t.id, t.tender_number, COALESCE(t.version, 1) AS version, t.title,
           t.usd_rate, t.eur_rate, t.cny_rate
    FROM public.tenders t
    ORDER BY t.tender_number, COALESCE(t.version, 1) DESC, t.created_at DESC
  )`;

// Позиция + ВСЕ её строки. Джойны — донор boqItemsFullSelect (position_full.go).
const DATASET_SQL = `
WITH ${LATEST_CTE},
tgt AS (
  SELECT DISTINCT bi.client_position_id AS id
  FROM public.boq_items bi
  JOIN latest l ON l.id = bi.tender_id
  JOIN public.detail_cost_categories dcc ON dcc.id = bi.detail_cost_category_id
  WHERE dcc.cost_category_id = $1
),
scope AS (
  SELECT id FROM tgt
  UNION
  SELECT cp.id FROM public.client_positions cp
  JOIN tgt ON tgt.id = cp.parent_position_id
  WHERE cp.is_additional
)
SELECT l.title, l.tender_number, l.version,
       l.usd_rate, l.eur_rate, l.cny_rate,
       cp.id AS position_id, cp.position_number, cp.item_no, cp.work_name,
       cp.unit_code AS position_unit, cp.volume, cp.manual_volume, cp.hierarchy_level,
       cp.is_additional, cp.client_note, cp.manual_note,
       bi.id AS item_id, bi.sort_number, bi.boq_item_type::text AS boq_item_type,
       bi.material_type::text AS material_type,
       COALESCE(wn.name, mn.name) AS item_name,
       COALESCE(bi.unit_code, wn.unit, mn.unit) AS item_unit,
       bi.quantity, bi.consumption_coefficient, bi.conversion_coefficient,
       bi.currency_type::text AS currency_type,
       bi.delivery_price_type::text AS delivery_price_type,
       bi.delivery_amount, bi.unit_rate, bi.total_amount,
       bi.quote_link, bi.description,
       bi.parent_work_item_id,
       cc.name AS cat, dcc.name AS cat_detail, dcc.location AS cat_location
FROM public.client_positions cp
JOIN scope ON scope.id = cp.id
JOIN latest l ON l.id = cp.tender_id
LEFT JOIN public.boq_items bi ON bi.client_position_id = cp.id
LEFT JOIN public.work_names wn ON wn.id = bi.work_name_id
LEFT JOIN public.material_names mn ON mn.id = bi.material_name_id
LEFT JOIN public.boq_items pw ON pw.id = bi.parent_work_item_id
LEFT JOIN public.detail_cost_categories dcc ON dcc.id = bi.detail_cost_category_id
LEFT JOIN public.cost_categories cc ON cc.id = dcc.cost_category_id
ORDER BY l.tender_number, cp.position_number, cp.id,
         COALESCE(pw.sort_number, bi.sort_number),
         (bi.parent_work_item_id IS NOT NULL),
         bi.sort_number, bi.id`;

// Fail-closed по курсам: строка в валюте, у которой курс тендера пуст/≤0,
// делает total_amount недостоверным. Правило репо — падать, а не писать частично.
const FX_GUARD_SQL = `
WITH ${LATEST_CTE}
SELECT l.tender_number, l.title, bi.currency_type::text AS currency, COUNT(*)::int AS items
FROM public.boq_items bi
JOIN latest l ON l.id = bi.tender_id
JOIN public.detail_cost_categories dcc ON dcc.id = bi.detail_cost_category_id
WHERE dcc.cost_category_id = ANY($1::uuid[])
  AND bi.currency_type IS DISTINCT FROM 'RUB'
  AND (
       (bi.currency_type = 'USD' AND COALESCE(l.usd_rate, 0) <= 0)
    OR (bi.currency_type = 'EUR' AND COALESCE(l.eur_rate, 0) <= 0)
    OR (bi.currency_type = 'CNY' AND COALESCE(l.cny_rate, 0) <= 0)
  )
GROUP BY 1, 2, 3
ORDER BY 1`;

// -------------------------------------------------------------- расчёт строк

const num = (v) => (v === null || v === undefined || v === '' ? null : Number(v));

/** Курс валюты строки; RUB → 1. Курсы уже проверены FX-гардом. */
function fxRate(currency, row) {
  switch (currency) {
    case 'USD': return num(row.usd_rate) || 1;
    case 'EUR': return num(row.eur_rate) || 1;
    case 'CNY': return num(row.cny_rate) || 1;
    default: return 1;
  }
}

/** Доставка на единицу — канон calculateBoqAmount.ts / calc/boq_amount.go. */
function deliveryUnitCost(row) {
  const rate = num(row.unit_rate) || 0;
  switch (row.delivery_price_type) {
    case 'не в цене': return rate * fxRate(row.currency_type, row) * 0.03;
    case 'суммой': return num(row.delivery_amount) || 0;
    case 'в цене': return 0;
    default: return null;
  }
}

const positionRow = (r, total) => [
  r.title || '', r.tender_number || '', num(r.version),
  r.item_no || String(r.position_number ?? ''), num(r.position_number), num(r.hierarchy_level) ?? 0,
  r.is_additional ? 'ДОП-позиция' : 'Позиция заказчика',
  '', '', '', '',
  r.work_name || '', r.position_unit || '',
  num(r.volume), null, null, num(r.manual_volume),
  '', '', null, null, total,
  '', r.client_note || '', r.manual_note || '',
];

const itemRow = (r) => {
  const isWork = WORK_TYPES.has(r.boq_item_type);
  const category = r.cat ? `${r.cat} / ${r.cat_detail || ''} / ${r.cat_location || ''}` : '';
  return [
    r.title || '', r.tender_number || '', num(r.version),
    '', num(r.position_number), num(r.hierarchy_level) ?? 0,
    isWork ? 'Работа' : 'Материал',
    category,
    isWork ? '' : (r.parent_work_item_id ? 'да' : 'нет'),
    r.boq_item_type || '', r.material_type || '',
    r.item_name || '', r.item_unit || '',
    null, num(r.conversion_coefficient), num(r.consumption_coefficient), num(r.quantity),
    r.currency_type || '', r.delivery_price_type || '',
    deliveryUnitCost(r), num(r.unit_rate), num(r.total_amount),
    r.quote_link || '', '', r.description || '',
  ];
};

/** SQL-строки (уже отсортированы) → массив строк листа + счётчики. */
function buildSheetRows(rows) {
  const out = [];
  const objects = new Set();
  let positions = 0;
  let items = 0;

  let i = 0;
  while (i < rows.length) {
    const first = rows[i];
    const pid = first.position_id;
    const group = [];
    while (i < rows.length && rows[i].position_id === pid) {
      if (rows[i].item_id) group.push(rows[i]);
      i += 1;
    }
    const total = group.reduce((acc, r) => acc + (num(r.total_amount) || 0), 0);
    out.push(positionRow(first, group.length ? total : null));
    for (const r of group) out.push(itemRow(r));

    objects.add(first.tender_number);
    positions += 1;
    items += group.length;
  }

  return { rows: out, objects: objects.size, positions, items };
}

// ------------------------------------------------------------------- Excel

function makeSheet(dataRows) {
  const aoa = [HEADERS, ...dataRows];
  const ws = XLSX.utils.aoa_to_sheet(aoa);
  ws['!cols'] = COL_WIDTHS;
  ws['!autofilter'] = { ref: XLSX.utils.encode_range({ s: { r: 0, c: 0 }, e: { r: 0, c: HEADERS.length - 1 } }) };

  for (let c = 0; c < HEADERS.length; c += 1) {
    const addr = XLSX.utils.encode_cell({ r: 0, c });
    if (ws[addr]) ws[addr].s = HEADER_STYLE;
  }

  for (let i = 0; i < dataRows.length; i += 1) {
    const row = dataRows[i];
    const kind = row[6];
    const isPosition = kind === 'Позиция заказчика' || kind === 'ДОП-позиция';
    const fill = isPosition ? null : FILL_BY_TYPE[row[9]] || null;

    for (let c = 0; c < HEADERS.length; c += 1) {
      const addr = XLSX.utils.encode_cell({ r: i + 1, c });
      if (!ws[addr]) continue;
      const style = {
        border: BORDER,
        alignment: {
          wrapText: true,
          vertical: 'center',
          horizontal: c === NAME_COL ? 'left' : undefined,
        },
      };
      if (isPosition) style.font = { bold: true };
      if (fill) style.fill = { fgColor: { rgb: fill } };
      if (NUM_FMT_BY_COL[c] && typeof row[c] === 'number') style.numFmt = NUM_FMT_BY_COL[c];
      ws[addr].s = style;
    }
  }

  return ws;
}

/** Путь листа по имени — как в src/utils/excel/strikeInject.ts. */
function resolveSheetPath(files, sheetName) {
  const wbXml = strFromU8(files['xl/workbook.xml']);
  const relsXml = strFromU8(files['xl/_rels/workbook.xml.rels']);

  let rid = null;
  const sheetRe = /<sheet\b[^>]*>/g;
  let m;
  while ((m = sheetRe.exec(wbXml)) !== null) {
    const nameMatch = m[0].match(/name="([^"]*)"/);
    if (nameMatch && nameMatch[1] === sheetName) {
      rid = m[0].match(/r:id="([^"]*)"/)?.[1] ?? null;
      break;
    }
  }
  if (!rid) throw new Error(`Лист "${sheetName}" не найден в workbook.xml`);

  const rel = relsXml.match(new RegExp(`<Relationship\\b[^>]*Id="${rid}"[^>]*>`));
  const target = rel?.[0].match(/Target="([^"]*)"/)?.[1];
  if (!target) throw new Error(`Target листа "${sheetName}" не найден`);
  const clean = target.replace(/^\//, '');
  return clean.startsWith('xl/') ? clean : `xl/${clean}`;
}

/**
 * Заморозить шапку на всех листах: xlsx-js-style@1.2.0 не пишет panes
 * (`ws['!freeze']` игнорируется), поэтому <pane> впрыскивается в OOXML —
 * порт injectFreezePane из src/utils/excel/strikeInject.ts на несколько листов.
 */
function injectFreezePanes(xlsxBytes, sheetNames, rows = 1) {
  const files = unzipSync(xlsxBytes);
  const topLeft = `A${rows + 1}`;
  const paneXml =
    `<pane ySplit="${rows}" topLeftCell="${topLeft}" activePane="bottomLeft" state="frozen"/>` +
    `<selection pane="bottomLeft" activeCell="${topLeft}" sqref="${topLeft}"/>`;

  for (const name of sheetNames) {
    const sheetPath = resolveSheetPath(files, name);
    let xml = strFromU8(files[sheetPath]);
    const selfClosing = /<sheetView\b[^>]*\/>/;
    const openTag = /<sheetView\b[^>]*?>/;

    if (selfClosing.test(xml)) {
      xml = xml.replace(selfClosing, (tag) => `${tag.slice(0, -2)}>${paneXml}</sheetView>`);
    } else if (openTag.test(xml)) {
      xml = xml.replace(openTag, (tag) => `${tag}${paneXml}`);
    } else {
      const views = `<sheetViews><sheetView workbookViewId="0">${paneXml}</sheetView></sheetViews>`;
      xml = xml.replace(/<sheetData\b/, `${views}<sheetData`);
    }
    files[sheetPath] = strToU8(xml);
  }

  return zipSync(files);
}

// -------------------------------------------------------------------- main

function outPath() {
  const idx = process.argv.indexOf('--out');
  if (idx !== -1 && process.argv[idx + 1]) return path.resolve(process.argv[idx + 1]);
  const stamp = new Date().toISOString().slice(0, 10);
  return path.join('c:/tmp/hubtender-datasets', `Датасеты_разделы_${stamp}.xlsx`);
}

async function main() {
  const dsn = readDsnFromEnvFile(ENV_PATH);
  if (!dsn) { console.error(`[FAIL] DATABASE_URL not found in ${ENV_PATH}`); process.exit(2); }
  const u = new URL(dsn);
  const client = clientFromDsn(dsn);
  await client.connect();
  console.log(`[info] target: ${u.host}${u.pathname}`);

  try {
    await client.query('BEGIN READ ONLY');

    // 1. Резолв категорий по имени — защита от переименования в справочнике.
    const cats = (await client.query(
      'SELECT id, name FROM public.cost_categories WHERE name = ANY($1::text[])',
      [SECTIONS.map((s) => s.category)],
    )).rows;
    const byName = new Map(cats.map((r) => [r.name, r.id]));
    const missing = SECTIONS.filter((s) => !byName.has(s.category));
    if (missing.length) {
      const all = (await client.query('SELECT name FROM public.cost_categories ORDER BY name')).rows;
      console.error(`[FAIL] в cost_categories не найдены: ${missing.map((s) => s.category).join(', ')}`);
      console.error(`[info] доступные категории:\n  ${all.map((r) => r.name).join('\n  ')}`);
      process.exit(2);
    }

    // 2. FX-guard: fail-closed, если валютная строка без курса тендера.
    const fxBad = (await client.query(FX_GUARD_SQL, [SECTIONS.map((s) => byName.get(s.category))])).rows;
    if (fxBad.length) {
      console.error('[FAIL] строки в валюте без курса тендера — выгрузка остановлена:');
      for (const r of fxBad) console.error(`  ${r.tender_number} «${r.title}» ${r.currency}: ${r.items} строк`);
      process.exit(1);
    }

    // 3. По листу на раздел.
    const wb = XLSX.utils.book_new();
    const summary = [];
    for (const section of SECTIONS) {
      const rows = (await client.query(DATASET_SQL, [byName.get(section.category)])).rows;
      const built = buildSheetRows(rows);
      XLSX.utils.book_append_sheet(wb, makeSheet(built.rows), section.sheet);
      summary.push({
        Лист: section.sheet,
        Объектов: built.objects,
        Позиций: built.positions,
        'Строк раб/мат': built.items,
        'Всего строк листа': built.rows.length,
      });
    }

    await client.query('ROLLBACK');

    const out = outPath();
    fs.mkdirSync(path.dirname(out), { recursive: true });
    const written = new Uint8Array(XLSX.write(wb, { type: 'buffer', bookType: 'xlsx' }));
    fs.writeFileSync(out, injectFreezePanes(written, SECTIONS.map((s) => s.sheet)));

    console.table(summary);
    console.log(`[ok] ${out}`);
  } finally {
    await client.end();
  }
}

main().catch((e) => { console.error('[FAIL]', e.message); process.exit(1); });
