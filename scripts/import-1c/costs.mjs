// Статьи затрат 1С («000000013 10.01. Общестроительные работы/ Кровельные
// работы/ Нет») → detail_cost_categories HUBTender («КРОВЛЯ / Кровельные
// работы / Здание»). Вид затраты HUBTender = подгруппа 1С, категория у
// HUBTender своя, локации переименованы — см. LOCATION_ALIASES.

/** Локации, которые встречаются в выгрузке 1С (после последнего «/»). */
export const KNOWN_LOCATIONS = [
  'Нет', 'МОПы-надземная часть', 'МОПы-надземная часть (лестницы)', 'МОПы-подземная часть',
  'Технические помещения', '1-й этаж (Лобби)', 'Автостоянка', 'Квартира', 'Наружные сети',
  'Кладовки (Помещения не БКТ)', 'МОКАП Фасады', 'Техпространство',
];

/** Локация 1С → варианты локации HUBTender по убыванию приоритета. */
const LOCATION_ALIASES = {
  'нет': ['здание', 'улица'],
  'мопы надземная часть': ['моп нч'],
  'мопы подземная часть': ['моп пч'],
  'мопы надземная часть лестницы': ['лк', 'моп нч'],
  '1 й этаж лобби': ['лобби'],
  'кладовки помещения не бкт': ['кладовки помещения не бкт'],
  'квартира': ['квартира', 'квартиры'],
  'наружные сети': ['улица', 'наружные сети'],
  'техническое помещение': ['технические помещения'], // так в расчётах Google Таблиц
};

/** Нормализация для сравнения названий: регистр, ё, пунктуация, пробелы. */
export const normText = (s) => String(s ?? '')
  .toLowerCase()
  .replace(/ё/g, 'е')
  .replace(/[^a-zа-я0-9]+/g, ' ')
  .trim();

/**
 * Разбор статьи 1С. Группы передаются списком известных (из cost_group):
 * подгруппа сама может содержать « / », поэтому режем по известным краям.
 * @returns {{code: string|null, number: string|null, group: string|null,
 *   subgroup: string|null, location: string|null, text: string}}
 */
export const parseCostArticle = (raw, knownGroups = [], knownSubgroups = []) => {
  let s = String(raw ?? '').trim();
  const codeM = /^(\d{6,})\s+/.exec(s);
  const code = codeM ? codeM[1] : null;
  if (codeM) s = s.slice(codeM[0].length);
  const numM = /^(\d+(?:\.\d+)*)[.\s]+/.exec(s);
  const number = numM ? numM[1] : null;
  if (numM) s = s.slice(numM[0].length);
  const text = s;

  let group = null;
  const groups = [...knownGroups].sort((a, b) => b.length - a.length);
  for (const g of groups) {
    if (s.startsWith(`${g}/`)) {
      group = g;
      s = s.slice(g.length + 1).trim();
      break;
    }
  }
  if (!group) {
    const i = s.indexOf('/');
    if (i > 0) {
      group = s.slice(0, i).trim();
      s = s.slice(i + 1).trim();
    }
  }

  let location = null;
  const slash = s.lastIndexOf('/');
  if (slash >= 0) {
    const tail = s.slice(slash + 1).trim();
    if (KNOWN_LOCATIONS.includes(tail)) {
      location = tail;
      s = s.slice(0, slash).trim();
    }
  }
  let subgroup = s.replace(/[\s/]+$/, '').trim() || null;
  // 1С обрезает статью до 150 символов: восстанавливаем полное имя подгруппы
  // по колонке cost_subgroup, если обрезок — начало ровно одного из них.
  if (subgroup && !location) {
    const full = knownSubgroups.filter((x) => x !== subgroup && x.startsWith(subgroup));
    if (full.length === 1) subgroup = full[0];
  }
  return { code, number, group, subgroup, location, text };
};

const tokens = (s) => new Set(normText(s).split(' ').filter(Boolean));
const jaccard = (a, b) => {
  const A = tokens(a);
  const B = tokens(b);
  if (!A.size || !B.size) return 0;
  let inter = 0;
  for (const t of A) if (B.has(t)) inter += 1;
  return inter / (A.size + B.size - inter);
};

/** Метка как в выгрузках HUBTender: «КАТЕГОРИЯ / Вид / Локация». */
export const detailLabel = (d) => [d.categoryName, d.name, d.location].filter(Boolean).join(' / ');

/**
 * Подбор детальной категории HUBTender для статьи 1С.
 * @param {ReturnType<typeof parseCostArticle>} art
 * @param {{id: string, name: string, location: string|null, categoryName: string}[]} details
 * @returns {{id: string|null, how: string, candidates: {id: string, label: string, score: number}[]}}
 */
export const matchArticle = (art, details) => {
  const sub = normText(art.subgroup);
  const group = art.group ?? '';
  const byName = details.filter((d) => normText(d.name) === sub);
  const groupScore = (d) => jaccard(d.categoryName, group);
  const top = (list, how) => ({
    id: list.length === 1 ? list[0].id : null,
    how: list.length === 1 ? how : 'неоднозначно',
    candidates: list.slice(0, 5).map((d) => ({ id: d.id, label: detailLabel(d), score: 1 })),
  });

  if (byName.length === 1) return top(byName, 'вид');
  if (byName.length > 1) {
    const aliases = LOCATION_ALIASES[normText(art.location)] ?? [normText(art.location)];
    for (const alias of aliases) {
      const byLoc = byName.filter((d) => normText(d.location) === alias);
      if (byLoc.length === 1) return top(byLoc, 'вид+локация');
      if (byLoc.length > 1) {
        const best = [...byLoc].sort((a, b) => groupScore(b) - groupScore(a));
        if (groupScore(best[0]) > groupScore(best[1])) return top([best[0]], 'вид+локация+категория');
        return top(best, 'неоднозначно');
      }
    }
    return top(byName, 'неоднозначно');
  }

  const scored = details
    .map((d) => ({ d, score: jaccard(d.name, art.subgroup) }))
    .filter((x) => x.score >= 0.5)
    .sort((a, b) => b.score - a.score || groupScore(b.d) - groupScore(a.d));
  return {
    id: null,
    how: scored.length ? 'похоже — проверить' : 'нет',
    candidates: scored.slice(0, 5).map(({ d, score }) => ({ id: d.id, label: detailLabel(d), score: Number(score.toFixed(2)) })),
  };
};
