#!/usr/bin/env bash
# Серверный деплой HUBTender.
# Запускается на проде (root@45.80.128.254, hostname=hub) из /opt/hubtender-build.
# Сам синкает git, собирает, перезапускает backend, чистит docker, выкатывает frontend.
#
# Использование:
#   bash scripts/deploy-server.sh --check
#   bash scripts/deploy-server.sh backend
#   bash scripts/deploy-server.sh frontend
#   bash scripts/deploy-server.sh both
#
# Env-флаги:
#   SKIP_VERIFY=1        пропустить health-checks
#   FRONTEND_NPM_CI=1    принудительный npm ci (по умолчанию — только если нет node_modules)
#   BUILD_CLEAN_HARD=1   git clean -fdx (СМЕТЁТ node_modules)
#   HUBTENDER_REMOTE     по умолчанию origin
#   HUBTENDER_BRANCH     по умолчанию main

set -euo pipefail

readonly BUILD_DIR=/opt/hubtender-build
readonly SITE_DIR=/srv/sites/tender.su10.ru
# Единственный env-файл сервера: ключи бэкенда и VITE_* для сборки фронта.
readonly BACKEND_ENV="$SITE_DIR/server/.env.prod"
# Устаревший отдельный env фронта: читается Vite, только если ещё лежит рядом.
readonly LEGACY_FRONTEND_ENV="$BUILD_DIR/.env.production.yandex"
readonly DB_CA="$SITE_DIR/.certs/yandex-ca.pem"
readonly IMAGE=hubtender-api:prod
readonly SERVICE=hubtender-bff.service
readonly BFF_PORT=3006
readonly REMOTE="${HUBTENDER_REMOTE:-origin}"
readonly BRANCH="${HUBTENDER_BRANCH:-main}"

readonly C_RED='\033[0;31m'
readonly C_GREEN='\033[0;32m'
readonly C_YELLOW='\033[0;33m'
readonly C_BLUE='\033[0;34m'
readonly C_RESET='\033[0m'

log()  { printf '%b[deploy]%b %s\n' "$C_BLUE"  "$C_RESET" "$*"; }
ok()   { printf '%b[ ok ]%b  %s\n' "$C_GREEN" "$C_RESET" "$*"; }
warn() { printf '%b[warn]%b  %s\n' "$C_YELLOW" "$C_RESET" "$*"; }
fail() { printf '%b[FAIL]%b  %s\n' "$C_RED"   "$C_RESET" "$*" >&2; exit 1; }

usage() {
  cat <<EOF
Usage: bash scripts/deploy-server.sh <scope>

Scopes:
  --check     только проверки, ничего не деплоит
  backend     пересобрать образ $IMAGE и перезапустить $SERVICE
  frontend    npm run build:prod и rsync dist/ в $SITE_DIR/public/
  both        backend, затем frontend

Env: SKIP_VERIFY, FRONTEND_NPM_CI, BUILD_CLEAN_HARD,
     HUBTENDER_REMOTE (default $REMOTE), HUBTENDER_BRANCH (default $BRANCH).
EOF
}

# VITE_* для сборки фронта: из $BACKEND_ENV; отдельный файл — только как
# устаревший запасной вариант, чтобы первый деплой после переезда не упал.
check_frontend_env() {
  if grep -q '^VITE_API_URL=' "$BACKEND_ENV"; then
    ok "frontend env: VITE_* в $BACKEND_ENV"
    [ ! -f "$LEGACY_FRONTEND_ENV" ] || warn "$LEGACY_FRONTEND_ENV больше не нужен — удали его"
  elif [ -f "$LEGACY_FRONTEND_ENV" ]; then
    warn "VITE_* лежат в $LEGACY_FRONTEND_ENV — перенеси VITE_API_URL и VITE_API_REALTIME_ENABLED в $BACKEND_ENV и удали файл"
  else
    fail "нет VITE_API_URL — добавь VITE_API_URL и VITE_API_REALTIME_ENABLED в $BACKEND_ENV"
  fi
}

preflight() {
  log "preflight"

  local host; host="$(hostname)"
  [ "$host" = "hub" ] || fail "ожидался hostname=hub, получено: $host"
  ok "hostname=$host"

  [ "$PWD" = "$BUILD_DIR" ] || fail "запускай из $BUILD_DIR (сейчас $PWD)"
  ok "cwd=$PWD"

  [ -d "$BUILD_DIR/.git" ] || fail "$BUILD_DIR не git-репозиторий"
  ok "git-репозиторий найден"

  [ -f "$BACKEND_ENV" ] || fail "нет env: $BACKEND_ENV"
  ok "env: $BACKEND_ENV"

  check_frontend_env

  [ -f "$DB_CA" ] || warn "не найден сертификат БД: $DB_CA (проверь DATABASE_URL sslrootcert)"

  command -v docker >/dev/null || fail "docker не установлен"
  ok "docker: $(docker --version | head -n1)"

  systemctl list-unit-files "$SERVICE" >/dev/null 2>&1 || fail "systemd unit $SERVICE не зарегистрирован"
  ok "systemd unit: $SERVICE"

  command -v node >/dev/null || fail "node не установлен"
  command -v npm  >/dev/null || fail "npm не установлен"
  ok "node $(node --version), npm $(npm --version)"

  command -v rsync >/dev/null || fail "rsync не установлен"
  ok "rsync найден"
}

git_sync() {
  log "git sync $REMOTE/$BRANCH"
  git fetch "$REMOTE" "$BRANCH" --prune
  git checkout -f -B "$BRANCH" "$REMOTE/$BRANCH"
  git reset --hard "$REMOTE/$BRANCH"
  if [ "${BUILD_CLEAN_HARD:-0}" = "1" ]; then
    warn "BUILD_CLEAN_HARD=1 → git clean -fdx (сметёт node_modules)"
    git clean -fdx
    # clean -fdx сносит и устаревший .env.production.yandex — VITE_* должны быть в $BACKEND_ENV.
    check_frontend_env
  else
    git clean -fd
  fi
  ok "HEAD: $(git rev-parse --short HEAD) — $(git log -1 --pretty=%s)"
}

deploy_backend() {
  log "backend deploy"

  local sha; sha="$(git rev-parse --short HEAD)"

  log "patch SENTRY_ENVIRONMENT=production в $BACKEND_ENV"
  if grep -q '^SENTRY_ENVIRONMENT=' "$BACKEND_ENV"; then
    sed -i 's|^SENTRY_ENVIRONMENT=.*|SENTRY_ENVIRONMENT=production|' "$BACKEND_ENV"
  else
    printf '\nSENTRY_ENVIRONMENT=production\n' >> "$BACKEND_ENV"
  fi

  log "patch SENTRY_RELEASE=hubtender-api@$sha"
  if grep -q '^SENTRY_RELEASE=' "$BACKEND_ENV"; then
    sed -i "s|^SENTRY_RELEASE=.*|SENTRY_RELEASE=hubtender-api@$sha|" "$BACKEND_ENV"
  else
    printf 'SENTRY_RELEASE=hubtender-api@%s\n' "$sha" >> "$BACKEND_ENV"
  fi

  log "docker build -t $IMAGE ./backend"
  docker build -t "$IMAGE" ./backend

  log "systemctl restart $SERVICE"
  systemctl restart "$SERVICE"
  sleep 2

  log "journalctl (последние 50 строк)"
  journalctl -u "$SERVICE" -n 50 --no-pager -o cat || true

  if [ "${SKIP_VERIFY:-0}" = "1" ]; then
    warn "SKIP_VERIFY=1 → health-checks пропущены"
  else
    log "GET http://127.0.0.1:$BFF_PORT/health"
    curl -fsS "http://127.0.0.1:$BFF_PORT/health" && echo
    log "GET http://127.0.0.1:$BFF_PORT/health/db"
    curl -fsS "http://127.0.0.1:$BFF_PORT/health/db" && echo
  fi

  ok "backend release=hubtender-api@$sha"
}

docker_cleanup() {
  log "docker cleanup: docker image prune -f && docker builder prune -f"
  docker image prune -f && docker builder prune -f \
    || warn "docker cleanup завершился с ошибкой — не критично, продолжаю"
  ok "docker очищен: dangling-образы и build-кэш"
}

deploy_frontend() {
  log "frontend deploy"

  if [ "${FRONTEND_NPM_CI:-0}" = "1" ] || [ ! -d node_modules ]; then
    log "npm ci"
    npm ci
  else
    log "node_modules уже есть, пропускаю npm ci (форсируй FRONTEND_NPM_CI=1)"
  fi

  log "загрузка VITE_* и SENTRY_AUTH_TOKEN из $BACKEND_ENV"
  set -a
  # shellcheck disable=SC1090
  . "$BACKEND_ENV"
  set +a
  [ -n "${SENTRY_AUTH_TOKEN:-}" ] || warn "SENTRY_AUTH_TOKEN пуст — source maps не уйдут в Sentry"

  # Память: Vite-сборка с sourcemap в фазе rendering chunks легко съедает 1.5–3 ГБ.
  # Если суммарно (свободная RAM + swap) мало — предупреждаем и подсказываем фикс,
  # иначе ядро прибьёт node по OOM (в логе — голое `Killed`).
  local mem_avail_mb swap_total_mb
  mem_avail_mb="$(awk '/^MemAvailable:/ {print int($2/1024)}' /proc/meminfo 2>/dev/null || echo 0)"
  swap_total_mb="$(awk '/^SwapTotal:/ {print int($2/1024)}' /proc/meminfo 2>/dev/null || echo 0)"
  log "память: MemAvailable=${mem_avail_mb}MB, SwapTotal=${swap_total_mb}MB"
  if [ "$(( mem_avail_mb + swap_total_mb ))" -lt 3072 ]; then
    warn "доступно <3 ГБ (RAM+swap) — Vite может словить OOM (Killed) в фазе rendering chunks"
    warn "фикс: добавь swap (см. DEPLOY.md → «Сборка фронта падает с Killed») или собери с BUILD_NO_SOURCEMAP=1"
  fi

  log "npm run build:prod"
  npm run build:prod
  [ -f dist/index.html ] || fail "dist/index.html не появился (вероятно OOM — см. DEPLOY.md «Сборка фронта падает с Killed»)"

  local ts; ts="$(date +%Y%m%d-%H%M%S)"
  local backup_dir="$SITE_DIR/backups/public"
  if [ -d "$SITE_DIR/public" ]; then
    mkdir -p "$backup_dir"
    log "бэкап $SITE_DIR/public → $backup_dir/public.backup-$ts"
    cp -a "$SITE_DIR/public" "$backup_dir/public.backup-$ts"
  else
    warn "$SITE_DIR/public ещё нет — пропуск бэкапа"
  fi

  log "rsync dist/ → $SITE_DIR/public/"
  rsync -a --delete dist/ "$SITE_DIR/public/"

  ok "frontend release=hubtender-web@$(git rev-parse --short HEAD)"
}

summary() {
  local sha; sha="$(git rev-parse --short HEAD)"
  echo
  ok "deploy done — release $sha"
  echo "  Frontend: https://tender.su10.ru/"
  echo "  Backend:  https://tender.su10.ru/api/health (loopback: http://127.0.0.1:$BFF_PORT/health)"
}

main() {
  local scope="${1:-}"
  case "$scope" in
    --check)
      preflight
      ok "--check passed"
      ;;
    backend)
      preflight
      git_sync
      deploy_backend
      docker_cleanup
      summary
      ;;
    frontend)
      preflight
      git_sync
      deploy_frontend
      summary
      ;;
    both)
      preflight
      git_sync
      deploy_backend
      docker_cleanup
      deploy_frontend
      summary
      ;;
    ""|-h|--help)
      usage
      [ -z "$scope" ] && exit 1 || exit 0
      ;;
    *)
      usage
      fail "неизвестный scope: $scope"
      ;;
  esac
}

main "$@"
