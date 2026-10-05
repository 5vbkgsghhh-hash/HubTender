// Чистые хелперы панели эталонов. Без React — проверяются
// scripts/checks/costBenchmarkPolicy.check.mjs.
import type {
  BenchmarkAssessment,
  BenchmarkReference,
  BenchmarkStatus,
  CostBenchmarkRow,
} from '../api/costBenchmarks';
import { VIS_SUPER_GROUP_NAME, isVisCategory } from '../../utils/costGroups';

/** Роли, которым сервер разрешает править справочник (BenchmarkRangeEditorRoles). */
export const BENCHMARK_EDITOR_ROLES = [
  'administrator',
  'developer',
  'director',
  'general_director',
  'veduschiy_inzhener',
] as const;

export function canEditBenchmarkRanges(roleCode: string | null | undefined): boolean {
  return !!roleCode && (BENCHMARK_EDITOR_ROLES as readonly string[]).includes(roleCode);
}

export function statusDisplay(status: BenchmarkStatus): { text: string; color: string } {
  switch (status) {
    case 'ABOVE_RANGE':
      return { text: 'выше эталона', color: 'red' };
    case 'BELOW_RANGE':
      return { text: 'ниже эталона', color: 'orange' };
    case 'WITHIN_RANGE':
      return { text: 'в эталоне', color: 'green' };
    case 'NO_VALUE':
      return { text: 'нет объёма/площади', color: 'default' };
    case 'NO_REFERENCE':
      return { text: 'нет эталона', color: 'default' };
    default:
      return { text: 'расчёт не актуален', color: 'default' };
  }
}

export function sourceLabel(ref: BenchmarkReference): string {
  switch (ref.source) {
    case 'manual_exact':
      return 'справочник: класс и объём строительства';
    case 'manual_class':
      return 'справочник: класс';
    case 'manual_scope':
      return 'справочник: объём строительства';
    case 'manual_any':
      return 'справочник: любой объект';
    case 'history_class':
      return `история своего класса (${ref.tenders_count ?? 0} тенд.)`;
    default:
      return `история всех объектов (${ref.tenders_count ?? 0} тенд.)`;
  }
}

const num = (v: number): string => Math.round(v).toLocaleString('ru-RU');

/** Диапазон эталона одной строкой: «18 000 – 25 000», «до 25 000», «от 18 000». */
export function referenceRangeText(ref: BenchmarkReference): string {
  if (ref.min !== null && ref.max !== null) return `${num(ref.min)} – ${num(ref.max)}`;
  if (ref.max !== null) return `до ${num(ref.max)}`;
  if (ref.min !== null) return `от ${num(ref.min)}`;
  return '—';
}

export function isDeviation(a: BenchmarkAssessment | null): boolean {
  return !!a && (a.status === 'ABOVE_RANGE' || a.status === 'BELOW_RANGE' || a.history_conflict);
}

export interface CostBenchmarkTreeRow extends CostBenchmarkRow {
  key: string;
  /** Над-группа ВИС: собирается на фронте, своего эталона у неё нет. */
  isGroup?: boolean;
  children?: CostBenchmarkTreeRow[];
}

/** Площадь и готовность расчёта — для ₽/м² над-группы, которую сервер не считает. */
export interface BenchmarkTreeContext {
  areaSp: number | null;
  calculationReady: boolean;
}

/** ₽/м² по СП над-группы. Статусы в том же порядке, что у сервера (costbenchmark.assess). */
function groupAreaAssessment(total: number, ctx: BenchmarkTreeContext): BenchmarkAssessment {
  const value =
    ctx.areaSp !== null && ctx.areaSp > 0 && total > 0
      ? Math.round((total / ctx.areaSp) * 100) / 100
      : null;
  let status: BenchmarkStatus = 'NO_REFERENCE';
  if (!ctx.calculationReady) status = 'CALCULATION_NOT_READY';
  else if (value === null) status = 'NO_VALUE';
  return {
    value,
    status,
    reference: null,
    deviation_percent: null,
    history_conflict: false,
    history_median: null,
    history_tenders: 0,
  };
}

/**
 * Над-группа «ВНУТРЕННИЕ ИНЖЕНЕРНЫЕ СИСТЕМЫ», как на «Затратах на строительство»:
 * ВИС-категории уходят под одну строку на месте первой из них. Сумма группы — по
 * всем ВИС-категориям тендера, а не по оставшимся в режиме отклонений, чтобы
 * она не менялась от переключателя.
 */
function groupVisCategories(
  nodes: CostBenchmarkTreeRow[],
  visTotal: number,
  ctx: BenchmarkTreeContext,
): CostBenchmarkTreeRow[] {
  const isVis = (n: CostBenchmarkTreeRow) => n.level === 'category' && isVisCategory(n.name);
  const first = nodes.findIndex(isVis);
  if (first < 0) return nodes;
  const group: CostBenchmarkTreeRow = {
    key: 'group:vis',
    isGroup: true,
    level: 'category',
    category_id: '',
    detail_id: '',
    name: VIS_SUPER_GROUP_NAME,
    location: '',
    unit: '',
    volume: null,
    commercial_total: visTotal,
    per_volume_unit: null,
    per_area_sp: groupAreaAssessment(visTotal, ctx),
    children: nodes.filter(isVis),
  };
  const rest = nodes.filter((n) => !isVis(n));
  rest.splice(first, 0, group);
  return rest;
}

/**
 * Дерево для таблицы: итог, категории, под категорией — её детализации;
 * ВИС-категории — под общей над-группой. onlyDeviations оставляет строки с
 * отклонением и категории, у которых отклонение есть хотя бы у одной детализации.
 */
export function buildBenchmarkTree(
  rows: CostBenchmarkRow[],
  onlyDeviations: boolean,
  ctx: BenchmarkTreeContext = { areaSp: null, calculationReady: true },
): CostBenchmarkTreeRow[] {
  const out: CostBenchmarkTreeRow[] = [];
  const byCategory = new Map<string, CostBenchmarkTreeRow>();
  const dev = (r: CostBenchmarkRow) => isDeviation(r.per_area_sp) || isDeviation(r.per_volume_unit);

  for (const r of rows) {
    if (r.level === 'detail') continue;
    const node: CostBenchmarkTreeRow = { ...r, key: `${r.level}:${r.category_id}` };
    if (r.level === 'category') byCategory.set(r.category_id, node);
    out.push(node);
  }
  for (const r of rows) {
    if (r.level !== 'detail') continue;
    if (onlyDeviations && !dev(r)) continue;
    const parent = byCategory.get(r.category_id);
    const node: CostBenchmarkTreeRow = { ...r, key: `detail:${r.detail_id}` };
    if (parent) (parent.children ??= []).push(node);
    else out.push(node);
  }
  const kept = onlyDeviations ? out.filter((n) => dev(n) || (n.children?.length ?? 0) > 0) : out;
  const visTotal = rows
    .filter((r) => r.level === 'category' && isVisCategory(r.name))
    .reduce((sum, r) => sum + r.commercial_total, 0);
  return groupVisCategories(kept, visTotal, ctx);
}
