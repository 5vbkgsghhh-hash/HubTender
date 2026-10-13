// Боевая загрузка (--commit): все записи — через существующие эндпоинты Go BFF,
// деньги считает сервер (контрольный total_amount только для сверки).
// Порядок: замена «старого» номера → единицы → наименования → оболочки версий
// (от старшей к младшей: POST всегда создаёт version=1) → заполнение →
// архив строк «Перечня» для новых номеров. Всё созданное — в out/manifest.json.

import fs from 'node:fs';
import path from 'node:path';
import { forEachBuiltVersion, nameKey } from './prepare.mjs';

const FILE_NAME = 'Результаты.csv (выгрузка 1С)';
const POSITIONS_CHUNK = 2000;
const IMPORT_CHUNK_BYTES = 900_000;
const IMPORT_CHUNK_ITEMS = 3000;
const NAME_CONCURRENCY = 6;

const manifestPath = (outDir) => path.join(outDir, 'manifest.json');
const readManifest = (outDir) => (fs.existsSync(manifestPath(outDir))
  ? JSON.parse(fs.readFileSync(manifestPath(outDir), 'utf8'))
  : { runs: [], tenders: {}, renamed: [], registryArchived: [] });
const saveManifest = (outDir, m) => fs.writeFileSync(manifestPath(outDir), `${JSON.stringify(m, null, 2)}\n`, 'utf8');

const pool = async (items, limit, fn) => {
  let i = 0;
  const workers = Array.from({ length: Math.min(limit, items.length) }, async () => {
    while (i < items.length) {
      const idx = i;
      i += 1;
      await fn(items[idx], idx);
    }
  });
  await Promise.all(workers);
};

/** Проверки перед заменой: проекты, ссылающиеся на старые версии, и хронология. */
export const replacePreflight = async ({ api, refs, replace }) => {
  const problems = [];
  const old = [];
  if (!replace.length) return { problems, old };
  const projects = await api.listProjects();
  for (const r of replace) {
    for (const t of refs.tenders.filter((x) => x.tender_number === r.old)) {
      const proj = projects.find((p) => p.tender_id === t.id);
      if (proj) problems.push(`${r.old} v${t.version}: на тендер ссылается проект «${proj.name ?? proj.id}»`);
      const groups = await api.listTimelineGroups(t.id).catch(() => []);
      old.push({ id: t.id, number: r.old, version: t.version, title: t.title, timelineGroups: groups.length });
    }
  }
  return { problems, old };
};

const toApiItem = (it, { positionId, dopTempId, nameIds }) => {
  const work = it.kind === 'work';
  const nameId = nameIds.get(nameKey(it.kind, it.name, it.unit_code));
  if (!nameId) throw new Error(`строка ${it.row_index}: нет наименования «${it.name}» [${it.unit_code}]`);
  return {
    row_index: it.row_index,
    client_position_id: positionId ?? '',
    client_position_temp_id: dopTempId ?? null,
    temp_id: work ? it.temp_id : null,
    parent_work_temp_id: work ? null : it.parent_work_temp_id,
    boq_item_type: it.boq_item_type,
    work_name_id: work ? nameId : null,
    material_name_id: work ? null : nameId,
    unit_code: it.unit_code,
    quantity: it.quantity,
    base_quantity: it.base_quantity,
    conversion_coefficient: work ? null : it.conversion_coefficient,
    consumption_coefficient: work ? null : it.consumption_coefficient,
    unit_rate: it.unit_rate,
    currency_type: it.currency_type,
    total_amount: it.total_amount,
    delivery_price_type: work ? null : it.delivery_price_type,
    delivery_amount: work ? null : it.delivery_amount,
    quote_link: it.quote_link,
    quote_price_date: it.quote_price_date,
    detail_cost_category_id: it.detail_cost_category_id,
    material_type: work ? null : it.material_type,
    description: it.description ?? null,
  };
};

/**
 * Заполняет оболочку версии позициями, ДОП и строками; возвращает сверку.
 * chunkItems — предел строк в одном запросе импорта (блок позиции не разрывается).
 * pauseMs — пауза между порциями: не меньше этого и не меньше времени самой порции (до 30 с),
 * чтобы сервер успел закончить автопересчёт тендера; 0 — без паузы.
 */
export const fillVersion = async ({ api, tenderId, built, nameIds, log, fileName = FILE_NAME, chunkItems = IMPORT_CHUNK_ITEMS, pauseMs = 0 }) => {
  const rows = built.positions.map((p) => ({
    tender_id: tenderId,
    position_number: p.position_number,
    work_name: p.work_name,
    unit_code: p.unit_code,
    volume: p.volume,
    client_note: p.client_note,
    item_no: p.item_no,
    hierarchy_level: p.hierarchy_level,
    is_additional: false,
  }));
  for (let i = 0; i < rows.length; i += POSITIONS_CHUNK) {
    await api.bulkPositions(tenderId, rows.slice(i, i + POSITIONS_CHUNK));
  }
  const listed = await api.listPositions(tenderId);
  const idByNumber = new Map(listed.map((p) => [Number(p.position_number), p.id]));
  if (idByNumber.size !== rows.length) throw new Error(`позиций на сервере ${idByNumber.size}, отправлено ${rows.length}`);
  const posId = (index) => idByNumber.get(built.positions[index].position_number);

  const chunks = [];
  let cur = null;
  const flush = () => { if (cur && (cur.items.length || cur.position_updates.length || cur.additional_positions.length)) chunks.push(cur); cur = null; };
  for (const unit of built.units) {
    const apiItems = [];
    let extra = null;
    if (unit.target.kind === 'pos') {
      const id = posId(unit.target.index);
      const p = built.positions[unit.target.index];
      extra = { update: { position_id: id, manual_volume: p.manual_volume, manual_note: p.manual_note } };
      for (const it of unit.items) apiItems.push(toApiItem(it, { positionId: id, nameIds }));
    } else {
      const d = built.dops[unit.target.index];
      extra = { dop: { row_index: null, temp_id: d.temp_id, parent_position_id: posId(d.parent_index), work_name: d.work_name,
        unit_code: d.unit_code, manual_volume: d.manual_volume, manual_note: d.manual_note } };
      for (const it of unit.items) apiItems.push(toApiItem(it, { dopTempId: d.temp_id, nameIds }));
    }
    const size = JSON.stringify(apiItems).length + 400;
    if (cur && (cur.bytes + size > IMPORT_CHUNK_BYTES || cur.items.length + apiItems.length > chunkItems)) flush();
    cur ??= { items: [], position_updates: [], additional_positions: [], bytes: 0 };
    cur.items.push(...apiItems);
    if (extra.update) cur.position_updates.push(extra.update);
    if (extra.dop) cur.additional_positions.push(extra.dop);
    cur.bytes += size;
  }
  flush();

  let inserted = 0;
  let mismatchCount = 0;
  const mismatches = [];
  for (const [i, c] of chunks.entries()) {
    const startedAt = Date.now();
    const res = await api.importBoq({
      tender_id: tenderId,
      file_name: fileName,
      items: c.items,
      position_updates: c.position_updates,
      additional_positions: c.additional_positions,
    });
    if (res.inserted_items_count !== c.items.length) {
      throw new Error(`порция ${i + 1}: вставлено ${res.inserted_items_count} из ${c.items.length}`);
    }
    inserted += res.inserted_items_count;
    mismatchCount += res.total_mismatch_count ?? 0;
    for (const m of res.total_mismatches ?? []) if (mismatches.length < 30) mismatches.push(m);
    const tookMs = Date.now() - startedAt;
    log(`    порция ${i + 1}/${chunks.length}: ${c.items.length} строк, ${(tookMs / 1000).toFixed(1)} с`);
    if (pauseMs > 0 && i < chunks.length - 1) {
      await new Promise((resolve) => { setTimeout(resolve, Math.min(Math.max(pauseMs, tookMs), 30_000)); });
    }
  }

  const after = await api.listPositions(tenderId);
  const serverTotal = after.reduce((s, p) => s + (p.total_material ?? 0) + (p.total_works ?? 0), 0);
  return {
    positions: after.length,
    inserted,
    mismatchCount,
    mismatches,
    total1c: Math.round(built.stats.amount1c * 100) / 100,
    serverTotal: Math.round(serverTotal * 100) / 100,
  };
};

export const commitLoad = async ({ api, csvPath, inv, maps, prepared, refs, selected, replace, dopOverrides, outDir, log }) => {
  const manifest = readManifest(outDir);
  const run = { startedAt: new Date().toISOString(), baseUrl: api.baseUrl, selected: [...selected] };
  manifest.runs.push(run);
  saveManifest(outDir, manifest);

  const pre = await replacePreflight({ api, refs, replace });
  if (pre.problems.length) throw new Error(`замена невозможна:\n- ${pre.problems.join('\n- ')}`);
  for (const t of pre.old) {
    log(`Переименовываю старую версию ${t.number} v${t.version} → «${t.number}-old» (хронология: ${t.timelineGroups} групп)`);
    await api.adminPatchTender(t.id, { tender_number: `${t.number}-old` });
    manifest.renamed.push({ id: t.id, from: t.number, to: `${t.number}-old`, version: t.version });
    saveManifest(outDir, manifest);
  }

  if (refs.missingUnits.length) {
    log(`Заводим единицы: ${refs.missingUnits.join(', ')}`);
    await api.importUnits(refs.missingUnits.map((code) => ({ code, name: code, category: 'custom', sort_order: 999, is_active: true })));
    manifest.unitsCreated = [...(manifest.unitsCreated ?? []), ...refs.missingUnits];
    saveManifest(outDir, manifest);
  }

  const missingNames = [...prepared.names.entries()].filter(([k]) => !refs.nameIds.has(k)).map(([, v]) => v);
  log(`Создаю наименования: ${missingNames.length}`);
  let done = 0;
  await pool(missingNames, NAME_CONCURRENCY, async (n) => {
    await api.createName(n.kind, n.name, n.unit);
    done += 1;
    if (done % 1000 === 0) log(`  …${done}`);
  });
  manifest.namesCreated = (manifest.namesCreated ?? 0) + missingNames.length;
  saveManifest(outDir, manifest);
  const nameIds = new Map();
  for (const kind of ['work', 'material']) {
    for (const n of await api.listNames(kind)) {
      const k = nameKey(kind, n.name, n.unit);
      if (!nameIds.has(k)) nameIds.set(k, n.id);
    }
  }
  const lost = [...prepared.names.keys()].filter((k) => !nameIds.has(k));
  if (lost.length) throw new Error(`после создания не найдено ${lost.length} наименований (пример: ${lost[0]})`);

  const registryBefore = new Set(refs.registry.map((r) => r.tender_number).filter(Boolean));
  // Номера, которые уже были в «Перечне»: строки остальных создаст триггер
  // при POST /tenders — их архивируем в конце или при откате.
  manifest.registryBefore = [...new Set([...(manifest.registryBefore ?? []), ...registryBefore])];
  saveManifest(outDir, manifest);
  const registryClient = new Map(refs.registry.filter((r) => r.tender_number).map((r) => [r.tender_number, r.client_name]));
  const versionInfo = new Map(prepared.versions.map((v) => [`${v.tender}|${v.version}`, v]));

  for (const t of inv.tenders.filter((x) => selected.has(x.number))) {
    manifest.tenders[t.number] ??= { title: t.title, versions: {} };
    const clientName = registryClient.get(t.baseNumber) ?? t.clientHint ?? '—';
    for (const v of [...t.versions].sort((a, b) => b.version - a.version)) {
      const info = versionInfo.get(`${t.number}|${v.version}`);
      const rate = (c) => info?.rates?.[c]?.rate;
      const created = await api.createTender({
        tender_number: t.number,
        title: t.title,
        client_name: clientName,
        usd_rate: rate('USD'),
        eur_rate: rate('EUR'),
        cny_rate: rate('CNY'),
      });
      manifest.tenders[t.number].versions[v.version] = { id: created.id, status: 'shell' };
      saveManifest(outDir, manifest);
      await api.adminPatchTender(created.id, { version: v.version, is_archived: true });
      log(`Оболочка ${t.number} v${v.version}: ${created.id}`);
    }
  }

  // Строки «Перечня» для новых номеров создал триггер — сразу в архив,
  // чтобы на время заполнения они не висели в рабочем списке.
  await archiveNewRegistry({ api, manifest, registryBefore, outDir, log });

  await forEachBuiltVersion({ csvPath, inv, maps, selected, dopOverrides }, async (tender, version, built) => {
    const entry = manifest.tenders[tender.number].versions[version.version];
    log(`Загружаю ${tender.number} v${version.version} «${tender.title}»…`);
    try {
      const res = await fillVersion({ api, tenderId: entry.id, built, nameIds, log });
      Object.assign(entry, { status: 'loaded', ...res });
      log(`  ✓ позиций ${res.positions}, строк ${res.inserted}, Σ сервер ${res.serverTotal} / Σ 1С ${res.total1c}, расхождений ${res.mismatchCount}`);
    } catch (err) {
      Object.assign(entry, { status: 'failed', error: err.message });
      log(`  ✗ ${err.message} — удаляю версию`);
      await api.deleteTender(entry.id).then(() => { entry.status = 'failed-deleted'; }).catch((e) => { entry.deleteError = e.message; });
    }
    saveManifest(outDir, manifest);
  });

  await archiveNewRegistry({ api, manifest, registryBefore, outDir, log });
  run.finishedAt = new Date().toISOString();
  saveManifest(outDir, manifest);
  log(`Готово. Манифест: ${manifestPath(outDir)}`);
};

/** Архивирует строки «Перечня», созданные триггером для номеров этой загрузки. */
const archiveNewRegistry = async ({ api, manifest, registryBefore, outDir, log }) => {
  const loadedNumbers = new Set(Object.keys(manifest.tenders));
  for (const r of await api.listRegistry()) {
    if (r.tender_number && loadedNumbers.has(r.tender_number) && !registryBefore.has(r.tender_number) && !r.is_archived) {
      await api.patchRegistry(r.id, { is_archived: true });
      manifest.registryArchived.push({ id: r.id, tender_number: r.tender_number });
      log(`«Перечень»: строка ${r.tender_number} в архиве`);
    }
  }
  saveManifest(outDir, manifest);
};

/** Удаляет старые версии «<номер>-old» после проверки замены. */
export const purgeOld = async ({ api, number, yes, outDir, log }) => {
  const old = (await api.listTenders()).filter((t) => t.tender_number === `${number}-old`);
  if (!old.length) { log(`«${number}-old» на портале нет`); return; }
  for (const t of old) log(`  ${t.tender_number} v${t.version} «${t.title}» ${t.id}`);
  if (!yes) { log('Добавьте --yes, чтобы удалить эти версии.'); return; }
  const manifest = readManifest(outDir);
  for (const t of old) {
    await api.deleteTender(t.id);
    (manifest.purged ??= []).push({ id: t.id, tender_number: t.tender_number, version: t.version });
    log(`  удалено: ${t.id}`);
  }
  saveManifest(outDir, manifest);
};
