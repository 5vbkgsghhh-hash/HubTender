// Проверки хелперов панели эталонов — run via tsx:
//   npx tsx scripts/checks/costBenchmarkPolicy.check.mjs

import {
  canEditBenchmarkRanges, statusDisplay, referenceRangeText, isDeviation, buildBenchmarkTree,
} from '../../src/lib/quality/costBenchmarkPolicy.ts';

const failures = [];
function check(name, cond) {
  if (!cond) failures.push(name);
  else console.log('  ok — ' + name);
}

const assess = (over = {}) => ({
  value: 100, status: 'WITHIN_RANGE', reference: null, deviation_percent: null,
  history_conflict: false, history_median: null, history_tenders: 0, ...over,
});
const row = (over = {}) => ({
  level: 'category', category_id: 'c1', detail_id: '', name: 'Монолит', location: '', unit: 'м3',
  volume: 10, commercial_total: 1000, per_volume_unit: assess(), per_area_sp: assess(), ...over,
});

check('инженер не правит справочник', !canEditBenchmarkRanges('engineer'));
check('ведущий инженер правит', canEditBenchmarkRanges('veduschiy_inzhener'));
check('без роли — нет', !canEditBenchmarkRanges(undefined));

check('выше эталона — красный', statusDisplay('ABOVE_RANGE').color === 'red');
check('диапазон с двумя границами', referenceRangeText({ source: 'manual_any', min: 18000, max: 25000 }).includes('–'));
check('только верхняя граница', referenceRangeText({ source: 'manual_any', min: null, max: 25000 }).startsWith('до'));

check('конфликт с историей — тоже отклонение', isDeviation(assess({ history_conflict: true })));
check('в эталоне — не отклонение', !isDeviation(assess()));
check('null — не отклонение', !isDeviation(null));

const rows = [
  row({ level: 'total', category_id: '', name: 'Итого', per_volume_unit: null }),
  row(),
  row({ level: 'detail', detail_id: 'd1', name: 'Стены', per_area_sp: assess({ status: 'ABOVE_RANGE' }) }),
  row({ level: 'detail', detail_id: 'd2', name: 'Плиты' }),
  row({ category_id: 'c2', name: 'Кладка' }),
];
const tree = buildBenchmarkTree(rows, false);
check('итог и категории — верхний уровень', tree.length === 3 && tree[0].level === 'total');
check('детализации под своей категорией', tree[1].children?.length === 2);

const dev = buildBenchmarkTree(rows, true);
check('в режиме отклонений остаётся категория с отклонением в детализации',
  dev.length === 1 && dev[0].category_id === 'c1' && dev[0].children?.length === 1);

// Над-группа ВИС — как на «Затратах на строительство» (utils/costGroups).
const visRows = [
  row({ level: 'total', category_id: '', name: 'Итого', per_volume_unit: null }),
  row({ category_id: 'b', name: 'БЛАГОУСТРОЙСТВО', commercial_total: 50 }),
  row({ category_id: 'v1', name: 'ВИС / Механические инженерные системы', commercial_total: 300 }),
  row({ level: 'detail', category_id: 'v1', detail_id: 'v1d', name: 'Отопление',
    per_area_sp: assess({ status: 'ABOVE_RANGE' }) }),
  row({ category_id: 'v2', name: 'ВИС/Электрические системы', commercial_total: 200 }),
  row({ category_id: 'n1', name: 'НАРУЖНИЕ ВИС / Электрические системы', commercial_total: 70 }),
];
const ctx = { areaSp: 10, calculationReady: true };
const vis = buildBenchmarkTree(visRows, false, ctx);
const group = vis.find((n) => n.isGroup);
check('ВИС-категории собраны в одну над-группу',
  !!group && group.name === 'ВНУТРЕННИЕ ИНЖЕНЕРНЫЕ СИСТЕМЫ' && group.children?.length === 2);
check('над-группа на месте первой ВИС-категории', vis[2] === group && vis.length === 4);
check('наружные сети ВИС в группу не входят',
  vis.some((n) => n.category_id === 'n1') && !group.children.some((c) => c.category_id === 'n1'));
check('детализации остаются под своей категорией внутри группы',
  group.children[0].category_id === 'v1' && group.children[0].children?.length === 1);
check('сумма группы — по её категориям', group.commercial_total === 500);
check('₽/м² группы считается, эталона нет',
  group.per_area_sp.value === 50 && group.per_area_sp.status === 'NO_REFERENCE' && group.per_volume_unit === null);
check('без площади — нет показателя',
  buildBenchmarkTree(visRows, false, { areaSp: null, calculationReady: true })
    .find((n) => n.isGroup).per_area_sp.status === 'NO_VALUE');
check('расчёт не актуален — как у остальных строк',
  buildBenchmarkTree(visRows, false, { areaSp: 10, calculationReady: false })
    .find((n) => n.isGroup).per_area_sp.status === 'CALCULATION_NOT_READY');

const visDev = buildBenchmarkTree(visRows, true, ctx);
const devGroup = visDev.find((n) => n.isGroup);
check('в режиме отклонений в группе только категории с отклонением',
  !!devGroup && devGroup.children?.length === 1 && devGroup.children[0].category_id === 'v1');
check('сумма группы не зависит от режима отклонений', devGroup.commercial_total === 500);
check('без отклонений в ВИС группы нет',
  !buildBenchmarkTree(visRows.filter((r) => r.detail_id !== 'v1d'), true, ctx).some((n) => n.isGroup));
check('без ВИС-категорий группы нет', !buildBenchmarkTree(rows, false, ctx).some((n) => n.isGroup));

if (failures.length) {
  console.error('\nПровалено: ' + failures.length);
  for (const f of failures) console.error('  FAIL — ' + f);
  process.exit(1);
}
console.log('\nВсе проверки пройдены.');
