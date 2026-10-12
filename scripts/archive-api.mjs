#!/usr/bin/env node
/**
 * Тонкий CLI к API архива смет — для вызовов из терминала и из агента Cursor.
 *
 * Ключ берётся ТОЛЬКО из окружения: в аргументы командной строки он не
 * передаётся (иначе осел бы в истории шелла и в логах агента).
 *
 *   TENDERHUB_API_KEY=thk_...           обязателен
 *   TENDERHUB_API_URL=https://tender.su10.ru   по умолчанию
 *
 * Примеры:
 *   node scripts/archive-api.mjs search "устройство стяжки" --unit=м2 --limit=5
 *   node scripts/archive-api.mjs position <uuid>
 *   node scripts/archive-api.mjs tenders --search=ЖК --archived=false
 *   node scripts/archive-api.mjs tender <tender_id>
 *   node scripts/archive-api.mjs positions <tender_id>
 *   node scripts/archive-api.mjs costs <tender_id>
 *   node scripts/archive-api.mjs estimate <tender_id> [--position=<position_id>]
 *   node scripts/archive-api.mjs suggest "кладка стен" "монтаж дверей"
 *   node scripts/archive-api.mjs compose ./compose.json --dry-run
 *
 * Проверка данных (области verification:read / verification:write):
 *   node scripts/archive-api.mjs quality <tender_id> [--new] [--open] [--rule=U,V] [--full]
 *   node scripts/archive-api.mjs rules
 *   node scripts/archive-api.mjs sections <tender_id>
 *   node scripts/archive-api.mjs benchmarks <tender_id> [--period=24]
 *   node scripts/archive-api.mjs brief <tender_id>
 *   node scripts/archive-api.mjs verdicts <tender_id> ./verdicts.json
 *   node scripts/archive-api.mjs checkpoint <tender_id>
 *   node scripts/archive-api.mjs brief-set <tender_id> ./brief.txt
 */

const BASE = process.env.TENDERHUB_API_URL ?? 'https://tender.su10.ru';
const KEY = process.env.TENDERHUB_API_KEY;

if (!KEY) {
  console.error('Не задан TENDERHUB_API_KEY. Выпустите ключ: Настройки → Доступ к API.');
  process.exit(2);
}

const flags = new Map();
const positional = [];
for (const arg of process.argv.slice(3)) {
  if (arg.startsWith('--')) {
    const [name, value] = arg.slice(2).split('=');
    flags.set(name, value ?? 'true');
  } else {
    positional.push(arg);
  }
}

async function request(method, path, body) {
  try {
    return await fetch(`${BASE}${path}`, {
      method,
      headers: {
        'X-API-Key': KEY,
        ...(body ? { 'Content-Type': 'application/json' } : {}),
      },
      ...(body ? { body: JSON.stringify(body) } : {}),
    });
  } catch (err) {
    // Самая частая ошибка первого запуска — недоступный адрес, а не отказ API.
    console.error(`Не удалось соединиться с ${BASE}: ${err.cause?.code ?? err.message}`);
    console.error('Проверьте TENDERHUB_API_URL, сеть и то, что бэкенд запущен.');
    process.exit(1);
  }
}

async function call(method, path, body) {
  const res = await request(method, path, body);

  const text = await res.text();
  // 204 (вердикты, отметки разделов) приходит без тела — разбирать нечего.
  if (res.ok && !text) return undefined;
  let parsed;
  try {
    parsed = JSON.parse(text);
  } catch {
    // nginx и прочие прокси отвечают HTML — показываем как есть, не молчим.
    console.error(`HTTP ${res.status}, ответ не JSON:\n${text.slice(0, 500)}`);
    process.exit(1);
  }
  if (!res.ok) {
    const code = parsed.code ? ` [${parsed.code}]` : '';
    console.error(`HTTP ${res.status}${code}: ${parsed.detail ?? parsed.title ?? text}`);
    process.exit(1);
  }
  return parsed.data;
}

const command = process.argv[2];

switch (command) {
  case 'search': {
    const q = positional[0];
    if (!q) throw new Error('Укажите строку поиска');
    const params = new URLSearchParams({ q });
    // Слева — как пишут в командной строке, справа — имя параметра API.
    const map = {
      unit: 'unit_code', unit_code: 'unit_code', volume: 'volume', item_no: 'item_no',
      limit: 'limit', min_score: 'min_score', exclude: 'exclude_tender_id',
      exclude_tender_id: 'exclude_tender_id', period_months: 'period_months',
    };
    for (const [flag, param] of Object.entries(map)) {
      const value = flags.get(flag);
      if (value !== undefined) params.set(param, value);
    }
    console.log(JSON.stringify(await call('GET', `/api/v1/archive/positions/search?${params}`), null, 2));
    break;
  }

  case 'position': {
    const id = positional[0];
    if (!id) throw new Error('Укажите id позиции');
    console.log(JSON.stringify(await call('GET', `/api/v1/archive/positions/${id}`), null, 2));
    break;
  }

  case 'suggest': {
    if (positional.length === 0) throw new Error('Укажите одно или несколько названий');
    const body = {
      queries: positional.map((work_name, i) => ({ ref: String(i), work_name })),
      limit_per_query: Number(flags.get('limit') ?? 5),
    };
    console.log(JSON.stringify(await call('POST', '/api/v1/archive/positions/suggest', body), null, 2));
    break;
  }

  case 'compose': {
    const file = positional[0];
    if (!file) throw new Error('Укажите путь к JSON с телом запроса');
    const { readFile } = await import('node:fs/promises');
    const body = JSON.parse(await readFile(file, 'utf8'));
    // Безопасный дефолт: без явного --no-dry-run сборка только проверяется.
    body.dry_run = flags.get('no-dry-run') !== 'true';
    const verbose = flags.get('verbose') === 'true' ? '?verbose=1' : '';
    console.log(JSON.stringify(await call('POST', `/api/v1/archive/compose${verbose}`, body), null, 2));
    break;
  }

  case 'tenders': {
    // Узкий список тендеров — выбрать цель по номеру/названию. Требует области
    // tenders:read; ключ с ограничением по тендерам видит только свои.
    const params = new URLSearchParams();
    if (flags.get('search')) params.set('search', flags.get('search'));
    if (flags.has('archived')) params.set('is_archived', flags.get('archived'));
    const qs = params.toString();
    console.log(JSON.stringify(await call('GET', `/api/v1/tenders/brief${qs ? `?${qs}` : ''}`), null, 2));
    break;
  }

  case 'tender': {
    // Шапка тендера: номер, заказчик, курсы, итог КП, число позиций и строк.
    const tenderId = positional[0];
    if (!tenderId) throw new Error('Укажите id тендера');
    console.log(JSON.stringify(await call('GET', `/api/v1/tenders/${tenderId}/overview`), null, 2));
    break;
  }

  case 'costs': {
    // Позиции с итогами: база, КП, наценка, число строк — всё, что видит
    // инженер на странице «Позиции заказчика». Один ответ на весь тендер.
    const tenderId = positional[0];
    if (!tenderId) throw new Error('Укажите id тендера');
    console.log(JSON.stringify(await call('GET', `/api/v1/tenders/${tenderId}/positions/with-costs`), null, 2));
    break;
  }

  case 'estimate': {
    // Строки сметы с названиями работ/материалов, категориями затрат, ценами
    // и КП. Весь тендер одним ответом (крупные — десятки МБ) либо одна
    // позиция через --position=<id>.
    const tenderId = positional[0];
    const positionId = flags.get('position');
    if (!tenderId && !positionId) throw new Error('Укажите id тендера или --position=<id>');
    const path = positionId
      ? `/api/v1/positions/${positionId}/boq-items-full`
      : `/api/v1/tenders/${tenderId}/boq-items-full`;
    console.log(JSON.stringify(await call('GET', path), null, 2));
    break;
  }

  case 'positions': {
    // Все позиции тендера (страницы склеиваются): сопоставить свои строки с id
    // существующих позиций, отобрать раздел по cost_category_name. Область
    // tenders:read. Порядок API — updated_at DESC, поэтому сортируем по номеру.
    const tenderId = positional[0];
    if (!tenderId) throw new Error('Укажите id тендера');
    const rows = [];
    let cursor = '';
    do {
      const res = await request('GET',
        `/api/v1/tenders/${tenderId}/positions?limit=200${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ''}`);
      const page = await res.json();
      if (!res.ok) {
        console.error(`HTTP ${res.status}${page.code ? ` [${page.code}]` : ''}: ${page.detail ?? page.title}`);
        process.exit(1);
      }
      rows.push(...page.data);
      cursor = page.next_cursor ?? '';
    } while (cursor);
    rows.sort((a, b) => a.position_number - b.position_number || a.id.localeCompare(b.id));
    const section = flags.get('section');
    console.log(JSON.stringify(
      section ? rows.filter((r) => (r.cost_category_name ?? '').toLowerCase().includes(section.toLowerCase())) : rows,
      null, 2,
    ));
    break;
  }

  case 'quality': {
    // Находки правил «Проверки данных». По умолчанию — компактно: без текста
    // «Суть» у каждой находки (он одинаков в группе — смотрите `rules`).
    // Нужны вердикты — берите rule_code, entity_id и fingerprint отсюда.
    const tenderId = positional[0];
    if (!tenderId) throw new Error('Укажите id тендера');
    const params = new URLSearchParams();
    if (flags.get('new') === 'true') params.set('only_new', '1');
    if (flags.get('open') === 'true') params.set('open_only', '1');
    if (flags.get('rule')) params.set('rule', flags.get('rule'));
    if (flags.get('refresh') === 'true') params.set('refresh', '1');
    const qs = params.toString();
    const rep = await call('GET', `/api/v1/tenders/${tenderId}/quality${qs ? `?${qs}` : ''}`);
    if (flags.get('full') !== 'true') {
      rep.findings = rep.findings.map((f) => ({
        rule_code: f.rule_code, severity: f.severity, position_number: f.position_number, item_no: f.item_no,
        entity_type: f.entity_type, entity_id: f.entity_id, fingerprint: f.fingerprint, detail: f.detail,
        money_delta: f.money_delta, verdict: f.verdict, note: f.note, is_new: f.is_new,
      }));
    }
    console.log(JSON.stringify(rep, null, 2));
    break;
  }

  case 'rules': {
    // Каталог правил: код, заголовок, severity, статус и «Суть».
    const rules = await call('GET', '/api/v1/quality/rules');
    console.log(JSON.stringify(rules.map((r) => ({
      code: r.Code, title: r.Title, severity: r.Severity, status: r.Status, entity_type: r.EntityType, summary: r.Summary,
    })), null, 2));
    break;
  }

  case 'sections': {
    // Готовность по разделам ВОР: расценено, открытые ошибки, отметка «проверено».
    const tenderId = positional[0];
    if (!tenderId) throw new Error('Укажите id тендера');
    console.log(JSON.stringify(await call('GET', `/api/v1/tenders/${tenderId}/verification/sections`), null, 2));
    break;
  }

  case 'benchmarks': {
    // ₽ за единицу объёма и ₽/м² СП против эталонов по классу жилья.
    const tenderId = positional[0];
    if (!tenderId) throw new Error('Укажите id тендера');
    const period = flags.get('period') ?? '24';
    console.log(JSON.stringify(
      await call('GET', `/api/v1/tenders/${tenderId}/cost-benchmarks?period_months=${encodeURIComponent(period)}`),
      null, 2,
    ));
    break;
  }

  case 'brief': {
    const tenderId = positional[0];
    if (!tenderId) throw new Error('Укажите id тендера');
    console.log(JSON.stringify(await call('GET', `/api/v1/tenders/${tenderId}/brief`), null, 2));
    break;
  }

  case 'verdicts': {
    // Пачка вердиктов из JSON-файла: [{rule_code, entity_id, fingerprint, verdict: accepted|error, note?}].
    // Область verification:write; вердикт пишется от имени владельца ключа.
    const [tenderId, file] = positional;
    if (!tenderId || !file) throw new Error('Укажите id тендера и JSON-файл с вердиктами');
    const { readFile } = await import('node:fs/promises');
    const items = JSON.parse(await readFile(file, 'utf8'));
    if (!Array.isArray(items) || items.length === 0) throw new Error('Файл должен содержать непустой массив');
    await call('POST', `/api/v1/tenders/${tenderId}/quality/verdicts`, { items });
    console.log(`Сохранено вердиктов: ${items.length}`);
    break;
  }

  case 'checkpoint': {
    // «Проверка завершена»: всё найденное сейчас — просмотрено, новыми дальше
    // станут только появившиеся после. Сдвигает точку и для людей в интерфейсе.
    const tenderId = positional[0];
    if (!tenderId) throw new Error('Укажите id тендера');
    const rep = await call('POST', `/api/v1/tenders/${tenderId}/quality/checkpoint`);
    console.log(JSON.stringify({ run_id: rep.run_id, checkpoint_at: rep.checkpoint_at, findings: rep.findings.length }, null, 2));
    break;
  }

  case 'brief-set': {
    // Текст выжимки для руководства из файла. Выбор категорий не трогается.
    const [tenderId, file] = positional;
    if (!tenderId || !file) throw new Error('Укажите id тендера и текстовый файл');
    const { readFile } = await import('node:fs/promises');
    const current = await call('GET', `/api/v1/tenders/${tenderId}/brief`);
    const text = await readFile(file, 'utf8');
    const saved = await call('PUT', `/api/v1/tenders/${tenderId}/brief`, {
      summary_text: text, fact_category_ids: current.fact_category_ids,
    });
    console.log(JSON.stringify({ updated_at: saved.updated_at, length: saved.summary_text.length }, null, 2));
    break;
  }

  case 'spec': {
    const res = await request('GET', '/api/v1/archive/openapi.yaml');
    console.log(await res.text());
    break;
  }

  default:
    console.error(`Команды: search | position | suggest | compose | tenders | tender | positions | costs | estimate | spec
          quality | rules | sections | benchmarks | brief | verdicts | checkpoint | brief-set
  node scripts/archive-api.mjs search "устройство стяжки" --unit=м2 --limit=5
  node scripts/archive-api.mjs position <uuid>
  node scripts/archive-api.mjs tenders --search=ЖК --archived=false   # список тендеров (tenders:read)
  node scripts/archive-api.mjs tender <tender_id>                     # шапка тендера
  node scripts/archive-api.mjs positions <tender_id> --section=монолит # позиции (раздел, заголовки)
  node scripts/archive-api.mjs costs <tender_id>                      # позиции с итогами и КП
  node scripts/archive-api.mjs estimate <tender_id>                   # строки сметы с названиями и ценами
  node scripts/archive-api.mjs estimate x --position=<position_id>    # строки одной позиции
  node scripts/archive-api.mjs suggest "кладка стен" "монтаж дверей"
  node scripts/archive-api.mjs compose ./compose.json            # проба (dry_run)
  node scripts/archive-api.mjs compose ./compose.json --no-dry-run --verbose
  node scripts/archive-api.mjs quality <tender_id> --new --open         # находки проверки (verification:read)
  node scripts/archive-api.mjs rules                                   # каталог правил
  node scripts/archive-api.mjs sections <tender_id>                    # готовность разделов
  node scripts/archive-api.mjs benchmarks <tender_id> --period=24      # сравнение с эталонами
  node scripts/archive-api.mjs brief <tender_id>                       # выжимка для руководства
  node scripts/archive-api.mjs verdicts <tender_id> ./verdicts.json    # вердикты (verification:write)
  node scripts/archive-api.mjs checkpoint <tender_id>                  # «Проверка завершена»
  node scripts/archive-api.mjs brief-set <tender_id> ./brief.txt       # записать текст выжимки`);
    process.exit(2);
}
