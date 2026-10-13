#!/usr/bin/env node
// Загрузка расчётов тендеров из Google Таблиц (xlsx: «Расчет», «Анализ»,
// «Затраты на строительство») в HUBTender через API Go BFF. Один файл — один
// новый тендер, версия 1, архивный (решение пользователя 08.10).
//
//   node scripts/import-gsheet/load.mjs --file "213. ….xlsx"           проверка: разбор + справочники (только GET)
//                                                                       → out/<номер>-report.md
//   node scripts/import-gsheet/load.mjs --file "213. ….xlsx" --apply   загрузка (запись!) [--create-units]
//   node scripts/import-gsheet/load.mjs --rollback 213 --yes            удалить загруженный тендер
//
// Расчёт из нескольких файлов (209): --merge — все --file это части одного тендера по
// порядку; --extras <xlsx> — файл с листами «Анализ» и «Затраты на строительство»;
// --number, --title, --client — вместо выведенных из имени файла.
// --extras-only — завести тендер только с процентами наценок и объёмами «Затрат на
// строительство», без прямых затрат (позиции и строки не создаются).
// --keep-on-fail — при сбое заполнения не удалять тендер (по умолчанию удаляется).
//
// Решения (out/): gs-cost-map.json {"Вид | Детализация | Локализация": id|null},
// gs-units-map.json {items: {текст: код}, customer: {…}, byName: {"material|Наименование": код}},
// gs-overrides.json {"<номер>": {"<строка>": {"currency": "USD"}}} (у частей строка —
// «часть:строка»). Журнал — out/gsheet-manifest.json.
// Окружение — как у scripts/import-1c (TENDERHUB_API_URL, TENDERHUB_EMAIL, TENDERHUB_PASSWORD).

import fs from 'node:fs';
import path from 'node:path';
import { parseArgs } from 'node:util';
import { HubApi, withRetry } from '../import-1c/api.mjs';
import { buildUnitMap } from '../import-1c/units.mjs';
import { canonKey } from '../import-1c/names.mjs';
import { fillVersion } from '../import-1c/commit.mjs';
import { detailLabel } from '../import-1c/costs.mjs';
import { groupKeyExists, growthByMajority, exclusionRows } from '../import-1c/extras-data.mjs';
import { readWorkbook, sheetAccess } from './sheet.mjs';
import { collectUnitTexts, parseCalcSheet, mergeParsed } from './calc-sheet.mjs';
import { makeNameUnitOf } from './name-units.mjs';
import { parseHeader, parseMarkups, buildMarkupValues, parseVolumes, makeGroupKeyOf } from './extras-sheet.mjs';
import { loadOneCMap, makeCostOf } from './cost-map.mjs';
import { renderReport } from './report.mjs';

const TACTIC_NAME = 'Базовая схема';
const NAME_CONCURRENCY = 6;
/** Строк в одном запросе импорта: порции по ~1 200 строк упирались в таймаут nginx (504). */
const IMPORT_CHUNK_ITEMS = 500;
/** Пауза между порциями импорта (мс) — даёт серверу закончить автопересчёт тендера. */
const IMPORT_PAUSE_MS = 3000;
const DIR_1C_OUT = 'scripts/import-1c/out';

const { values: args } = parseArgs({
  options: {
    file: { type: 'string', multiple: true, default: [] },
    out: { type: 'string', default: 'scripts/import-gsheet/out' },
    apply: { type: 'boolean', default: false },
    'create-units': { type: 'boolean', default: false },
    rollback: { type: 'string' },
    yes: { type: 'boolean', default: false },
    merge: { type: 'boolean', default: false },
    'extras-only': { type: 'boolean', default: false },
    'keep-on-fail': { type: 'boolean', default: false },
    extras: { type: 'string' },
    number: { type: 'string' },
    title: { type: 'string' },
    client: { type: 'string' },
  },
});

const log = (...a) => console.log(...a);
const readJson = (dir, file) => {
  const p = path.join(dir, file);
  return fs.existsSync(p) ? JSON.parse(fs.readFileSync(p, 'utf8')) : null;
};
const manifestFile = (dir) => path.join(dir, 'gsheet-manifest.json');
const saveManifest = (dir, m) => fs.writeFileSync(manifestFile(dir), `${JSON.stringify(m, null, 2)}\n`, 'utf8');

const pool = async (items, limit, fn) => {
  let next = 0;
  const worker = async () => {
    while (next < items.length) {
      const i = next;
      next += 1;
      await fn(items[i]);
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
};

const loadRefs = async (api, outDir) => {
  log('Справочники портала…');
  const [units, cats, detailsRaw, registry, tenders, works, materials, allParams, tactic] = await Promise.all([
    api.listUnits(), api.listCostCategories(), api.listDetailCostCategories(), api.listRegistry(), api.listTenders(),
    api.listNames('work'), api.listNames('material'), api.listMarkupParameters(), api.globalTactic(TACTIC_NAME),
  ]);
  if (!tactic?.id) throw new Error(`тактика «${TACTIC_NAME}» не найдена`);
  const catName = new Map(cats.map((c) => [c.id, c.name]));
  const details = detailsRaw.map((d) => ({ id: d.id, name: d.name, location: d.location ?? null, categoryName: catName.get(d.cost_category_id) ?? '' }));
  const nameIds = new Map();
  for (const [kind, list] of [['work', works], ['material', materials]]) {
    for (const n of list) {
      const k = canonKey(kind, n.name, n.unit);
      if (!nameIds.has(k)) nameIds.set(k, n.id);
    }
  }
  const costOf = makeCostOf({ details, manual: readJson(outDir, 'gs-cost-map.json') ?? {}, oneC: loadOneCMap(DIR_1C_OUT) });
  return {
    unitCodes: units.map((u) => u.code), cats, detailsRaw, details, registry, tenders, nameIds, costOf, tactic, works, materials,
    groupKeyOf: makeGroupKeyOf(details),
    params: allParams.filter((p) => p.is_active !== false).sort((a, b) => (a.order_num ?? 0) - (b.order_num ?? 0)),
    label: new Map(details.map((d) => [d.id, detailLabel(d)])),
  };
};

/**
 * Разбор расчёта (один файл или части одного тендера по порядку) и всё, что нужно
 * для записи; ничего не пишет. Курсы и ссылки шапки — из первой части.
 */
const prepareFile = (files, refs, outDir) => {
  const books = files.map((f) => readWorkbook(f));
  const extras = args.extras ? readWorkbook(args.extras) : books[0];
  const header = parseHeader(books[0], files[0], { extras, number: args.number, title: args.title, client: args.client });
  const sheets = books.map((wb) => sheetAccess(wb, 'Расчет'));
  const texts = { items: new Map(), customer: new Map() };
  for (const s of sheets) {
    const t = collectUnitTexts(s);
    for (const kind of ['items', 'customer']) for (const [u, n] of t[kind]) texts[kind].set(u, (texts[kind].get(u) ?? 0) + n);
  }
  const unitOverrides = readJson(outDir, 'gs-units-map.json') ?? {};
  const known = new Set(refs.unitCodes);
  const itemMap = buildUnitMap(texts.items, known, unitOverrides.items ?? {}, { proposeNew: true });
  const custMap = buildUnitMap(texts.customer, known, unitOverrides.customer ?? {});
  const nameUnitOf = makeNameUnitOf({
    works: refs.works, materials: refs.materials, manual: unitOverrides.byName ?? {},
    unitOf: (t) => buildUnitMap(new Map([[t, 1]]), known, unitOverrides.items ?? {}).map.get(t) ?? null,
  });
  const overrides = (readJson(outDir, 'gs-overrides.json') ?? {})[header.number] ?? {};
  const parsed = mergeParsed(sheets.map((s, i) => parseCalcSheet(s, {
    rates: header.rates,
    itemUnitOf: (t) => (t ? itemMap.map.get(t) ?? null : null),
    customerUnitOf: (t) => (t ? custMap.map.get(t) ?? null : null),
    costOf: refs.costOf,
    overrides,
    nameUnitOf,
    part: sheets.length > 1 ? i + 1 : undefined,
  })));
  const markups = parseMarkups(extras);
  const mv = buildMarkupValues(refs.params, markups.found);
  const volumes = parseVolumes(extras, null, refs.costOf, { groupKeyOf: refs.groupKeyOf });
  volumes.rows = volumes.rows.filter((r) => {
    if (!r.group_key || groupKeyExists(r.group_key, refs.cats, refs.detailsRaw)) return true;
    volumes.skipped.push({ code: r.group_key, name: `нет группы на портале: ${r.group_key}`, qty: r.volume, note: r.notes });
    return false;
  });

  const names = new Map();
  const usedUnits = new Set([...parsed.built.positions, ...parsed.built.dops].map((x) => x.unit_code).filter(Boolean));
  for (const u of parsed.built.units) {
    for (const it of u.items) {
      usedUnits.add(it.unit_code);
      const k = canonKey(it.kind, it.name, it.unit_code);
      if (!names.has(k)) names.set(k, { kind: it.kind, name: it.name, unit: it.unit_code, spellings: new Map() });
      const e = names.get(k);
      e.spellings.set(it.name, (e.spellings.get(it.name) ?? 0) + 1);
    }
  }
  for (const e of names.values()) e.name = [...e.spellings.entries()].sort((a, b) => b[1] - a[1])[0][0];
  const missingNames = [...names.entries()].filter(([k]) => !refs.nameIds.has(k)).map(([, v]) => v);
  const missingUnits = [...usedUnits].filter((c) => !known.has(c));

  const blockers = [...parsed.blockers];
  // Рост субподряда по «1» в «Интервале захода» (как в переносе из 1С): вид затрат получает
  // рост работ, если помечена большая часть суммы его суб-раб; остальным рост отключается.
  const growth = parsed.hasInterval ? { enabled: growthByMajority(parsed.growth, (id) => id).enabled } : null;
  if (growth) growth.exclusions = exclusionRows(null, refs.details.map((d) => d.id), growth.enabled, new Set()).length;

  if (!args['extras-only'] && missingUnits.length && !args['create-units']) blockers.push(`единиц нет в справочнике: ${missingUnits.join(', ')} — запуск с --create-units заведёт их`);
  if (!args['extras-only'] && parsed.unmappedCosts.size) blockers.push(`${parsed.unmappedCosts.size} видов затрат без сопоставления — решения в out/gs-cost-map.json`);
  const taken = refs.tenders.filter((t) => t.tender_number === header.number);
  if (taken.length) blockers.push(`номер ${header.number} уже есть на портале: ${taken.map((t) => `v${t.version} «${t.title}»`).join(', ')}`);
  return {
    files, fileName: files.map((f) => path.basename(f)).join(' + '), header, parsed, markups, mv, volumes, names, missingNames, missingUnits,
    unitReport: { items: itemMap.report, customer: custMap.report }, blockers, growth,
  };
};

const applyFile = async (api, refs, prep, outDir) => {
  const { header } = prep;
  const manifest = readJson(outDir, 'gsheet-manifest.json') ?? { tenders: {} };
  const entry = { file: prep.fileName, status: 'started', startedAt: new Date().toISOString(), registryArchived: [] };
  manifest.tenders[header.number] = entry;
  saveManifest(outDir, manifest);

  const extrasOnly = args['extras-only'];
  const nameIds = new Map(refs.nameIds);
  if (!extrasOnly) {
    if (prep.missingUnits.length) {
      log(`Заводим единицы: ${prep.missingUnits.join(', ')}`);
      await api.importUnits(prep.missingUnits.map((code) => ({ code, name: code, category: 'custom', sort_order: 999, is_active: true })));
      entry.unitsCreated = prep.missingUnits;
    }
    log(`Создаю наименования: ${prep.missingNames.length}`);
    await pool(prep.missingNames, NAME_CONCURRENCY, (n) => api.createName(n.kind, n.name, n.unit));
    entry.namesCreated = prep.missingNames.length;
    for (const kind of ['work', 'material']) {
      for (const n of await api.listNames(kind)) {
        const k = canonKey(kind, n.name, n.unit);
        if (!nameIds.has(k)) nameIds.set(k, n.id);
      }
    }
    const lost = [...prep.names.keys()].filter((k) => !nameIds.has(k));
    if (lost.length) throw new Error(`после создания не найдено ${lost.length} наименований (пример: ${lost[0]})`);
  }

  const registryBefore = new Set((await api.listRegistry()).map((r) => r.id));
  const rate = (c) => header.rates[c] ?? undefined;
  const created = await api.createTender({
    tender_number: header.number, title: header.title, client_name: header.client ?? '—',
    usd_rate: rate('USD'), eur_rate: rate('EUR'), cny_rate: rate('CNY'),
  });
  entry.id = created.id;
  saveManifest(outDir, manifest);
  const fields = Object.fromEntries(Object.entries(header.fields).filter(([, v]) => v !== null && v !== undefined));
  await api.adminPatchTender(created.id, { version: 1, is_archived: true, ...fields });
  for (const r of await api.listRegistry()) {
    if (!registryBefore.has(r.id) && r.tender_number === header.number && !r.is_archived) {
      await api.patchRegistry(r.id, { is_archived: true });
      entry.registryArchived.push(r.id);
    }
  }
  log(`Тендер ${header.number} v1: ${created.id}`);

  if (!extrasOnly) {
    try {
      entry.fill = await fillVersion({ api, tenderId: created.id, built: prep.parsed.built, nameIds, log, fileName: `${prep.fileName} (Google Таблицы)`, chunkItems: IMPORT_CHUNK_ITEMS, pauseMs: IMPORT_PAUSE_MS });
    } catch (err) {
      entry.status = 'failed';
      entry.error = err.message;
      if (args['keep-on-fail']) {
        // Удаление большого тендера сразу после сбоя добивает и без того занятую базу (13.10).
        log(`  ✗ ${err.message} — тендер оставлен недогруженным, удалить: --rollback ${header.number} --yes`);
      } else {
        log(`  ✗ ${err.message} — удаляю тендер`);
        await api.deleteTender(created.id).then(() => { entry.status = 'failed-deleted'; }).catch((e) => { entry.deleteError = e.message; });
      }
      saveManifest(outDir, manifest);
      throw err;
    }
    saveManifest(outDir, manifest);
    log(`  позиций ${entry.fill.positions}, строк ${entry.fill.inserted}, расхождений с сервером ${entry.fill.mismatchCount}`);
  }

  await withRetry(() => api.putPercentages(created.id, refs.params.map((p) => ({ tender_id: created.id, markup_parameter_id: p.id, value: prep.mv.values[p.key] }))), { log });
  await withRetry(() => api.putTacticId(created.id, refs.tactic.id), { log });
  if (prep.growth && !extrasOnly) {
    const rows = exclusionRows(created.id, refs.details.map((d) => d.id), prep.growth.enabled, new Set());
    await withRetry(() => api.insertExclusions(rows), { log });
    entry.growth = { enabledWorks: prep.growth.enabled.size, enabledMaterials: 0, exclusions: rows.length };
    log(`  рост субподряда: работы — ${prep.growth.enabled.size} видов затрат, исключений ${rows.length}`);
  }
  for (const v of prep.volumes.rows) {
    await withRetry(() => api.upsertCostVolume({ tender_id: created.id, detail_cost_category_id: v.detail_cost_category_id, group_key: v.group_key, volume: v.volume, notes: v.notes }), { log });
  }
  entry.volumes = prep.volumes.rows.length;
  entry.status = extrasOnly ? 'loaded-extras-only' : 'loaded';
  entry.finishedAt = new Date().toISOString();
  saveManifest(outDir, manifest);
  if (extrasOnly) { log(`  без прямых затрат: проценты, тактика, объёмов ${entry.volumes}`); return; }
  const t = (await api.listTenders()).find((x) => x.id === created.id);
  log(`  Σ прямых затрат на портале ${t?.base_total} (ожидалось ${prep.parsed.stats.expected}); проценты, тактика, объёмов ${entry.volumes}`);
};

const rollback = async (api, outDir, number) => {
  const manifest = readJson(outDir, 'gsheet-manifest.json') ?? { tenders: {} };
  const entry = manifest.tenders[number];
  if (!entry?.id) throw new Error(`в журнале нет загруженного тендера ${number}`);
  log(`Удалить тендер ${number} (${entry.id}, файл «${entry.file}»)?`);
  if (!args.yes) { log('Добавьте --yes для удаления.'); return; }
  await api.deleteTender(entry.id);
  entry.status = 'rolled-back';
  entry.rolledBackAt = new Date().toISOString();
  saveManifest(outDir, manifest);
  log('Удалён. Строка «Перечня» остаётся в архиве (API удаления строк нет).');
};

const main = async () => {
  const outDir = args.out;
  fs.mkdirSync(outDir, { recursive: true });
  const api = new HubApi({
    baseUrl: process.env.TENDERHUB_API_URL ?? 'https://tender.su10.ru',
    email: process.env.TENDERHUB_EMAIL,
    password: process.env.TENDERHUB_PASSWORD,
  });
  if (args.rollback) { await rollback(api, outDir, args.rollback); return; }
  if (!args.file.length) throw new Error('укажите --file <xlsx>');
  const refs = await loadRefs(api, outDir);
  if (args.merge && args.file.length < 2) throw new Error('--merge: нужно несколько --file (части одного тендера по порядку)');
  for (const files of args.merge ? [args.file] : args.file.map((f) => [f])) {
    log(`Разбор «${files.map((f) => path.basename(f)).join('» + «')}»…`);
    const prep = prepareFile(files, refs, outDir);
    const reportPath = path.join(outDir, `${prep.header.number}-report.md`);
    fs.writeFileSync(reportPath, renderReport({ prep, params: refs.params, label: refs.label }), 'utf8');
    const st = prep.parsed.stats;
    log(`  позиций ${st.positions}, ДОП ${st.dops ?? 0}, строк ${st.items}; разделителей пропущено ${st.separators ?? 0}; обнулено ${prep.parsed.zeroed.length}, исправлено ${prep.parsed.corrections.length}; Σ листа ${st.sheetTotal} → на портале ${st.expected}`);
    log(`  отчёт: ${reportPath}`);
    if (prep.blockers.length) { log(`  Блокеры:\n  - ${prep.blockers.join('\n  - ')}`); continue; }
    if (args.apply) await applyFile(api, refs, prep, outDir);
  }
};

main().catch((err) => {
  console.error(`ОШИБКА: ${err.message}`);
  process.exitCode = 1;
});
