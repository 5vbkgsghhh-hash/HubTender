// Второй проход: собираем каждую выбранную версию (model.buildVersion) без
// записи в API — для отчёта, проверки блокеров и списка наименований.
// Тот же обход (forEachBuiltVersion) используется при боевой загрузке.

import { readVersions } from './csv.mjs';
import { buildVersion } from './model.mjs';
import { canonKey } from './names.mjs';

/** Ключ наименования для сопоставления со справочником (см. names.mjs). */
export const nameKey = canonKey;

/**
 * Обходит выгрузку и отдаёт собранные версии выбранных тендеров по порядку файла.
 * @param {(tender: object, version: object, built: ReturnType<typeof buildVersion>, batch: object) => Promise<void>|void} cb
 */
export const forEachBuiltVersion = async ({ csvPath, inv, maps, selected, dopOverrides }, cb) => {
  const byName = new Map(inv.tenders.map((t) => [t.name, t]));
  const prevDop = new Map();
  for await (const batch of readVersions(csvPath)) {
    const tender = byName.get(batch.tenderName);
    if (!tender || !selected.has(tender.number)) continue;
    const version = tender.versions.find((v) => v.name === batch.versionName);
    const built = buildVersion(batch, {
      itemUnitOf: maps.itemUnitOf,
      customerUnitOf: maps.customerUnitOf,
      nameUnit: maps.nameUnit,
      costOf: maps.costOf,
      dopOverrides,
      prevDop: prevDop.get(tender.name) ?? new Map(),
    });
    const carried = new Map([...(prevDop.get(tender.name) ?? new Map()), ...built.nextDop]);
    prevDop.set(tender.name, carried);
    await cb(tender, version, built, batch);
  }
};

/** Сводка по всем выбранным версиям: статистика, наименования, валюты, курсы. */
export const prepareAll = async (opts) => {
  const versions = [];
  const names = new Map();
  const usedUnits = new Map();
  const unresolved = [];
  const reviewDops = [];
  const noUnitItems = [];

  const useUnit = (code) => { if (code) usedUnits.set(code, (usedUnits.get(code) ?? 0) + 1); };

  await forEachBuiltVersion(opts, (tender, version, built) => {
    const currencies = new Set();
    for (const p of built.positions) useUnit(p.unit_code);
    for (const d of built.dops) useUnit(d.unit_code);
    for (const u of built.units) {
      for (const it of u.items) {
        if (it.currency_type && it.currency_type !== 'RUB') currencies.add(it.currency_type);
        if (!it.unit_code) {
          noUnitItems.push({ tender: tender.number, version: version.version, name: it.name, row: it.row_index });
          continue;
        }
        useUnit(it.unit_code);
        const key = nameKey(it.kind, it.name, it.unit_code);
        if (!names.has(key)) names.set(key, { kind: it.kind, name: it.name, unit: it.unit_code, rows: 0, spellings: new Map() });
        const entry = names.get(key);
        entry.rows += 1;
        entry.spellings.set(it.name, (entry.spellings.get(it.name) ?? 0) + 1);
      }
    }
    for (const u of built.stats.unresolved) unresolved.push({ tender: tender.number, version: version.version, ...u });
    for (const d of built.dops) {
      if (d.how !== 'ключ' && d.how !== 'вручную') {
        reviewDops.push({ tender: tender.number, version: version.version, key: d.key, name: d.work_name, how: d.how,
          section_no: d.section_no, parent_line: d.parent_line, candidates: d.candidates,
          parent: built.positions[d.parent_index]?.item_no, parentName: built.positions[d.parent_index]?.work_name });
      }
    }
    versions.push({
      tender: tender.number,
      tenderName: tender.name,
      title: tender.title,
      version: version.version,
      label: version.label,
      rates: built.rates,
      currencies: [...currencies],
      stats: built.stats,
    });
  });

  // Новое наименование заводим в самом частом написании 1С.
  for (const entry of names.values()) {
    entry.name = [...entry.spellings.entries()].sort((a, b) => b[1] - a[1])[0][0];
    delete entry.spellings;
  }
  fillMissingRates(versions);
  return { versions, names, usedUnits, unresolved, reviewDops, noUnitItems };
};

/**
 * Валюта используется в версии, но курс не вычислился (все строки с нулевым
 * объёмом) — берём курс ближайшей версии того же тендера, иначе медиану по
 * всем версиям. Сервер считает суммы fail-closed и без курса импорт упадёт.
 */
const fillMissingRates = (versions) => {
  const all = {};
  for (const v of versions) {
    for (const [cur, r] of Object.entries(v.rates)) (all[cur] ??= []).push(r.rate);
  }
  const median = (xs) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)];
  for (const v of versions) {
    for (const cur of v.currencies) {
      if (v.rates[cur]) continue;
      const same = versions
        .filter((x) => x.tender === v.tender && x.rates[cur] && !x.rates[cur].borrowed)
        .sort((a, b) => Math.abs(a.version - v.version) - Math.abs(b.version - v.version));
      if (same.length) {
        v.rates[cur] = { rate: same[0].rates[cur].rate, rows: 0, borrowed: `версия ${same[0].version}` };
      } else if (all[cur]?.length) {
        v.rates[cur] = { rate: median(all[cur]), rows: 0, borrowed: 'медиана по выгрузке' };
      }
    }
  }
};
