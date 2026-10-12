// Отчёт пробного прогона части 2 (out/extras-report.md): что и куда запишется.

const fmt = (x, d = 0) => (x === null || x === undefined ? '—'
  : Number(x).toLocaleString('ru-RU', { minimumFractionDigits: d, maximumFractionDigits: d }));
const mln = (x) => fmt((x ?? 0) / 1e6, 1);
const cell = (s) => String(s ?? '—').replace(/\|/g, '\\|').replace(/\s+/g, ' ');

const SHORT = {
  mechanization_service: 'СМ', mbp_gsm: 'МБП', warranty_period: 'Гар', works_16_markup: 'Раб1,6',
  works_cost_growth: 'РостР', material_cost_growth: 'РостМ', subcontract_works_cost_growth: 'РостСР',
  subcontract_materials_cost_growth: 'РостСМ', contingency_costs: 'Непр', overhead_own_forces: 'ООЗс',
  overhead_subcontract: 'ООЗсуб', general_costs_without_subcontract: 'ОФЗ', profit_own_forces: 'Пр.с',
  profit_subcontract: 'Пр.суб', nds_22: 'НДС',
};

/** @param {{mode: string, parts: string[], plans: object[], coverage: object, params: object[], tactic: object, label: Map}} r */
export const renderExtrasReport = ({ mode, parts, plans, coverage, params, tactic, label }) => {
  const out = [];
  const p = (...l) => out.push(...l);
  const tag = (pl) => `${pl.number} v${pl.version}`;
  p(`# Перенос из 1С, часть 2 — ${mode}`, '', `Сформирован: ${new Date().toISOString()}. Версий: ${plans.length}. Части: ${parts.join(', ')}.`, '');

  p('## Сопоставление файлов', '', '| Файл | Версий в файле | Сопоставлено | Исключено (297, 272, Тестовый) | Не найдено |', '|---|---|---|---|---|');
  for (const [file, c] of Object.entries(coverage)) p(`| ${file} | ${c.total} | ${c.matched} | ${c.excluded} | ${c.missing.length ? c.missing.map(cell).join('; ') : '—'} |`);
  p('');

  // ─── Поля ─────────────────────────────────────────────────────────────────
  const withFields = plans.filter((pl) => Object.keys(pl.fields.changed).length);
  p('## Поля тендера (ссылки, площади, описание, срок)', '', `Изменится версий: ${withFields.length} из ${plans.length}. Курсы не меняются.`, '');
  const fieldCount = {};
  for (const pl of withFields) for (const k of Object.keys(pl.fields.changed)) fieldCount[k] = (fieldCount[k] ?? 0) + 1;
  p(Object.entries(fieldCount).map(([k, n]) => `- \`${k}\`: ${n}`).join('\n') || '- нет', '');
  p('<details><summary>По версиям</summary>', '', '| Версия | Поле | Было | Станет |', '|---|---|---|---|');
  for (const pl of withFields) {
    for (const [k, [a, b]] of Object.entries(pl.fields.changed)) p(`| ${tag(pl)} | ${k} | ${cell(a).slice(0, 60)} | ${cell(b).slice(0, 60)} |`);
  }
  p('', '</details>', '');

  // ─── Объёмы ───────────────────────────────────────────────────────────────
  const vol = plans.filter((pl) => pl.volumes.rows.length || pl.volumes.skipped.length);
  p('## «Затраты на строительство»: количества и примечания', '');
  p(`Версий с данными: ${vol.length}. Строк к записи: ${plans.reduce((s, pl) => s + pl.volumes.changed.length, 0)} `
    + `(всего целей ${plans.reduce((s, pl) => s + pl.volumes.rows.length, 0)}, уже совпадают — остальные).`, '');
  const skippedAgg = new Map();
  for (const pl of plans) {
    for (const s of pl.volumes.skipped) {
      const e = skippedAgg.get(s.code) ?? { name: s.name, versions: 0, withQty: 0 };
      e.versions += 1;
      if (s.qty) e.withQty += 1;
      skippedAgg.set(s.code, e);
    }
  }
  p('Не легло (аналога на портале нет):', '', '| Код 1С | Статья / группа | Версий | С количеством |', '|---|---|---|---|');
  for (const [code, e] of skippedAgg) p(`| ${code} | ${cell(e.name)} | ${e.versions} | ${e.withQty} |`);
  const merged = plans.flatMap((pl) => pl.volumes.rows.filter((r) => r.merged > 1).map((r) => ({ pl, r })));
  if (merged.length) {
    p('', 'Несколько статей 1С в одну строку портала (количества сложены):', '');
    for (const { pl, r } of merged.slice(0, 40)) p(`- ${tag(pl)}: ${cell(r.group_key ?? label.get(r.detail_cost_category_id))} — ${r.merged} статьи, итог ${fmt(r.volume, 3)}`);
  }
  p('');

  // ─── Рост субподряда ──────────────────────────────────────────────────────
  p('## Рост субподряда (исключения по видам затрат)', '');
  p('Рост включён = вид затрат НЕ в исключениях. «1» — `visit_interval` у строк суб-раб в Результаты.csv; '
    + 'правило: вид затрат получает рост работ, если больше половины суммы его суб-раб — в строках с «1».', '');
  p('| Версия | Источник | Видов с ростом раб/мат | Исключений | Добавить/удалить | Σ суб-раб 1С, млн | с «1», млн | рост получит, млн | из них с «1», млн | Без вида затрат (рост всегда), млн | Собств. работы с «1» |',
    '|---|---|---|---|---|---|---|---|---|---|---|');
  for (const pl of plans) {
    const g = pl.growth;
    p(`| ${tag(pl)} | ${g.source} | ${g.enabledWorks.length}/${g.enabledMaterials.length} | ${g.desired} | +${g.toInsert.length}/−${g.toDeleteCount} | ${mln(g.stats.subTotal)} | ${mln(g.stats.flagged)} | ${mln(g.stats.covered)} | ${mln(g.stats.coveredFlagged)} | ${mln(g.stats.noCategory)} | ${g.own ? `${g.own.rows} стр., ${mln(g.own.amount)} млн` : '—'} |`);
  }
  const unmapped = plans.flatMap((pl) => pl.growth.unmapped.map((u) => `${tag(pl)}: ${u}`));
  if (unmapped.length) p('', 'Статьи файла зафиксированных затрат без вида затрат на портале:', '', ...unmapped.map((u) => `- ${cell(u)}`));
  p('', '<details><summary>Виды затрат с ростом по версиям</summary>', '');
  for (const pl of plans) {
    if (!pl.growth.enabledWorks.length && !pl.growth.enabledMaterials.length) continue;
    const both = pl.growth.enabledWorks.filter((id) => pl.growth.enabledMaterials.includes(id));
    const onlyW = pl.growth.enabledWorks.filter((id) => !pl.growth.enabledMaterials.includes(id));
    p(`**${tag(pl)}** (${pl.growth.source}): ${[...both.map((id) => `${label.get(id)} [раб+мат]`), ...onlyW.map((id) => `${label.get(id)} [раб]`)].map(cell).join('; ')}`, '');
  }
  p('</details>', '');

  // ─── Наценки ──────────────────────────────────────────────────────────────
  const keys = params.map((x) => x.key);
  p('## Проценты наценок', '');
  p(`Тактика: «${tactic.name}» (${tactic.id}) — назначится ${plans.filter((pl) => pl.markups.tacticChanged).length} версиям. `
    + 'СМ/МБП/Гар — из ₽/м²: значение × площадь ÷ Σ прямых «раб» версии на портале × 100.', '');
  p(`| Версия | Источник | Площадь | Σ раб портал, млн | Σ раб 1С, млн | ${keys.map((k) => SHORT[k] ?? k).join(' | ')} | Изм. |`,
    `|---|---|---|---|---|${keys.map(() => '---').join('|')}|---|`);
  for (const pl of plans) {
    const m = pl.markups;
    p(`| ${tag(pl)} | ${m.source} | ${fmt(m.area, 0)} | ${mln(m.worksSum)} | ${mln(m.worksSum1c)} | ${keys.map((k) => fmt(m.values[k], 3)).join(' | ')} | ${m.changed ? 'да' : '—'} |`);
  }
  const notes = plans.flatMap((pl) => pl.markups.notes.map((n) => `${tag(pl)}: ${n}`));
  if (notes.length) p('', ...notes.map((n) => `- ${n}`));
  p('');
  return out.join('\n');
};
