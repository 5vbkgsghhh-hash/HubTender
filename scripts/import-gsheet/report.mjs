// Отчёт проверки файла расчёта (out/<номер>-report.md): что и как ляжет на портал.

const fmt = (x, d = 2) => (x === null || x === undefined ? '—'
  : Number(x).toLocaleString('ru-RU', { minimumFractionDigits: 0, maximumFractionDigits: d }));
const cell = (s) => String(s ?? '—').replace(/\|/g, '\\|').replace(/\s+/g, ' ');

export const renderReport = ({ prep, params, label }) => {
  const { header, parsed, markups, mv, volumes } = prep;
  const st = parsed.stats;
  const out = [];
  const p = (...l) => out.push(...l);
  const zeroSum = parsed.zeroed.reduce((s, z) => s + z.sum, 0);
  const byLevel = new Map();
  for (const pos of parsed.built.positions) byLevel.set(pos.hierarchy_level, (byLevel.get(pos.hierarchy_level) ?? 0) + 1);
  const levels = [...byLevel.entries()].sort((a, b) => a[0] - b[0]);

  p(`# ${header.number}. ${header.title} — проверка файла`, '', `Файл: ${prep.fileName}. Сформирован: ${new Date().toISOString()}.`, '');
  if (parsed.partStats) p('Расчёт из нескольких файлов: строки ниже указаны как «часть:строка листа».', '');
  if (prep.blockers.length) p('## ⛔ Блокеры', '', ...prep.blockers.map((b) => `- ${cell(b)}`), '');
  p('## Тендер', '',
    `- Номер **${header.number}**, название «${header.title}», заказчик «${header.client ?? '—'}»; версия 1, архивный.`,
    `- Курсы: USD ${fmt(header.rates.USD)}, EUR ${fmt(header.rates.EUR)}, CNY ${fmt(header.rates.CNY)}.`,
    `- Площадь по СП ${fmt(header.fields.area_sp)}, заказчика ${fmt(header.fields.area_client)}.`,
    `- Ссылки: ${['qa_form_link', 'upload_folder', 'bsm_link', 'tz_link'].map((k) => `${k} ${header.fields[k] ? 'есть' : '—'}`).join(', ')}.`, '');

  p('## Прямые затраты', '',
    `- Позиций ${st.positions}, строк ${st.items} (${Object.entries(st.types).map(([k, n]) => `${k} ${n}`).join(', ')}).`,
    ...(st.separators || st.dops || st.dopEmpty ? [`- Разделителей пропущено ${st.separators ?? 0}; ДОП ${st.dops ?? 0}, пустых ДОП-заголовков не загружено ${st.dopEmpty ?? 0}.`] : []),
    `- Материалы: привязаны к работе ${st.linked}, без привязки ${st.unlinked}, расход < 1 перенесён ${st.folded}.`,
    `- Работ с коэффициентом в цене ${st.workFactor}. Доставка: ${Object.entries(st.delivery).map(([k, n]) => `${k} ${n}`).join(', ')}.`,
    `- Валюты строк: ${Object.entries(st.currencies).map(([k, n]) => `${k} ${n}`).join(', ')}.`,
    `- Без единицы: по наименованию ${st.unitByName ?? 0}, по позиции ${st.unitFallback}, по умолчанию «шт» ${st.unitDefault}; без вида затрат ${st.noCost}.`,
    ...(levels.length > 1 ? [`- Уровни иерархии (позиций): ${levels.map(([l, n]) => `${l} — ${n}`).join(', ')}.`] : []),
    ...(parsed.partStats ?? []).map((ps, i) => `- Часть ${i + 1}: позиций ${ps.positions}, строк ${ps.items}, Σ листа ${fmt(ps.sheetTotal)} → ${fmt(ps.expected)}.`),
    '', '| Сумма | ₽ |', '|---|---|',
    `| Итого листа (по строкам) | ${fmt(st.sheetTotal)} |`,
    `| Обнулено (кол-во × цена, итог 0): ${parsed.zeroed.length} строк | ${fmt(zeroSum)} |`,
    `| Итог ≠ кол-во × цена: ${parsed.corrections.length} строк, Δ к листу | ${fmt(parsed.corrections.reduce((s, c) => s + c.loaded - c.sheet, 0))} |`,
    ...(parsed.negatives?.length ? [`| Отрицательный итог (цена обнулена): ${parsed.negatives.length} строк, Δ к листу | ${fmt(-parsed.negatives.reduce((s, n) => s + n.sheet, 0))} |`] : []),
    `| **Ожидаемая сумма тендера на портале** | **${fmt(st.expected)}** |`, '');

  if (parsed.corrections.length) {
    p('### Исправленные итоги', '', '| Строка | Тип | Позиция | Наименование | В таблице | Грузим | Как |', '|---|---|---|---|---|---|---|');
    for (const c of parsed.corrections) p(`| ${c.row} | ${c.type} | ${cell(c.position).slice(0, 40)} | ${cell(c.name).slice(0, 45)} | ${fmt(c.sheet)} | ${fmt(c.loaded)} | ${c.how} |`);
    p('');
  }
  if (parsed.negatives?.length) {
    p('### Отрицательные итоги (портал их не хранит — цена 0, исходное в описании строки)', '', '| Строка | Тип | Позиция | Наименование | В таблице | Формула |', '|---|---|---|---|---|---|');
    for (const n of parsed.negatives) p(`| ${n.row} | ${n.type} | ${cell(n.position).slice(0, 40)} | ${cell(n.name).slice(0, 45)} | ${fmt(n.sheet)} | ${cell(n.sheetFormula)} |`);
    p('');
  }
  if (parsed.zeroed.length) {
    p('### Обнулённые строки (цена 0, кол-во сохранено, цена в примечании) — крупнейшие', '',
      '| Строка | Тип | Позиция | Наименование | Кол-во × цена |', '|---|---|---|---|---|');
    for (const z of [...parsed.zeroed].sort((a, b) => b.sum - a.sum).slice(0, 25)) p(`| ${z.row} | ${z.type} | ${cell(z.position).slice(0, 40)} | ${cell(z.name).slice(0, 45)} | ${fmt(z.sum)} |`);
    p('');
  }
  if (parsed.anomalies.length) {
    p('### Особые строки', '');
    for (const a of parsed.anomalies.slice(0, 120)) p(`- стр. ${a.row}${a.name ? ` «${cell(a.name).slice(0, 60)}»` : ''}: ${a.reason}`);
    if (parsed.anomalies.length > 120) p(`- … ещё ${parsed.anomalies.length - 120}`);
    p('');
  }

  const { dops = [], positions, units } = parsed.built;
  if (dops.length) {
    const byDop = new Map(units.filter((u) => u.target.kind === 'dop').map((u) => [u.target.index, u.items]));
    p('### ДОП (доп. работы к позициям заказчика)', '', '| Строка | ДОП | Ед. | Объём | Строк | Сумма | Позиция заказчика |', '|---|---|---|---|---|---|---|');
    dops.forEach((d, i) => {
      const items = byDop.get(i) ?? [];
      const parent = positions[d.parent_index];
      p(`| ${d.temp_id.replace(/^dop_?/, '').replace('_', ':')} | ${cell(d.work_name).slice(0, 60)} | ${d.unit_code ?? '—'} | ${fmt(d.manual_volume)} | ${items.length} | ${fmt(items.reduce((t, x) => t + (x.total_amount ?? 0), 0))} | ${cell(parent.item_no ?? '')} ${cell(parent.work_name).slice(0, 45)} |`);
    });
    p('');
  }
  if (parsed.costSplit?.size) {
    p('### Статьи, разнесённые по наименованию строки', '', '| Статья расчёта | Вид затрат портала | Строк | Сумма |', '|---|---|---|---|');
    for (const e of [...parsed.costSplit.values()].sort((a, b) => a.text.localeCompare(b.text, 'ru') || b.sum - a.sum)) {
      p(`| ${cell(e.text)} | ${cell(label.get(e.id) ?? e.id)} | ${e.rows} | ${fmt(e.sum)} |`);
    }
    p('');
  }

  const unmapped = [...parsed.unmappedCosts.values()].sort((a, b) => b.rows - a.rows);
  p('## Виды затрат без сопоставления', '');
  if (!unmapped.length) p('Нет — все строки сопоставлены.', '');
  else {
    p('Решение — в `out/gs-cost-map.json`: `{"Вид | Детализация | Локализация": "<id вида затрат>" | null}`.', '',
      '| Строк | Вид | Детализация | Локализация | Подбор | Кандидаты (id) |', '|---|---|---|---|---|---|');
    for (const u of unmapped) {
      const [g, d, l] = u.text.split(' | ');
      p(`| ${u.rows} | ${cell(g)} | ${cell(d)} | ${cell(l)} | ${u.how} | ${u.candidates.slice(0, 3).map((c) => `${cell(c.label)} (${c.id})`).join('; ') || '—'} |`);
    }
    p('');
  }

  p('## Единицы измерения', '', '| Текст | Строк | Код | Есть в справочнике | Источник |', '|---|---|---|---|---|');
  for (const kind of ['items', 'customer']) {
    for (const u of prep.unitReport[kind]) if (u.code !== u.text || !u.exists) p(`| ${cell(u.text)} (${kind === 'items' ? 'строки' : 'позиции'}) | ${u.rows} | ${u.code ?? '—'} | ${u.exists ? 'да' : '**нет**'} | ${u.source} |`);
  }
  p('', `Новых единиц: ${prep.missingUnits.length ? prep.missingUnits.join(', ') : 'нет'}. Новых наименований: ${prep.missingNames.length} из ${prep.names.size}.`, '');

  const fills = new Map();
  for (const f of parsed.unitFills ?? []) {
    const k = `${f.kind}|${f.name}|${f.unit}|${f.how}`;
    if (!fills.has(k)) fills.set(k, { ...f, rows: 0 });
    fills.get(k).rows += 1;
  }
  if (fills.size) {
    p('### Строки без единицы измерения', '', `Строк ${parsed.unitFills.length}, наименований ${fills.size}.`, '',
      '| Строка | Строк | Тип | Наименование | В таблице | Единица | Источник |', '|---|---|---|---|---|---|---|');
    for (const f of [...fills.values()].sort((a, b) => Number(a.byName) - Number(b.byName) || String(a.how).localeCompare(String(b.how), 'ru'))) {
      p(`| ${f.row} | ${f.rows} | ${f.type} | ${cell(f.name)} | ${cell(f.text ?? 'пусто')} | **${f.unit}** | ${cell(f.how)} |`);
    }
    p('');
  }

  p('## Проценты наценок («Анализ»)', '', `Тактика «Базовая схема».`, '', '| Параметр | % |', '|---|---|');
  for (const prm of params) p(`| ${prm.label ?? prm.key} (${prm.key}) | ${fmt(mv.values[prm.key], 4)} |`);
  if (mv.notes.length || markups.unknown.length) p('', ...mv.notes.map((n) => `- ${n}`), ...markups.unknown.map((u) => `- без соответствия: ${cell(u)}`));
  p('');

  if (prep.growth) {
    const { growth, visitOther } = parsed;
    const on = [...growth.entries()].filter(([id]) => prep.growth.enabled.has(id)).sort((x, y) => y[1].flagged - x[1].flagged);
    const off = [...growth.entries()].filter(([id, e]) => e.flaggedRows && !prep.growth.enabled.has(id));
    p('## Рост субподряда («Интервал захода подряда на площадку»)', '',
      `Рост работ субподряда включён для ${on.length} видов затрат (помечена большая часть суммы суб-раб); остальным видам затрат рост отключён: исключений ${prep.growth.exclusions} (работы и материалы).`, '',
      '| Вид затрат | Суб-раб с «1» / всего | Сумма с «1» | Сумма всего |', '|---|---|---|---|');
    for (const [id, e] of on) p(`| ${cell(label.get(id) ?? id)} | ${e.flaggedRows} / ${e.rows} | ${fmt(e.flagged)} | ${fmt(e.total)} |`);
    p('');
    if (off.length) {
      p('«1» стоит у меньшей части суммы суб-раб — рост не включён:', '');
      for (const [id, e] of off) p(`- ${cell(label.get(id) ?? id)}: ${e.flaggedRows} из ${e.rows} строк, ${fmt(e.flagged)} из ${fmt(e.total)} ₽`);
      p('');
    }
    const other = Object.entries(visitOther ?? {});
    if (other.length) {
      p('«1» у строк, не являющихся работами субподряда (на портал не переносится):', '');
      for (const [type, o] of other) {
        const names = Object.entries(o.details).sort((x, y) => y[1] - x[1]).map(([id, n]) => `${cell(label.get(id) ?? id)} — ${n}`);
        p(`- ${type}: ${o.rows} строк, ${fmt(o.sum)} ₽ (${names.join('; ')})`);
      }
      p('');
    }
  }

  p('## Затраты на строительство', '', `Строк к записи: ${volumes.rows.length}.`, '');
  if (volumes.skipped.length) {
    p('Не легло (аналога на портале нет):', '');
    for (const s of volumes.skipped) p(`- ${cell(s.name)}${s.qty ? ` — ${fmt(s.qty)}` : ''}${s.note ? ` («${cell(s.note)}»)` : ''}`);
    p('');
  }
  const merged = volumes.rows.filter((r) => r.merged > 1);
  if (merged.length) p('Несколько строк в одну строку портала (сложены):', '', ...merged.map((r) => `- ${cell(r.group_key ?? label.get(r.detail_cost_category_id))}: ${r.merged} строк, ${fmt(r.volume)}`), '');
  return out.join('\n');
};
