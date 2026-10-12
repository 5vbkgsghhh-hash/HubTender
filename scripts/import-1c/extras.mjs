#!/usr/bin/env node
// Перенос из 1С, часть 2: поля тендера (ссылки, площади, описание, срок),
// количества «Затрат на строительство», рост субподряда и проценты наценок
// для версий, загруженных load.mjs (out/manifest.json).
//
//   node scripts/import-1c/extras.mjs                  пробный прогон: только GET → out/extras-report.md
//   node scripts/import-1c/extras.mjs --apply [--only 263,265] [--part fields,volumes,growth,markups]
//
// Запись по версиям: поля → объёмы → исключения роста → проценты → тактика
// (пересчёт сервер ставит сам, с дебаунсом — один на версию). Всё сравнивается
// с порталом заранее, повторный запуск пишет только расхождения.
// Журнал — out/extras-manifest.json. Окружение — как у load.mjs.

import fs from 'node:fs';
import path from 'node:path';
import { parseArgs } from 'node:util';
import { HubApi, withRetry } from './api.mjs';
import { inventory, buildMaps, readOverrides } from './inventory.mjs';
import { detailLabel } from './costs.mjs';
import {
  readCsv, numComma, versionKey, buildVersionIndex, readMarkups, pickMarkupSource, buildMarkupValues,
  readFixedCosts, collectVisitFlags, growthByMajority, exclusionRows, makeCostResolver,
  groupKeyExists, buildVolumeRows, buildFieldsPatch, GROUP_KEYS,
} from './extras-data.mjs';
import { renderExtrasReport } from './extras-report.mjs';

const FILES = {
  markups: 'Проценты наценок.csv',
  fixed: 'Ценообразование субподряда по затратам тендера.csv',
  links: 'Ссылки на таблицы и курсы валют тендеров.csv',
  volumes: 'Количество и примечания в затратах.csv',
};
const TACTIC_NAME = 'Базовая схема';
const PARTS = ['fields', 'volumes', 'growth', 'markups'];
const READ_CONCURRENCY = 2;
const CACHE_FILE = 'extras-source-cache.json';
const CACHE_VERSION = 1; // поднять при изменении разбора Результаты.csv
const SUMS_CACHE_FILE = 'extras-sums-cache.json';

const { values: args } = parseArgs({
  options: {
    csv: { type: 'string', default: 'Результаты.csv' },
    dir: { type: 'string', default: '.' },
    out: { type: 'string', default: 'scripts/import-1c/out' },
    apply: { type: 'boolean', default: false },
    part: { type: 'string', default: PARTS.join(',') },
    only: { type: 'string' },
  },
});

const log = (...a) => console.log(...a);
const list = (s) => (s ? s.split(',').map((x) => x.trim()).filter(Boolean) : []);
const sum = (xs) => xs.reduce((s, x) => s + x, 0);

/** Параллельный map с ограничением (чтение с портала). */
const pool = async (items, n, fn) => {
  const out = new Array(items.length);
  let next = 0;
  const worker = async () => {
    while (next < items.length) {
      const i = next;
      next += 1;
      out[i] = await fn(items[i], i);
    }
  };
  await Promise.all(Array.from({ length: Math.min(n, items.length) }, worker));
  return out;
};

const retry = (fn) => withRetry(fn, { log });

const sameValue = (a, b) => {
  if (typeof b === 'number') return a !== null && a !== undefined && Math.abs(Number(a) - b) < 1e-6;
  if (/^\d{4}-\d{2}-\d{2}T/.test(b)) return Boolean(a) && new Date(a).getTime() === new Date(b).getTime();
  return (a ?? null) === b;
};

const fromCache = (c) => ({
  inv: {
    tenders: c.tenders, skipped: new Map(c.skipped.map((n) => [n, ''])), groups: c.groups, subgroups: c.subgroups,
    articles: new Map(c.articles), itemUnits: new Map(), customerUnits: new Map(), nameUoms: new Map(),
  },
  visit: { flags: new Map(c.flags.map(([k, e]) => [k, new Map(e)])), ownFlagged: new Map(c.ownFlagged), rabSum: new Map(c.rabSum) },
});

/** Разбор Результаты.csv (≈2 мин CPU) — один раз, дальше из кэша в out/ по размеру и дате файла. */
const loadSource = async (csvPath, outDir) => {
  const st = fs.statSync(csvPath);
  const stamp = { v: CACHE_VERSION, size: st.size, mtimeMs: st.mtimeMs };
  const cacheFile = path.join(outDir, CACHE_FILE);
  if (fs.existsSync(cacheFile)) {
    const c = JSON.parse(fs.readFileSync(cacheFile, 'utf8'));
    if (c.stamp?.v === stamp.v && c.stamp?.size === stamp.size && c.stamp?.mtimeMs === stamp.mtimeMs) {
      log(`${csvPath} — из кэша ${cacheFile}`);
      return fromCache(c);
    }
  }
  log(`Читаю ${csvPath}…`);
  const inv = await inventory(csvPath);
  const skipped = [...inv.skipped.keys()];
  log('Флаги «интервала захода»…');
  const visit = await collectVisitFlags(csvPath, (k) => !skipped.includes(k.split('|')[0]));
  const cache = {
    stamp, tenders: inv.tenders, skipped, groups: inv.groups, subgroups: inv.subgroups, articles: [...inv.articles],
    flags: [...visit.flags].map(([k, m]) => [k, [...m]]), ownFlagged: [...visit.ownFlagged], rabSum: [...visit.rabSum],
  };
  fs.writeFileSync(cacheFile, JSON.stringify(cache), 'utf8');
  return fromCache(cache);
};

/** Сколько версий каждого файла сопоставилось с загруженными. */
const coverageOf = (keys, index, skippedNames) => {
  const all = [...new Set(keys)];
  const excluded = all.filter((k) => !index.has(k) && skippedNames.has(k.split('|')[0]));
  const missing = all.filter((k) => !index.has(k) && !skippedNames.has(k.split('|')[0]));
  return { total: all.length, matched: all.filter((k) => index.has(k)).length, excluded: excluded.length, missing };
};

const main = async () => {
  const outDir = args.out;
  const parts = list(args.part);
  for (const p of parts) if (!PARTS.includes(p)) throw new Error(`--part: неизвестная часть «${p}» (есть: ${PARTS.join(', ')})`);
  const only = list(args.only);
  const file = (k) => path.join(args.dir, FILES[k]);

  fs.mkdirSync(outDir, { recursive: true });
  const { inv, visit } = await loadSource(args.csv, outDir);
  const manifest = JSON.parse(fs.readFileSync(path.join(outDir, 'manifest.json'), 'utf8'));
  const index = buildVersionIndex(inv, manifest);
  const loaded = sum(Object.values(manifest.tenders).map((t) => Object.values(t.versions).filter((v) => v.status === 'loaded').length));
  if (index.size !== loaded) throw new Error(`сопоставлено ${index.size} версий из ${loaded} загруженных (manifest.json)`);
  for (const n of only) if (![...index.values()].some((v) => v.number === n)) throw new Error(`--only: номера ${n} нет среди загруженных`);
  const targets = [...index.entries()]
    .filter(([, v]) => !only.length || only.includes(v.number))
    .sort(([, a], [, b]) => a.number.localeCompare(b.number, 'ru', { numeric: true }) || a.version - b.version);

  const markups = readMarkups(file('markups'));
  const fixed = readFixedCosts(file('fixed'));
  const linkRows = readCsv(file('links'));
  const links = new Map(linkRows.map((r) => [versionKey(r.tender_name, r.version_name), r]));
  const volumeRows = readCsv(file('volumes'));
  const volumesBy = new Map();
  for (const r of volumeRows) {
    const k = versionKey(r.tender_name, r.version_name);
    if (!volumesBy.has(k)) volumesBy.set(k, []);
    volumesBy.get(k).push(r);
  }
  const skippedNames = new Set([...inv.skipped.keys()]);
  const coverage = {
    [FILES.markups]: coverageOf([...markups.keys()], index, skippedNames),
    [FILES.fixed]: coverageOf([...fixed.keys()], index, skippedNames),
    [FILES.links]: coverageOf([...links.keys()], index, skippedNames),
    [FILES.volumes]: coverageOf([...volumesBy.keys()], index, skippedNames),
  };

  const api = new HubApi({
    baseUrl: process.env.TENDERHUB_API_URL ?? 'https://tender.su10.ru',
    email: process.env.TENDERHUB_EMAIL,
    password: process.env.TENDERHUB_PASSWORD,
  });
  log('Справочники портала…');
  const [tenders, cats, detailsRaw, allParams, tactic] = await Promise.all([
    api.listTenders(), api.listCostCategories(), api.listDetailCostCategories(), api.listMarkupParameters(), api.globalTactic(TACTIC_NAME),
  ]);
  if (!tactic?.id) throw new Error(`тактика «${TACTIC_NAME}» не найдена`);
  const params = allParams.filter((p) => p.is_active !== false).sort((a, b) => (a.order_num ?? 0) - (b.order_num ?? 0));
  const catName = new Map(cats.map((c) => [c.id, c.name]));
  const details = detailsRaw.map((d) => ({ id: d.id, name: d.name, location: d.location ?? null, categoryName: catName.get(d.cost_category_id) ?? '' }));
  const label = new Map(details.map((d) => [d.id, detailLabel(d)]));
  const allDetailIds = details.map((d) => d.id);

  // Статьи 1С → виды затрат: как при загрузке строк (те же решения и автоподбор).
  const maps = buildMaps(inv, { unitCodes: [], details, outDir });
  const known = new Map();
  for (const c of maps.costReport) if (c.code && (!known.has(c.code) || (!known.get(c.code) && c.detail_cost_category_id))) known.set(c.code, c.detail_cost_category_id);
  const resolve = makeCostResolver({ overrides: readOverrides(outDir, 'cost-map.json'), known, details, groups: inv.groups, subgroups: inv.subgroups });

  // Σ «раб» и суб-строки без вида затрат — из сметы версии (тяжёлый запрос);
  // кэш по base_total (Σ прямых затрат строк): наши записи наценок его не меняют.
  // Записи без base (старый ключ) приняты как есть — смета с тех пор не менялась.
  const sumsFile = path.join(outDir, SUMS_CACHE_FILE);
  const sumsCache = fs.existsSync(sumsFile) ? JSON.parse(fs.readFileSync(sumsFile, 'utf8')) : {};
  const sumsOf = async (tender) => {
    const base = Math.round(Number(tender.base_total ?? 0) * 100) / 100;
    const c = sumsCache[tender.id];
    if (c && (c.base === undefined || c.base === base)) {
      c.base = base;
      return c;
    }
    const items = await api.boqItemsFull(tender.id);
    const total = (pred) => sum(items.filter(pred).map((it) => Number(it.total_amount ?? 0)));
    const s = {
      base,
      worksSum: total((it) => it.boq_item_type === 'раб'),
      noCategory: total((it) => (it.boq_item_type === 'суб-раб' || it.boq_item_type === 'суб-мат') && !it.detail_cost_category_id),
    };
    sumsCache[tender.id] = s;
    return s;
  };

  const needItems = parts.includes('growth') || parts.includes('markups');
  log(`Состояние ${targets.length} версий на портале…`);
  const plans = await pool(targets, READ_CONCURRENCY, async ([key, v]) => {
    const tender = tenders.find((t) => t.id === v.id);
    if (!tender) throw new Error(`${v.number} v${v.version}: тендера ${v.id} нет на портале`);
    const [curPct, curExcl, curVol, sums] = await Promise.all([
      api.listPercentages(v.id), api.listExclusions(v.id), api.listCostVolumes(v.id), needItems ? sumsOf(tender) : { worksSum: 0, noCategory: 0 },
    ]);

    // Поля
    const patch = links.has(key) ? buildFieldsPatch(links.get(key)) : {};
    const changed = Object.fromEntries(Object.entries(patch).filter(([k, val]) => !sameValue(tender[k], val)).map(([k, val]) => [k, [tender[k] ?? null, val]]));

    // Объёмы
    const built = buildVolumeRows(volumesBy.get(key) ?? [], v.id, resolve);
    const rows = built.rows.filter((r) => {
      if (!r.group_key || groupKeyExists(r.group_key, cats, detailsRaw)) return true;
      built.skipped.push({ code: r.group_key, name: `нет группы на портале: ${r.group_key}`, qty: r.volume, note: r.notes });
      return false;
    });
    const volKey = (r) => (r.group_key ? `g:${r.group_key}` : `d:${r.detail_cost_category_id}`);
    const curVolMap = new Map(curVol.map((r) => [volKey(r), r]));
    const volChanged = rows.filter((r) => {
      const c = curVolMap.get(volKey(r));
      return !c || Math.abs(Number(c.volume ?? 0) - r.volume) > 1e-9 || (c.notes ?? null) !== (r.notes ?? null);
    });

    // Рост субподряда
    const byCode = visit.flags.get(key) ?? new Map();
    let enabledW;
    let enabledM;
    let source;
    const unmapped = [];
    if (fixed.has(key)) {
      const set = new Set();
      for (const [code, name] of fixed.get(key)) {
        if (Object.prototype.hasOwnProperty.call(GROUP_KEYS, code)) continue; // строка-группа: её статьи перечислены отдельно
        const id = resolve(code, `${code} ${name}`);
        if (id) set.add(id); else unmapped.push(`${code} ${name}`);
      }
      enabledW = set;
      enabledM = set;
      source = 'файл затрат';
    } else {
      enabledW = growthByMajority(byCode, resolve).enabled;
      enabledM = new Set();
      source = [...byCode.values()].some((e) => e.flaggedRows) ? '«1» в 1С' : 'нет «1» — без роста';
    }
    const stats = { subTotal: 0, flagged: 0, covered: 0, coveredFlagged: 0, noCategory: 0 };
    for (const [code, e] of byCode) {
      stats.subTotal += e.total;
      stats.flagged += e.flagged;
      const id = resolve(code, e.raw);
      if (id && enabledW.has(id)) {
        stats.covered += e.total;
        stats.coveredFlagged += e.flagged;
      }
    }
    stats.noCategory = sums.noCategory;
    const desired = exclusionRows(v.id, allDetailIds, enabledW, enabledM);
    const exKey = (r) => `${r.detail_cost_category_id}|${r.exclusion_type}`;
    const curEx = new Set(curExcl.map(exKey));
    const desiredEx = new Set(desired.map(exKey));
    const toInsert = desired.filter((r) => !curEx.has(exKey(r)));
    const toDelete = { works: [], materials: [] };
    for (const r of curExcl) if (!desiredEx.has(exKey(r))) toDelete[r.exclusion_type]?.push(r.detail_cost_category_id);

    // Наценки
    const src = pickMarkupSource(key, v, index, markups);
    const srcData = src ? markups.get(src.key) : null;
    const area = markups.get(key)?.area ?? numComma(links.get(key)?.area_customer) ?? srcData?.area ?? null;
    const { worksSum } = sums;
    const { values, notes } = buildMarkupValues(params, srcData, area, worksSum);
    const curByParam = new Map(curPct.map((r) => [r.markup_parameter_id, Number(r.value)]));
    const pctChanged = curPct.length !== params.length
      || params.some((p) => !curByParam.has(p.id) || Math.abs(curByParam.get(p.id) - values[p.key]) > 1e-9);

    return {
      key, id: v.id, number: v.number, version: v.version, title: tender.title,
      fields: { patch, changed },
      volumes: { rows, skipped: built.skipped, changed: volChanged },
      growth: {
        source, enabledWorks: [...enabledW], enabledMaterials: [...enabledM], unmapped, desired: desired.length,
        toInsert, toDelete, toDeleteCount: toDelete.works.length + toDelete.materials.length, stats, own: visit.ownFlagged.get(key) ?? null,
      },
      markups: {
        source: src?.how ?? 'по умолчанию', area, worksSum, worksSum1c: visit.rabSum.get(key) ?? 0, values, notes,
        changed: pctChanged, tacticChanged: tender.markup_tactic_id !== tactic.id,
      },
    };
  });

  fs.writeFileSync(sumsFile, JSON.stringify(sumsCache), 'utf8');
  const mode = args.apply ? 'запись' : 'пробный прогон (только чтение)';
  const reportPath = path.join(outDir, 'extras-report.md');
  fs.writeFileSync(reportPath, renderExtrasReport({ mode, parts, plans, coverage, params, tactic, label }), 'utf8');
  log(`Отчёт: ${reportPath}`);
  const pending = {
    fields: plans.filter((pl) => Object.keys(pl.fields.changed).length).length,
    volumes: sum(plans.map((pl) => pl.volumes.changed.length)),
    growth: plans.filter((pl) => pl.growth.toInsert.length || pl.growth.toDeleteCount).length,
    markups: plans.filter((pl) => pl.markups.changed || pl.markups.tacticChanged).length,
  };
  log(`К записи: поля — ${pending.fields} версий, объёмы — ${pending.volumes} строк, рост — ${pending.growth} версий, наценки/тактика — ${pending.markups} версий`);
  if (!args.apply) return;

  const journalFile = path.join(outDir, 'extras-manifest.json');
  const journal = fs.existsSync(journalFile) ? JSON.parse(fs.readFileSync(journalFile, 'utf8')) : { runs: [], versions: {} };
  journal.runs.push({ at: new Date().toISOString(), parts, only });
  const save = () => fs.writeFileSync(journalFile, `${JSON.stringify(journal, null, 2)}\n`, 'utf8');
  for (const pl of plans) {
    const at = new Date().toISOString();
    const j = (journal.versions[pl.id] ??= { number: pl.number, version: pl.version });
    const done = [];
    if (parts.includes('fields') && Object.keys(pl.fields.changed).length) {
      const patch = Object.fromEntries(Object.keys(pl.fields.changed).map((k) => [k, pl.fields.patch[k]]));
      await retry(() => api.adminPatchTender(pl.id, patch));
      j.fields = { at, keys: Object.keys(patch) };
      done.push(`поля ${Object.keys(patch).length}`);
    }
    if (parts.includes('volumes') && pl.volumes.changed.length) {
      for (const r of pl.volumes.changed) {
        await retry(() => api.upsertCostVolume({ tender_id: r.tender_id, detail_cost_category_id: r.detail_cost_category_id, group_key: r.group_key, volume: r.volume, notes: r.notes }));
      }
      j.volumes = { at, rows: pl.volumes.changed.length };
      done.push(`объёмы ${pl.volumes.changed.length}`);
    }
    if (parts.includes('growth') && (pl.growth.toInsert.length || pl.growth.toDeleteCount)) {
      for (const [type, ids] of Object.entries(pl.growth.toDelete)) if (ids.length) await retry(() => api.deleteExclusions(pl.id, ids, type));
      if (pl.growth.toInsert.length) await retry(() => api.insertExclusions(pl.growth.toInsert));
      j.growth = { at, source: pl.growth.source, inserted: pl.growth.toInsert.length, deleted: pl.growth.toDeleteCount, enabledWorks: pl.growth.enabledWorks.length };
      done.push(`исключения +${pl.growth.toInsert.length}/−${pl.growth.toDeleteCount}`);
    }
    if (parts.includes('markups')) {
      if (pl.markups.changed) {
        await retry(() => api.putPercentages(pl.id, params.map((p) => ({ tender_id: pl.id, markup_parameter_id: p.id, value: pl.markups.values[p.key] }))));
        j.markups = { at, source: pl.markups.source, values: pl.markups.values };
        done.push('проценты');
      }
      if (pl.markups.tacticChanged) {
        await retry(() => api.putTacticId(pl.id, tactic.id));
        j.tactic = { at, id: tactic.id };
        done.push('тактика');
      }
    }
    save();
    log(`${pl.number} v${pl.version}: ${done.length ? done.join(', ') : 'без изменений'}`);
  }
  log('Готово. Пересчёт и «Проверка данных» идут на сервере в фоне.');
};

main().catch((err) => {
  console.error(`ОШИБКА: ${err.message}`);
  process.exitCode = 1;
});
