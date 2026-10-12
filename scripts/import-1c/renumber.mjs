#!/usr/bin/env node
// Перенумерация загруженных из 1С тендеров (все версии номера) по карте
// «старый=новый». Порядок подбирается так, чтобы не нарушить
// UNIQUE (tender_number, version): 262.1→262, затем 262.2→262.1 и т.д.
// Строки «Перечня», созданные загрузкой, получают новые номер и название;
// если у нового номера уже есть «чужая» строка — наша остаётся без номера
// (API удаления строк «Перечня» нет). Обновляет out/manifest.json.
//
//   node scripts/import-1c/renumber.mjs --map "262.1=262,262.2=262.1" [--yes]

import fs from 'node:fs';
import path from 'node:path';
import { parseArgs } from 'node:util';
import { HubApi } from './api.mjs';

const { values: args } = parseArgs({
  options: {
    out: { type: 'string', default: 'scripts/import-1c/out' },
    map: { type: 'string' },
    yes: { type: 'boolean', default: false },
  },
});

const main = async () => {
  const file = path.join(args.out, 'manifest.json');
  const manifest = JSON.parse(fs.readFileSync(file, 'utf8'));
  const pairs = (args.map ?? '').split(',').map((s) => s.trim()).filter(Boolean).map((s) => {
    const [from, to] = s.split('=').map((x) => x.trim());
    if (!from || !to) throw new Error(`--map: «${s}» — нужно «старый=новый»`);
    if (!manifest.tenders[from]) throw new Error(`--map: номера ${from} нет в манифесте`);
    return { from, to };
  }).filter((p) => p.from !== p.to);
  if (!pairs.length) throw new Error('пустая карта --map');

  const api = new HubApi({
    baseUrl: process.env.TENDERHUB_API_URL ?? 'https://tender.su10.ru',
    email: process.env.TENDERHUB_EMAIL,
    password: process.env.TENDERHUB_PASSWORD,
  });
  const tenders = await api.listTenders();
  const ids = (n) => Object.values(manifest.tenders[n].versions).filter((v) => v.status === 'loaded').map((v) => v.id);
  const moving = new Set(pairs.flatMap((p) => ids(p.from)));
  for (const p of pairs) {
    const foreign = tenders.filter((t) => t.tender_number === p.to && !moving.has(t.id));
    const staying = tenders.filter((t) => t.tender_number === p.to && moving.has(t.id))
      .filter((t) => !pairs.some((q) => q.from === p.to));
    if (foreign.length || staying.length) throw new Error(`номер ${p.to} занят тендером «${(foreign[0] ?? staying[0]).title}»`);
  }

  // Порядок: сначала те, чей новый номер уже никем не занят.
  const occupied = new Set(tenders.map((t) => t.tender_number));
  const order = [];
  const pending = [...pairs];
  while (pending.length) {
    const i = pending.findIndex((p) => !occupied.has(p.to));
    if (i < 0) throw new Error(`цикл переименований: ${pending.map((p) => `${p.from}→${p.to}`).join(', ')}`);
    const [p] = pending.splice(i, 1);
    order.push(p);
    occupied.delete(p.from);
    occupied.add(p.to);
  }

  const ourRegistry = new Set((manifest.registryArchived ?? []).map((r) => r.id));
  const registry = await api.listRegistry();
  console.log('Порядок:');
  for (const p of order) console.log(`  ${p.from} → ${p.to}  «${manifest.tenders[p.from].title}» (${ids(p.from).length} верс.)`);
  if (!args.yes) {
    console.log('Это предпросмотр. Добавьте --yes для выполнения.');
    return;
  }

  for (const p of order) {
    for (const id of ids(p.from)) await api.adminPatchTender(id, { tender_number: p.to });
    console.log(`тендер ${p.from} → ${p.to}`);
  }

  // Манифест: ключи — новые номера.
  const next = {};
  const renamed = new Map(pairs.map((p) => [p.from, p.to]));
  for (const [n, t] of Object.entries(manifest.tenders)) next[renamed.get(n) ?? n] = t;
  manifest.tenders = next;
  (manifest.renumbered ??= []).push({ at: new Date().toISOString(), pairs });
  fs.writeFileSync(file, `${JSON.stringify(manifest, null, 2)}\n`, 'utf8');

  // «Перечень»: наши строки старых номеров → новые номера с актуальным названием.
  const touched = new Set(pairs.flatMap((p) => [p.from, p.to]));
  const ours = registry.filter((r) => ourRegistry.has(r.id) && touched.has(r.tender_number));
  const foreignNumbers = new Set(registry.filter((r) => !ourRegistry.has(r.id) && r.tender_number).map((r) => r.tender_number));
  const need = [...touched].filter((n) => manifest.tenders[n] && !foreignNumbers.has(n));
  const free = [...ours];
  for (const n of need) {
    const exact = free.findIndex((r) => r.tender_number === (pairs.find((p) => p.to === n)?.from ?? n));
    const [row] = free.splice(exact >= 0 ? exact : 0, 1);
    if (!row) break;
    const t = tenders.find((x) => ids(n).includes(x.id));
    await api.patchRegistry(row.id, { tender_number: n, title: manifest.tenders[n].title, client_name: t?.client_name });
    console.log(`«Перечень»: строка ${row.tender_number} → ${n}`);
  }
  for (const row of free) {
    await api.patchRegistry(row.id, { tender_number: '' });
    console.log(`«Перечень»: строка ${row.tender_number} — дубль, номер снят (в архиве)`);
  }
};

main().catch((err) => {
  console.error(`ОШИБКА: ${err.message}`);
  process.exitCode = 1;
});
