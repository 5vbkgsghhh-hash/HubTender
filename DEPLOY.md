# Деплой HUBTender

Рабочая инструкция по деплою HUBTender на production-сервер
`tender.su10.ru`.

Главная идея: в production попадает то, что закоммичено и запушено в
`origin/main`. Перед деплоем нужные изменения должны быть в `main`.

Схема деплоя: git/исходники живут в одноразовом build-контексте
`/opt/hubtender-build`, сборка идёт там же. В папку сайта
`/srv/sites/tender.su10.ru` копируются только собранные артефакты
(`public/`). Рантайм-конфиг (`server/.env.prod`, `.certs/yandex-ca.pem`)
лежит в папке сайта и деплоем не трогается, кроме точечного патча
`SENTRY_ENVIRONMENT` / `SENTRY_RELEASE`.

Backend крутится в Docker через systemd-юнит `hubtender-bff.service`.
Образ собирается каждый деплой как `hubtender-api:prod`, контейнер
рестартуется через `systemctl`.

## Production

| Что | Значение |
|---|---|
| Домен | `https://tender.su10.ru` |
| Сервер | `45.80.128.254` |
| SSH | `ssh root@45.80.128.254` |
| Hostname | `hub` |
| Git remote | `origin` (`baldmaxim/HUBTender`), ветка `main` |
| Build-контекст | `/opt/hubtender-build` |
| Корень сайта | `/srv/sites/tender.su10.ru` |
| Frontend артефакты | `/srv/sites/tender.su10.ru/public/` |
| Env (бэкенд + сборка фронта) | `/srv/sites/tender.su10.ru/server/.env.prod` — единственный env-файл |
| Сертификат БД | `/srv/sites/tender.su10.ru/.certs/yandex-ca.pem` |
| Docker image | `hubtender-api:prod` |
| Backend service | systemd `hubtender-bff.service`, `127.0.0.1:3006` |
| Nginx vhost | `/etc/nginx/sites-available/tender.su10.ru` |

Runtime использует Yandex Managed PostgreSQL и Supabase Auth (временный bridge
для JWT). Supabase Realtime отключён, его заменяет нативный WS-хаб BFF.

## Самый частый деплой

Если ты уже на сервере по SSH, используй серверный скрипт. Он сам подтянет
свежий `origin/main` в `/opt/hubtender-build`:

```bash
ssh root@45.80.128.254
cd /opt/hubtender-build

bash scripts/deploy-server.sh --check
bash scripts/deploy-server.sh both
```

`--check` ничего не деплоит — он проверяет hostname, env-файлы, docker,
systemd, node/npm/rsync.

`both` деплоит backend и frontend подряд. Доступные scope:

```bash
bash scripts/deploy-server.sh frontend
bash scripts/deploy-server.sh backend
bash scripts/deploy-server.sh both
```

Что делает `scripts/deploy-server.sh`:

- проверяет, что hostname = `hub`, cwd = `/opt/hubtender-build`;
- проверяет наличие `server/.env.prod` (и `VITE_API_URL` в нём),
  `.certs/yandex-ca.pem`, доступность `docker`/`systemd`/`node`/`rsync`;
- синкает `/opt/hubtender-build` с `origin/main` (`git fetch` +
  `checkout -f -B main` + `reset --hard` + `clean -fd`);
- для backend: патчит `SENTRY_ENVIRONMENT=production` и
  `SENTRY_RELEASE=hubtender-api@<sha>` в `server/.env.prod`,
  `docker build -t hubtender-api:prod ./backend`,
  `systemctl restart hubtender-bff.service`, печатает `journalctl -n 50`,
  проверяет `/health` и `/health/db`;
- после backend: `docker image prune -f && docker builder prune -f` — удаляет
  dangling-образы (старый `hubtender-api:prod`) и build-кэш перед выкаткой фронта;
- для frontend: `npm ci` (если нет `node_modules`), загружает `VITE_*` и
  `SENTRY_AUTH_TOKEN` из `server/.env.prod`,
  `npm run build:prod` (внутри подставляется `VITE_SENTRY_RELEASE=hubtender-web@<sha>`),
  делает `cp -a` бэкап `public` → `public.backup-<TS>`, `rsync -a --delete dist/`
  в `public/`;
- печатает финальные URLs.

Локальные правки в `/opt/hubtender-build` затираются `git reset --hard` —
любые изменения катятся только через `origin/main`.

Полная пересборка с нуля (сметает `node_modules`):

```bash
BUILD_CLEAN_HARD=1 bash scripts/deploy-server.sh both
```

Сборка с другого remote/branch:

```bash
HUBTENDER_REMOTE=fork HUBTENDER_BRANCH=hotfix bash scripts/deploy-server.sh both
```

## Деплой с локального компьютера

`scripts/deploy-production.sh` — тонкий SSH-wrapper. Ничего локально не
собирает: всё происходит на сервере, код берётся из git на сервере.

Перед запуском убедись, что нужные изменения закоммичены и запушены:

```bash
git fetch origin main
git status --short
git rev-parse --short HEAD
git rev-parse --short origin/main

bash scripts/deploy-production.sh --check
bash scripts/deploy-production.sh both
```

Скрипт сам подключается к `root@45.80.128.254`, при необходимости делает
`git clone https://github.com/baldmaxim/HUBTender.git /opt/hubtender-build`,
синкает с `origin/main` и зовёт `scripts/deploy-server.sh`.

Доступные scope:

```bash
bash scripts/deploy-production.sh frontend
bash scripts/deploy-production.sh backend
bash scripts/deploy-production.sh both
```

Переменные для локального запуска:

```bash
HUBTENDER_SSH=root@45.80.128.254
HUBTENDER_BUILD_DIR=/opt/hubtender-build
HUBTENDER_REPO_URL=https://github.com/baldmaxim/HUBTender.git
HUBTENDER_REMOTE=origin
HUBTENDER_BRANCH=main
```

Сценарий «собрать локально и залить артефакты» не поддерживается —
production не должен зависеть от локального рабочего дерева.

## Полезные флаги

Флаги одинаковые для серверного и локального скриптов:

```bash
SKIP_VERIFY=1        bash scripts/deploy-server.sh both
FRONTEND_NPM_CI=1    bash scripts/deploy-server.sh frontend
BUILD_CLEAN_HARD=1   bash scripts/deploy-server.sh both
```

С локальной машины — то же самое:

```bash
SKIP_VERIFY=1        bash scripts/deploy-production.sh both
FRONTEND_NPM_CI=1    bash scripts/deploy-production.sh frontend
BUILD_CLEAN_HARD=1   bash scripts/deploy-production.sh both
```

| Флаг | Когда нужен |
|---|---|
| `SKIP_VERIFY=1` | curl до `/health` падает (например, на оффлайн-тесте), а сборку и рестарт всё равно нужно прогнать |
| `FRONTEND_NPM_CI=1` | поменялся `package-lock.json` или есть сомнение в `node_modules` |
| `BUILD_CLEAN_HARD=1` | хочешь чистую сборку «с нуля» |

> **ПРАВИЛО: менялся `package-lock.json` → деплой фронта только с `FRONTEND_NPM_CI=1`.**
> Скрипт делает `npm ci` лишь когда `node_modules` отсутствует. Если папка
> уже есть, новый lock игнорируется и в сборку попадают старые версии пакетов.
> Поэтому любой коммит, трогающий зависимости, катится так:
>
> ```bash
> FRONTEND_NPM_CI=1 bash scripts/deploy-server.sh frontend
> ```
>
> Пример — коммит `66e7e5a` «Безопасность зависимостей: react-router-dom 7.18,
> xlsx 0.20.3 (SheetJS CDN)»: без `FRONTEND_NPM_CI=1` security-фиксы не доедут
> до прода (останутся уязвимые версии в `node_modules`).

> **ПРАВИЛО: каждый деплой backend завершается чисткой docker.** После
> `docker build` и рестарта сервиса скрипт делает
> `docker image prune -f && docker builder prune -f` (шаг между backend и
> frontend — «предпредпоследний»). Удаляются только повисшие (`<none>`) образы —
> прежде всего старый `hubtender-api:prod` — и build-кэш; тегнутые образы
> (`hubtender-api:prod`, `hubtender-api:prod-prev` для отката) сохраняются. Так
> диск не забивается от деплоя к деплою.

## Первичная настройка `/opt/hubtender-build`

Одноразово, если build-контекст ещё не засеян.

```bash
ssh root@45.80.128.254
hostname    # должно быть: hub

# 1. Клон build-контекста
mkdir -p /opt
git clone https://github.com/baldmaxim/HUBTender.git /opt/hubtender-build
cd /opt/hubtender-build

# 2. Env — один файл server/.env.prod (шаблон — .env.example): ключи бэкенда
#    и VITE_API_URL, VITE_API_REALTIME_ENABLED, VITE_SENTRY_* для сборки фронта.
#    Отдельного env-файла в /opt/hubtender-build нет.

# 3. Убедиться, что папка сайта существует и в ней лежат env + сертификат
ls -la /srv/sites/tender.su10.ru/server/.env.prod
ls -la /srv/sites/tender.su10.ru/.certs/yandex-ca.pem

# 4. Проверить, что systemd-юнит зарегистрирован
systemctl status hubtender-bff.service

# 5. Прогон --check
bash scripts/deploy-server.sh --check

# 6. Первый деплой
bash scripts/deploy-server.sh both
```

## Ручной деплой backend

Обычно ручной деплой не нужен (`bash scripts/deploy-server.sh backend`), но
порядок такой:

```bash
ssh root@45.80.128.254
cd /opt/hubtender-build

git fetch origin main --prune
git checkout -f -B main origin/main
git reset --hard origin/main
git clean -fd

ENV_FILE=/srv/sites/tender.su10.ru/server/.env.prod
RELEASE_SHA="$(git rev-parse --short HEAD)"

grep -q '^SENTRY_ENVIRONMENT=' "$ENV_FILE" \
  && sed -i 's|^SENTRY_ENVIRONMENT=.*|SENTRY_ENVIRONMENT=production|' "$ENV_FILE" \
  || printf '\nSENTRY_ENVIRONMENT=production\n' >> "$ENV_FILE"

grep -q '^SENTRY_RELEASE=' "$ENV_FILE" \
  && sed -i "s|^SENTRY_RELEASE=.*|SENTRY_RELEASE=hubtender-api@$RELEASE_SHA|" "$ENV_FILE" \
  || printf 'SENTRY_RELEASE=hubtender-api@%s\n' "$RELEASE_SHA" >> "$ENV_FILE"

docker build -t hubtender-api:prod ./backend
systemctl restart hubtender-bff.service
sleep 2

journalctl -u hubtender-bff.service -n 50 --no-pager -o cat
curl -fsS http://127.0.0.1:3006/health
curl -fsS http://127.0.0.1:3006/health/db

# Чистка docker: убрать старый dangling-образ и build-кэш
docker image prune -f && docker builder prune -f
```

## Ручной деплой frontend

```bash
ssh root@45.80.128.254
cd /opt/hubtender-build

git fetch origin main --prune
git checkout -f -B main origin/main
git reset --hard origin/main
git clean -fd

npm ci

set -a
. /srv/sites/tender.su10.ru/server/.env.prod
set +a

npm run build:prod
test -f dist/index.html

SITE=/srv/sites/tender.su10.ru
TS="$(date +%Y%m%d-%H%M%S)"

[ -d "$SITE/public" ] && cp -a "$SITE/public" "$SITE/public.backup-$TS"
rsync -a --delete dist/ "$SITE/public/"
```

Frontend без systemd: после `rsync` сайт уже отдаёт новые файлы.

## Env и секреты

Секреты живут только на сервере и не коммитятся:

```text
/srv/sites/tender.su10.ru/server/.env.prod         # backend + VITE_* для сборки фронта
/srv/sites/tender.su10.ru/.certs/yandex-ca.pem     # CA для Yandex Managed PG
```

Перед ручным изменением env сделай бэкап:

```bash
ssh root@45.80.128.254
TS=$(date +%Y%m%d-%H%M%S)
mkdir -p /root/hubtender-env-backups/$TS
cp /srv/sites/tender.su10.ru/server/.env.prod        /root/hubtender-env-backups/$TS/server.env.prod
cp /srv/sites/tender.su10.ru/.certs/yandex-ca.pem    /root/hubtender-env-backups/$TS/yandex-ca.pem
```

Ключевые backend-переменные (`server/.env.prod`):

```env
PORT=3006
LOG_LEVEL=info
CORS_ORIGINS=https://tender.su10.ru

DATABASE_URL=postgres://...?sslmode=verify-full&sslrootcert=/srv/sites/tender.su10.ru/.certs/yandex-ca.pem
DB_MAX_CONNS=10

APP_JWT_ISSUER=https://tender.su10.ru/api
APP_JWT_AUDIENCE=hubtender
APP_JWT_PRIVATE_KEY_PATH=...

SENTRY_DSN=...
SENTRY_ENVIRONMENT=production
SENTRY_RELEASE=hubtender-api@<sha>          # автопатчится скриптом

SENTRY_AUTH_TOKEN=...                        # ВНИМАНИЕ: используется фронтом для source-map upload
```

Ключевые frontend-переменные — в том же `server/.env.prod` (скрипт загружает
файл перед `npm run build:prod`, в бандл попадают только `VITE_*`):

```env
VITE_API_URL=https://tender.su10.ru
VITE_API_REALTIME_ENABLED=true

VITE_SENTRY_DSN=...
VITE_SENTRY_ENVIRONMENT=production
# VITE_SENTRY_RELEASE подставляется автоматически из git SHA скриптом scripts/build-prod.mjs.
```

## Sentry release tagging

| Что | Тэг | Откуда берётся |
|---|---|---|
| Backend | `hubtender-api@<git-short-sha>` | патчится в `server/.env.prod` скриптом перед `docker build` |
| Frontend | `hubtender-web@<git-short-sha>` | подставляется в `VITE_SENTRY_RELEASE` внутри [scripts/build-prod.mjs](scripts/build-prod.mjs) и попадает в бандл |

Source maps фронта льёт `@sentry/vite-plugin` — для этого в окружении
сборки нужен `SENTRY_AUTH_TOKEN` (org и project заданы в `vite.config.ts`,
переменные `SENTRY_ORG` / `SENTRY_PROJECT` не читаются).
`deploy-server.sh frontend` поднимает его из `server/.env.prod` через
`set -a; . file; set +a`.

## Миграции БД

SQL-миграции применяются вручную (через `psql` к Yandex Managed PostgreSQL)
ДО или ПОСЛЕ деплоя — в зависимости от совместимости. Деплой-скрипты миграции
не запускают.

Канонический источник схемы — [db/yandex/sql/](db/yandex/sql/), миграции — [db/yandex/incremental/](db/yandex/incremental/).
Перед изменением схемы сверься с ним. Env — [docs/RUNTIME_ENV.md](docs/RUNTIME_ENV.md).

## Проверки после деплоя

На сервере:

```bash
systemctl status hubtender-bff.service
curl -fsS http://127.0.0.1:3006/health
curl -fsS http://127.0.0.1:3006/health/db
ss -tulpn | grep :3006
nginx -t
```

Снаружи:

```bash
curl -I https://tender.su10.ru/
curl -fsS https://tender.su10.ru/api/health
curl -fsS https://tender.su10.ru/api/health/db
```

В браузере после крупного деплоя проверь:

- логин (`/login`);
- список тендеров;
- открытие позиций BOQ внутри тендера;
- редактирование BOQ-item (запись через audit-обёртку);
- WebSocket-уведомления (DevTools → WS на `/api/v1/ws`);
- админку пользователей.

## Бэкапы фронта и rollback

Каждый деплой фронта создаёт `cp -a` копию `public/` в
`/srv/sites/tender.su10.ru/public.backup-<TS>`. Они НЕ чистятся
автоматически.

Откат фронта на предыдущий релиз:

```bash
ssh root@45.80.128.254
SITE=/srv/sites/tender.su10.ru
ls -dt $SITE/public.backup-* | head -n 5     # выбрать целевой бэкап
TARGET="$SITE/public.backup-20260523-153000"   # пример

TS=$(date +%Y%m%d-%H%M%S)
mv "$SITE/public" "$SITE/public.broken-$TS"
cp -a "$TARGET" "$SITE/public"
```

Откат backend через бэкап-образ:

```bash
docker image ls hubtender-api
# Если оставлен тэг предыдущего билда (например, hubtender-api:prod-prev):
docker tag hubtender-api:prod-prev hubtender-api:prod
systemctl restart hubtender-bff.service
```

Долгосрочный откат — revert в `main`, push, повторный `bash scripts/deploy-production.sh both`.

Чистка старых бэкапов (оставить последние 5):

```bash
ssh root@45.80.128.254 'ls -dt /srv/sites/tender.su10.ru/public.backup-* | tail -n +6 | xargs -r rm -rf'
```

## PM2

У HUBTender PM2 нет. Backend крутится в Docker через systemd. Для рестарта
используй `systemctl restart hubtender-bff.service`.

## Nginx и SSL

Обычно nginx трогать не нужно. Проверка конфига:

```bash
ssh root@45.80.128.254 'nginx -t'
```

Reload после осознанного изменения:

```bash
ssh root@45.80.128.254 'nginx -t && systemctl reload nginx'
```

Проверка продления сертификата:

```bash
ssh root@45.80.128.254 'certbot renew --dry-run'
```

## Частые проблемы

`502` или `connection refused` на `/api/*`:

```bash
systemctl status hubtender-bff.service
journalctl -u hubtender-bff.service -n 100 --no-pager
curl -fsS http://127.0.0.1:3006/health
docker ps | grep hubtender-api
```

`npm run build:prod` падает с `[build-prod] VITE_API_URL не задан` или
`VITE_API_URL указывает на localhost`:

```bash
grep '^VITE_API_URL=' /srv/sites/tender.su10.ru/server/.env.prod
# Должно быть VITE_API_URL=https://tender.su10.ru. Вне deploy-server.sh файл
# нужно загрузить самому: set -a; . /srv/sites/tender.su10.ru/server/.env.prod; set +a
```

Сборка фронта падает с `Killed` (`rendering chunks ... Killed`):

Это **kernel OOM** — серверу не хватило RAM на Vite-сборку (sourcemap + Sentry в
фазе `rendering chunks`). Не путать с `JavaScript heap out of memory` (это лимит
V8 со стектрейсом). Поднимать `--max-old-space-size` тут БЕСПОЛЕЗНО и только
ухудшит — нужно дать памяти, а не разрешить её больше есть.

```bash
free -h            # сколько RAM/swap; при <3 ГБ суммарно сборка рискует упасть
swapon --show
```

Штатный фикс — добавить swap-файл (сборка станет чуть медленнее, sourcemap в
Sentry сохраняются). Разово на сервере:

```bash
ssh root@45.80.128.254
fallocate -l 4G /swapfile || dd if=/dev/zero of=/swapfile bs=1M count=4096
chmod 600 /swapfile
mkswap /swapfile
swapon /swapfile
echo '/swapfile none swap sw 0 0' >> /etc/fstab   # чтобы пережил перезагрузку
free -h
```

Затем повторить (бэкенд уже задеплоен — достаточно `frontend`):

```bash
cd /opt/hubtender-build
bash scripts/deploy-server.sh frontend
```

Аварийный обход без swap — собрать без sourcemap (теряются читаемые трейсы в
Sentry до следующей нормальной сборки):

```bash
BUILD_NO_SOURCEMAP=1 bash scripts/deploy-server.sh frontend
```

`docker build` падает на `./backend`:

```bash
ls /opt/hubtender-build/backend/Dockerfile
docker build --no-cache -t hubtender-api:prod ./backend
```

Frontend открылся, но XHR ходит не туда:

```bash
grep '^VITE_API_URL=' /srv/sites/tender.su10.ru/server/.env.prod
# После изменения нужен пересбор: bash scripts/deploy-server.sh frontend
```

Sentry не получает source maps:

```bash
grep -c '^SENTRY_AUTH_TOKEN=.' /srv/sites/tender.su10.ru/server/.env.prod
# Должно быть 1 (токен задан; сам токен команда не печатает). После — пересобрать фронт.
```

Скрипт ругается на hostname:

```bash
hostname    # должно быть: hub
```

`./scripts/deploy-server.sh: Permission denied`:

```bash
# Запускай через `bash`, а не `./` — у скриптов нет бита +x:
bash scripts/deploy-server.sh both
# Либо разово выдай права:
chmod +x scripts/*.sh
```
