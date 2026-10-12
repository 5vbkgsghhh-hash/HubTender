// Номера тендеров, версии и исключения для загрузки выгрузки 1С.

/** Тендеры, которые по решению пользователя не грузим (базовые номера). */
export const EXCLUDED_NUMBERS = ['297', '272'];

/** Номер/название вместо выведенных из tender_name (номер уже занят на портале). */
export const TENDER_OVERRIDES = {
  '298': { number: '298.1', title: 'ЖК Сокольники (претендер)' },
};

/** Номера объектов с общим номером в 1С — как решил пользователь (07.10.2026). */
export const NAME_NUMBERS = {
  '262. ЖК Дмитровское Небо, этапы 2.1, 2.2': '262',
  '262. ЖК Дмитровское небо (этап 2.3)': '262.1',
  '262. ЖК Дмитровское небо (этап 2.4)': '262.2',
  '262. ЖК Дмитровское небо этап 2.5': '262.3',
  '262. ЖК Дмитровское небо (СОШ) Этап 2.5': '262.4',
  '262. ЖК Дмитровское небо (ДОУ 2 часть) Этап 2.5': '262.5',
  '265. ЖК Парксайд (этап 2.1)': '265',
  '265. ЖК Парксайд (этап 2.2)': '265.1',
  '279. ЖК Летная 71 (Жилой Дом №1)': '279',
  '279. ЖК Летная 98 (Жилой Дом №2)': '279.1',
  '282. Мы.С (Ub1)': '282',
  '282. Мы.С (Ub2)': '282.1',
  '288. ВГК№5 Беговая (Реновация К1-К2)': '288',
  '288. ВГК№5 Беговая (ЖК К3)': '288.1',
  '299. Квартал на воде Лот 3 (монолит, общестрой)': '299',
  '299. Квартал на воде Лот 4 (кладка паркинг)': '299.1',
};

/** «263. ЖК События (Донстрой)» → { baseNumber: '263', title: 'ЖК События (Донстрой)' }. */
export const parseTenderName = (name) => {
  const m = /^\s*(\d+)\.\s*(.+?)\s*$/.exec(name ?? '');
  if (!m) return null;
  return { baseNumber: m[1], title: m[2] };
};

/** Текст в последних скобках названия — 1С кладёт туда заказчика или этап. */
export const parenthetical = (title) => {
  const m = /\(([^()]+)\)\s*$/.exec(title ?? '');
  return m ? m[1].trim() : null;
};

/**
 * Номера тендеров: из начала tender_name; если у номера несколько разных
 * объектов — суффикс по порядку первого появления: 262.1, 262.2…
 * @param {string[]} names tender_name в порядке файла (без повторов)
 * @returns {Map<string, {number: string, baseNumber: string, title: string}>}
 */
export const assignTenderNumbers = (names) => {
  const byBase = new Map();
  for (const name of names) {
    const parsed = parseTenderName(name);
    if (!parsed) continue;
    if (!byBase.has(parsed.baseNumber)) byBase.set(parsed.baseNumber, []);
    const list = byBase.get(parsed.baseNumber);
    if (!list.includes(name)) list.push(name);
  }
  const out = new Map();
  for (const [base, list] of byBase) {
    list.forEach((name, i) => {
      const { title } = parseTenderName(name);
      const override = list.length === 1 ? TENDER_OVERRIDES[base] : undefined;
      out.set(name, {
        number: NAME_NUMBERS[name] ?? override?.number ?? (list.length > 1 ? `${base}.${i + 1}` : base),
        baseNumber: base,
        title: override?.title ?? title,
      });
    });
  }
  return out;
};

/**
 * --replace «269=Т-001-ГАЛС,…»: номер из файла = номер на портале, который он заменяет.
 * Просто «269» — заменяет тот же номер.
 */
export const parseReplace = (raw) => (raw ? raw.split(',').map((s) => s.trim()).filter(Boolean) : [])
  .map((s) => {
    const [number, old] = s.split('=').map((x) => x.trim());
    return { number, old: old || number };
  });

/** Причина пропуска тендера или null, если его грузим. */
export const skipReason = (name) => {
  const parsed = parseTenderName(name);
  if (!parsed) return 'нет номера в начале названия';
  if (EXCLUDED_NUMBERS.includes(parsed.baseNumber)) return 'исключён пользователем';
  return null;
};

/**
 * Номера версий тендера: «Версия N …» → N, прочие («Ub1+Ub2») — порядковый
 * номер в файле или ближайший свободный после него.
 * @param {string[]} versionNames в порядке файла
 * @returns {{name: string, version: number, label: string}[]}
 */
export const assignVersionNumbers = (versionNames) => {
  const used = new Set();
  const explicit = versionNames.map((name) => {
    const m = /^\s*Версия\s+(\d+)/i.exec(name);
    const v = m ? Number(m[1]) : null;
    if (v !== null && !used.has(v)) {
      used.add(v);
      return v;
    }
    return null;
  });
  return versionNames.map((name, i) => {
    let v = explicit[i];
    if (v === null) {
      v = i + 1;
      while (used.has(v)) v += 1;
      used.add(v);
    }
    return { name, version: v, label: name.trim() };
  });
};
