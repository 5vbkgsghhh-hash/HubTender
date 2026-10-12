#!/usr/bin/env bash
# Этап 2.6 (§26/§29): ОБЯЗАТЕЛЬНЫЙ live-гейт — evaluation с реальным OpenRouter
# + staging smoke пилотного пути. Всё против ОДНОРАЗОВОЙ БД (docker postgres);
# production DSN не читается и не используется.
#
#   bash scripts/readiness/run-live-ai-gate.sh
#
# Ключ: OPENROUTER_API_KEY читается из .env ЛОКАЛЬНО в переменную и
# НИКОГДА не печатается (никакого set -x; все echo — только статусы).
# Артефакты (redacted JSON для отчёта приёмки): $LIVE_GATE_OUT (обязателен).
set -euo pipefail
export MSYS_NO_PATHCONV=1 MSYS2_ARG_CONV_EXCL="*"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

OUT="${LIVE_GATE_OUT:?LIVE_GATE_OUT (каталог артефактов вне репозитория) обязателен}"
mkdir -p "$OUT"

STAMP="$(date +%s)$RANDOM"
CONTAINER="hubtender-live-gate-pg-$STAMP"
DB="hubtender_live_gate"
PGPORT=$((55200 + RANDOM % 300))
APIPORT=$((8300 + RANDOM % 300))
API="http://127.0.0.1:$APIPORT"
# Git Bash: native openssl/go не понимают MSYS-путь /tmp/... при
# MSYS_NO_PATHCONV=1 — конвертируем в Windows-вид.
TMP="$(mktemp -d)"
command -v cygpath >/dev/null 2>&1 && TMP="$(cygpath -m "$TMP")"
BACKEND_PID=""

# ── Ключ: только в переменную, ни одного echo ────────────────────────────────
# Один env-файл — .env; устаревший .env.prod читается, только если ещё лежит рядом.
ENV_FILE=.env
[[ -f .env.prod ]] && ENV_FILE=.env.prod
OR_KEY="$(grep -m1 '^OPENROUTER_API_KEY=' "$ENV_FILE" | cut -d= -f2- | tr -d '\r"' || true)"
[[ -n "$OR_KEY" ]] || { echo "GATE FAILED: OPENROUTER_API_KEY не найден в $ENV_FILE" >&2; exit 1; }

cleanup() {
  local rc=$?
  echo "== cleanup (rc=$rc) =="
  [[ -n "$BACKEND_PID" ]] && kill "$BACKEND_PID" >/dev/null 2>&1 || true
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  rm -rf "$TMP"
  exit $rc
}
trap cleanup EXIT INT TERM
fail() { echo "GATE FAILED: $*" >&2; exit 1; }

wait_pg() {
  local ok=0 i
  for i in $(seq 1 90); do
    if docker exec "$CONTAINER" psql -U postgres -d "$DB" -tAc 'SELECT 1' >/dev/null 2>&1; then
      ok=$((ok+1)); [[ $ok -ge 2 ]] && return 0
    else ok=0; fi
    sleep 1
  done
  return 1
}

# JSON-хелпер без jq: jget <path> — печатает значение по точечному пути из stdin.
jget() {
  node -e '
let d="";process.stdin.on("data",c=>d+=c).on("end",()=>{
  let o;try{o=JSON.parse(d)}catch(e){process.exit(3)}
  for(const k of process.argv[1].split(".")){if(o==null)process.exit(3);o=o[k];}
  if(o===undefined||o===null)process.exit(3);
  console.log(typeof o==="object"?JSON.stringify(o):String(o));
})' "$1"
}

echo "== postgres ($CONTAINER, port $PGPORT) =="
docker run -d --name "$CONTAINER" -e POSTGRES_PASSWORD=livegate -e POSTGRES_DB="$DB" \
  -p "$PGPORT:5432" postgres:17 >/dev/null
wait_pg || fail "postgres not ready"
DSN="postgres://postgres:livegate@127.0.0.1:$PGPORT/$DB?sslmode=disable"
case "$DSN" in *mdb.yandexcloud.net*|*supabase*) fail "production DSN";; esac

psql_q() { docker exec -i "$CONTAINER" psql -U postgres -d "$DB" -v ON_ERROR_STOP=0 -q "$@"; }
psql_s() { docker exec -i "$CONTAINER" psql -U postgres -d "$DB" -v ON_ERROR_STOP=1 -q "$@"; }
sqlv()   { docker exec -i "$CONTAINER" psql -U postgres -d "$DB" -tAc "$1"; }

echo "== schema =="
for f in db/yandex/sql/*.sql; do psql_q -f - < "$f" >/dev/null 2>&1 || true; done
for f in db/yandex/incremental/*.sql; do psql_s -f - < "$f" >/dev/null || fail "incremental $(basename "$f")"; done

echo "== seed =="
psql_s <<'SQL' >/dev/null
CREATE EXTENSION IF NOT EXISTS pgcrypto;
INSERT INTO public.roles (code, name, color) VALUES
 ('administrator','Администратор','#f00'), ('engineer','Инженер','#00f')
ON CONFLICT (code) DO NOTHING;
INSERT INTO public.units (code, name) VALUES
 ('м2','кв. метр'), ('м3','куб. метр'), ('шт','штука'), ('м','метр'), ('кг','килограмм')
ON CONFLICT (code) DO NOTHING;
INSERT INTO auth.users (id, email, encrypted_password)
VALUES ('11e60000-0000-0000-0000-000000000001', 'live-admin@test.local', crypt('Test1234!', gen_salt('bf', 10)))
ON CONFLICT (id) DO NOTHING;
INSERT INTO public.users (id, email, full_name, role_code, access_status, access_enabled, allowed_pages)
VALUES ('11e60000-0000-0000-0000-000000000001', 'live-admin@test.local', 'Live Admin',
        'administrator', 'approved', true, '[]'::jsonb)
ON CONFLICT (id) DO NOTHING;
INSERT INTO auth.users (id, email, encrypted_password)
VALUES ('11e60000-0000-0000-0000-000000000003', 'live-pilot@test.local', crypt('Test1234!', gen_salt('bf', 10)))
ON CONFLICT (id) DO NOTHING;
INSERT INTO public.users (id, email, full_name, role_code, access_status, access_enabled, allowed_pages)
VALUES ('11e60000-0000-0000-0000-000000000003', 'live-pilot@test.local', 'Live Pilot',
        'engineer', 'approved', true, '[]'::jsonb)
ON CONFLICT (id) DO NOTHING;
INSERT INTO public.material_names (name, unit) VALUES
 ('Кабель ВВГнг(А)-LS 3х2,5', 'м'), ('Кабель ВВГнг(А)-LS 3х4', 'м'),
 ('e2e материал', 'шт')
ON CONFLICT DO NOTHING;
INSERT INTO public.work_names (name, unit) VALUES ('e2e работа', 'м2')
ON CONFLICT DO NOTHING;
INSERT INTO public.tenders (id, title, client_name, tender_number, version,
    usd_rate, eur_rate, cny_rate, financial_calculation_status)
VALUES ('11e60000-0000-0000-0000-0000000000e2', 'Live Gate Тендер', 'ООО Тест', 'LIVE-1', 1,
        90, 100, 12, 'calculated')
ON CONFLICT (id) DO NOTHING;
INSERT INTO public.client_positions (tender_id, position_number, work_name)
SELECT id, 1, 'Позиция 1' FROM public.tenders WHERE title = 'Live Gate Тендер'
ON CONFLICT DO NOTHING;
SQL
TENDER_ID="11e60000-0000-0000-0000-0000000000e2"
PILOT_ID="11e60000-0000-0000-0000-000000000003"

echo "== app JWT ключ (ephemeral) =="
openssl genrsa -out "$TMP/jwt.pem" 2048 >/dev/null 2>&1

echo "== backend (port $APIPORT, OFFICIAL OpenRouter base, live key) =="
(
  cd backend
  DATABASE_URL="$DSN" \
  PORT="$APIPORT" BIND_HOST=127.0.0.1 \
  APP_JWT_ISSUER=hubtender-livegate APP_JWT_AUDIENCE=hubtender-app \
  APP_JWT_KEY_ID=livegate APP_JWT_PRIVATE_KEY_PATH="$TMP/jwt.pem" \
  CORS_ORIGINS="http://127.0.0.1:$APIPORT" \
  OPENROUTER_API_KEY="$OR_KEY" \
  OPENROUTER_LIVE_TEST=true \
  APP_ENV=development \
  LOG_LEVEL=warn \
  go run ./cmd/server > "$TMP/backend.log" 2>&1
) &
BACKEND_PID=$!
for i in $(seq 1 90); do
  curl -fsS "$API/health" >/dev/null 2>&1 && break; sleep 1
done
curl -fsS "$API/health/db" >/dev/null || { tail -20 "$TMP/backend.log"; fail "backend not healthy"; }

login() { # $1=email → печатает access_token
  curl -fsS -X POST "$API/api/v1/auth/login" -H 'Content-Type: application/json' \
    -d "{\"email\":\"$1\",\"password\":\"Test1234!\"}" | jget access_token
}
ADMIN_T="$(login live-admin@test.local)" || fail "admin login"
PILOT_T="$(login live-pilot@test.local)" || fail "pilot login"

adm() { curl -fsS -H "Authorization: Bearer $ADMIN_T" "$@"; }
pil() { curl -fsS -H "Authorization: Bearer $PILOT_T" "$@"; }

echo "== §26.1: connection + каталог /models/user =="
adm -X POST "$API/api/v1/admin/ai/openrouter/test-connection" > "$OUT/01-connection.json"
adm "$API/api/v1/admin/ai/openrouter/models" > "$TMP/models.json"
jget data.models < "$TMP/models.json" > "$TMP/models-list.json" || fail "каталог пуст"

# Кандидаты: structured outputs + известный ZDR-endpoint (политика этапа 2.5
# требует zdr+deny без fallback'ов — не у всех моделей есть такие endpoint'ы).
CANDIDATES="$(node -e '
const models = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")) || [];
const ok = models.filter(m => m.structured_outputs_indicated && !m.is_free_variant);
// Live-замеры (probe): gemini-2.5-flash — 10 строк за ~7s; DeepInfra/SiliconFlow
// под strict-грамматикой ~10 ток/с (батч 8 ≈ 48s > 30s-таймаут политики).
const prefs = ["google/gemini-2.5-flash","qwen/qwen3-235b-a22b-2507",
               "deepseek/deepseek-chat-v3-0324","z-ai/glm-4.7"];
const out = prefs.filter(p => ok.some(x => x.id === p));
if (!out.length) process.exit(3);
console.log(out.join(" "));
' "$TMP/models-list.json")" || fail "нет подходящей structured-output модели"

echo "== §26.2: draft → live model test (платный вызов #1) =="
MODEL_ID=""
for cand in $CANDIDATES; do
  echo "пробую модель: $cand"
  adm -X PUT "$API/api/v1/admin/ai/nomenclature-settings" -H 'Content-Type: application/json' \
    -d "{\"selected_model_id\":\"$cand\"}" >/dev/null
  adm -X POST "$API/api/v1/admin/ai/nomenclature/test-model" > "$OUT/02-model-test.json"
  TEST_STATUS="$(jget data.settings.model_test.status < "$OUT/02-model-test.json" || true)"
  if [[ "$TEST_STATUS" == "passed" ]]; then MODEL_ID="$cand"; break; fi
  echo "  → $TEST_STATUS ($(jget data.report.error_code < "$OUT/02-model-test.json" || true))"
done
[[ -n "$MODEL_ID" ]] || { cat "$OUT/02-model-test.json"; fail "ни одна модель не прошла live model test"; }
echo "MODEL: $MODEL_ID (test passed)"

echo "== §26.3: бюджет + пилот (до eval, чтобы гейты видели) =="
adm -X PUT "$API/api/v1/admin/ai/nomenclature/rollout/settings" -H 'Content-Type: application/json' \
  -d '{"monthly_budget_usd":"5.00"}' >/dev/null
adm -X POST "$API/api/v1/admin/ai/nomenclature/pilot-users" -H 'Content-Type: application/json' \
  -d "{\"user_id\":\"$PILOT_ID\",\"bulk_confirmation_allowed\":false}" >/dev/null

echo "== §26.4: off → evaluation =="
adm -X POST "$API/api/v1/admin/ai/nomenclature/rollout/transition" -H 'Content-Type: application/json' \
  -d '{"target":"evaluation","confirmation":"evaluation","reason":"live gate"}' >/dev/null

echo "== §26.5: LIVE evaluation через CLI (платные вызовы #2-#3) =="
(
  cd backend
  DATABASE_URL="$DSN" OPENROUTER_API_KEY="$OR_KEY" OPENROUTER_LIVE_TEST=true \
  go run ./cmd/ai-nomenclature-eval --mode live --dataset synthetic \
    --confirm-live-provider-cost --save-summary
) > "$OUT/03-live-eval.json" 2> "$TMP/eval.err" || { cat "$TMP/eval.err"; fail "live evaluation"; }

echo "== §26.6: гейты и переход evaluation → pilot_individual =="
adm "$API/api/v1/admin/ai/nomenclature/rollout" > "$OUT/04-rollout-after-eval.json"
adm -X POST "$API/api/v1/admin/ai/nomenclature/rollout/transition" -H 'Content-Type: application/json' \
  -d '{"target":"pilot_individual","confirmation":"pilot_individual","reason":"live gate"}' \
  > "$TMP/transition-pilot.json" || { cat "$OUT/04-rollout-after-eval.json"; fail "переход в pilot_individual (гейты)"; }

echo "== §29: staging smoke — пилотный suggest (платный вызов #4) =="
node scripts/readiness/gen-e2e-xlsx.mjs >/dev/null
FIXTURE="tests/readiness/fixtures/e2e-boq-pilot.xlsx"
CONF='{"accept_formula_cached":false,"default_currency":"","default_boq_type":""}'
pil -X POST "$API/api/v1/tenders/$TENDER_ID/boq-import/analyze" \
  -F "file=@$FIXTURE" -F "confirmed_options=$CONF" > "$OUT/00-analyze.json"
FP="$(jget data.workbook_fingerprint < "$OUT/00-analyze.json")" || fail "analyze"
echo "analyze summary: $(jget data.summary < "$OUT/00-analyze.json")"
pil -X POST "$API/api/v1/tenders/$TENDER_ID/boq-import/suggest-nomenclature" \
  -F "file=@$FIXTURE" -F "confirmed_options=$CONF" -F "workbook_fingerprint=$FP" \
  > "$OUT/05-suggest.json"
AI_REQ="$(jget data.ai_request_id < "$OUT/05-suggest.json" || true)"
PROV_STATUS="$(jget data.provider.status < "$OUT/05-suggest.json" || true)"
echo "suggest: provider=$PROV_STATUS ai_request_id=${AI_REQ:+set}"
[[ -n "$AI_REQ" ]] || { cat "$OUT/05-suggest.json"; fail "live suggest не вернул ai_request_id"; }

# Подтверждение: предложенный кандидат (ai_confirmed) или первый кандидат (manual).
node -e '
const r = JSON.parse(require("fs").readFileSync(process.argv[1], "utf8")).data;
const sel = [];
for (const row of r.rows || []) {
  const id = row.selected_candidate_id || (row.candidates[0] && row.candidates[0].id);
  if (!id) continue;
  sel.push({ row_reference: row.row_reference, catalog_id: id,
             selection_source: row.selected_candidate_id ? "ai_confirmed" : "manual" });
}
if (!sel.length) process.exit(3);
require("fs").writeFileSync(process.argv[2], JSON.stringify(sel));
' "$OUT/05-suggest.json" "$TMP/selections.json" || fail "нет кандидатов для подтверждения"

echo "== §29: execute импорта с ai_request_id (feedback ПОСЛЕ импорта) =="
curl -sS -H "Authorization: Bearer $PILOT_T" \
  -X POST "$API/api/v1/tenders/$TENDER_ID/boq-import/execute" \
  -F "file=@$FIXTURE" -F "confirmed_options=$CONF" -F "workbook_fingerprint=$FP" \
  -F "nomenclature_selections=<$TMP/selections.json" -F "ai_request_id=$AI_REQ" \
  > "$OUT/06-execute.json"
jget data.import.inserted_items_count < "$OUT/06-execute.json" >/dev/null \
  || { cat "$OUT/06-execute.json"; fail "execute"; }

echo "== §29: леджер и feedback в БД =="
sqlv "SELECT request_status, cost_source,
             (actual_provider_cost IS NOT NULL) AS has_actual,
             reservation_underestimate
      FROM public.ai_usage_requests ORDER BY created_at" > "$OUT/07-ledger.txt"
sqlv "SELECT outcome, count(*) FROM public.ai_row_feedback GROUP BY outcome" > "$OUT/08-feedback.txt"
cat "$OUT/07-ledger.txt" "$OUT/08-feedback.txt"
grep -q "completed" "$OUT/07-ledger.txt" || fail "нет completed-записи в леджере"
[[ -s "$OUT/08-feedback.txt" ]] || fail "feedback-строки не финализированы"

adm "$API/api/v1/admin/ai/nomenclature/usage" > "$OUT/09-usage-summary.json"

echo "== §29: emergency off + деградация =="
CALLS_BEFORE="$(sqlv "SELECT count(*) FROM public.ai_usage_requests")"
adm -X POST "$API/api/v1/admin/ai/nomenclature/rollout/emergency-off" -H 'Content-Type: application/json' \
  -d '{"reason":"live gate staging smoke"}' >/dev/null
CAP="$(pil "$API/api/v1/ai/nomenclature-capability" | jget data.status)"
echo "capability after emergency off: $CAP"
[[ "$CAP" == "rollout_off" ]] || fail "capability после emergency off: $CAP"
pil -X POST "$API/api/v1/tenders/$TENDER_ID/boq-import/suggest-nomenclature" \
  -F "file=@$FIXTURE" -F "confirmed_options=$CONF" -F "workbook_fingerprint=$FP" \
  > "$OUT/10-suggest-after-off.json"
AI_REQ2="$(jget data.ai_request_id < "$OUT/10-suggest-after-off.json" || true)"
[[ -z "$AI_REQ2" ]] || fail "после emergency off suggest всё ещё live"
CALLS_AFTER="$(sqlv "SELECT count(*) FROM public.ai_usage_requests")"
[[ "$CALLS_BEFORE" == "$CALLS_AFTER" ]] || fail "после emergency off появились новые провайдер-запросы"

echo "== финал: rollout = off, ключ не в БД =="
MODE="$(adm "$API/api/v1/admin/ai/nomenclature/rollout" | jget data.rollout_mode)"
[[ "$MODE" == "off" ]] || fail "rollout не off: $MODE"
# Ключ никогда не хранится в БД: ни одной колонки с key/secret в ai-таблицах.
KEY_COLS="$(sqlv "SELECT count(*) FROM information_schema.columns
  WHERE table_schema='public' AND table_name LIKE 'ai\\_%'
    AND (column_name ILIKE '%key%' OR column_name ILIKE '%secret%')")"
[[ "$KEY_COLS" == "0" ]] || fail "в ai-таблицах есть key/secret-колонки: $KEY_COLS"
echo "rollout_mode=off; key-columns-in-ai-tables=0"

echo "LIVE AI GATE PASSED (artifacts: $OUT)"
