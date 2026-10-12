#!/usr/bin/env node
// Перенос строк одной статьи 1С в другой вид затрат на уже загруженных версиях
// (ошибка сопоставления при загрузке: «17.03 Благоустройство/ Металлоконструкции»
// легла в «МЕТАЛЛИЧЕСКИЕ КОНСТРУКЦИИ / Металлоконструкции / Здание»).
// Строка 1С находится на портале точно: позиция её блока + порядковый номер в
// позиции (sort_number идёт по порядку импорта); проверяются число строк
// позиции, тип строки и текущий вид затрат.
//
//   nice -n 19 node scripts/import-1c/recat.mjs --code 000000161 --from <id> --to <id>   план → out/recat-plan.json
//   node scripts/import-1c/recat.mjs --apply                                            запись по плану

import fs from 'node:fs';
import path from 'node:path';
import { parseArgs } from 'node:util';
import { HubApi } from './api.mjs';
import { inventory, buildMaps, readOverrides } from './inventory.mjs';
import { forEachBuiltVersion } from './prepare.mjs';
import { readDopReview } from './dopreview.mjs';
import { str } from './csv.mjs';

const { values: args } = parseArgs({
  options: {
    csv: { type: 'string', default: 'Результаты.csv' },
    out: { type: 'string', default: 'scripts/import-1c/out' },
    code: { type: 'string' },
    from: { type: 'string' },
    to: { type: 'string' },
    apply: { type: 'boolean', default: false },
  },
});

const log = (...a) => console.log(...a);
const PLAN_FILE = 'recat-plan.json';

const api = new HubApi({
  baseUrl: process.env.TENDERHUB_API_URL ?? 'https://tender.su10.ru',
  email: process.env.TENDERHUB_EMAIL,
  password: process.env.TENDERHUB_PASSWORD,
});

/** Строки статьи по версиям: блок (позиция или ДОП) + индекс строки в нём. */
const collectHits = async (outDir, manifest) => {
  log(`Читаю ${args.csv}…`);
  const inv = await inventory(args.csv);
  const [units, cats, details] = await Promise.all([api.listUnits(), api.listCostCategories(), api.listDetailCostCategories()]);
  const catName = new Map(cats.map((c) => [c.id, c.name]));
  const joined = details.map((d) => ({ id: d.id, name: d.name, location: d.location ?? null, categoryName: catName.get(d.cost_category_id) ?? '' }));
  const maps = buildMaps(inv, { unitCodes: units.map((u) => u.code), details: joined, outDir });
  const dopOverrides = { ...readDopReview(outDir), ...readOverrides(outDir, 'dop-map.json') };
  const selected = new Set(Object.keys(manifest.tenders));
  const versions = [];
  log('Собираю версии (как при загрузке)…');
  await forEachBuiltVersion({ csvPath: args.csv, inv, maps, selected, dopOverrides }, (tender, version, built, batch) => {
    const entry = manifest.tenders[tender.number]?.versions?.[version.version];
    if (entry?.status !== 'loaded') return;
    const codeOf = new Map(batch.rows.map((r) => [r._line, (str(r.cost_category) ?? '').split(/\s+/)[0]]));
    const hits = [];
    for (const u of built.units) {
      const where = u.target.kind === 'pos'
        ? { position_number: built.positions[u.target.index].position_number }
        : { parent_number: built.positions[built.dops[u.target.index].parent_index].position_number, work_name: built.dops[u.target.index].work_name };
      u.items.forEach((it, k) => {
        if (codeOf.get(it.row_index) === args.code) hits.push({ where, k, n: u.items.length, type: it.boq_item_type, name: it.name, line: it.row_index });
      });
    }
    if (hits.length) versions.push({ number: tender.number, version: version.version, tenderId: entry.id, hits });
  });
  return versions;
};

const buildPlan = async (outDir) => {
  if (!args.code || !args.from || !args.to) throw new Error('нужны --code, --from, --to');
  const manifest = JSON.parse(fs.readFileSync(path.join(outDir, 'manifest.json'), 'utf8'));
  const versions = await collectHits(outDir, manifest);
  const items = [];
  const problems = [];
  for (const v of versions) {
    const positions = await api.listPositions(v.tenderId);
    const byNumber = new Map(positions.map((p) => [Number(p.position_number), p]));
    const cache = new Map();
    for (const h of v.hits) {
      let pos;
      if (h.where.work_name === undefined) pos = byNumber.get(Number(h.where.position_number));
      else {
        const parent = byNumber.get(Number(h.where.parent_number));
        pos = parent && positions.find((p) => p.parent_position_id === parent.id && p.work_name === h.where.work_name);
      }
      const tag = `${v.number} v${v.version}, строка 1С ${h.line} «${h.name}»`;
      if (!pos) {
        problems.push(`${tag}: позиция не найдена`);
        continue;
      }
      if (!cache.has(pos.id)) cache.set(pos.id, await api.positionItems(v.tenderId, pos.id));
      const list = cache.get(pos.id);
      const it = list[h.k];
      if (list.length !== h.n) problems.push(`${tag}: в позиции ${pos.position_number} строк ${list.length}, при загрузке было ${h.n} — пропущено`);
      else if (!it || it.boq_item_type !== h.type) problems.push(`${tag}: на месте ${h.k} другой тип строки — пропущено`);
      else if (it.detail_cost_category_id === args.to) problems.push(`${tag}: уже перенесена`);
      else if (it.detail_cost_category_id !== args.from) problems.push(`${tag}: вид затрат на портале другой (${it.detail_cost_category_id}) — пропущено`);
      else items.push({ id: it.id, number: v.number, version: v.version, position: pos.position_number, type: it.boq_item_type, name: h.name, total: it.total_amount });
    }
    log(`${v.number} v${v.version}: строк статьи ${v.hits.length}`);
  }
  const plan = { code: args.code, from: args.from, to: args.to, createdAt: new Date().toISOString(), items, problems, done: [] };
  fs.writeFileSync(path.join(outDir, PLAN_FILE), `${JSON.stringify(plan, null, 2)}\n`, 'utf8');
  const total = items.reduce((s, x) => s + Number(x.total ?? 0), 0);
  log(`План: ${items.length} строк на ${Math.round(total).toLocaleString('ru-RU')} ₽, проблем ${problems.length} → ${path.join(outDir, PLAN_FILE)}`);
  for (const p of problems.slice(0, 30)) log(`  ! ${p}`);
};

const applyPlan = async (outDir) => {
  const file = path.join(outDir, PLAN_FILE);
  const plan = JSON.parse(fs.readFileSync(file, 'utf8'));
  const done = new Set(plan.done);
  let n = 0;
  for (const x of plan.items) {
    if (done.has(x.id)) continue;
    const { item, etag } = await api.itemWithEtag(x.id);
    if (item.detail_cost_category_id === plan.to) {
      done.add(x.id);
      continue;
    }
    if (item.detail_cost_category_id !== plan.from) throw new Error(`${x.number} v${x.version} «${x.name}»: вид затрат изменился на портале — стоп`);
    await api.patchItem(x.id, { detail_cost_category_id: plan.to }, etag);
    done.add(x.id);
    n += 1;
    if (n % 50 === 0) {
      plan.done = [...done];
      fs.writeFileSync(file, `${JSON.stringify(plan, null, 2)}\n`, 'utf8');
      log(`  перенесено ${n}`);
    }
  }
  plan.done = [...done];
  plan.appliedAt = new Date().toISOString();
  fs.writeFileSync(file, `${JSON.stringify(plan, null, 2)}\n`, 'utf8');
  log(`Готово: перенесено ${n}, всего в плане ${plan.items.length}.`);
};

(args.apply ? applyPlan(args.out) : buildPlan(args.out)).catch((err) => {
  console.error(`ОШИБКА: ${err.message}`);
  process.exitCode = 1;
});
