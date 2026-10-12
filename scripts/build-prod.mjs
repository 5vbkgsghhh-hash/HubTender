#!/usr/bin/env node
// Production frontend build with auto-derived Sentry release.
// Подставляет VITE_SENTRY_RELEASE = hubtender-web@<git-short-sha> в окружение,
// затем зовёт `vite build --mode production.yandex`. Кросс-платформенно — не
// использует bash-substitution `$(...)`, поэтому работает в PowerShell.
//
// Отдельный env-файл для сборки не нужен: VITE_* берутся из окружения процесса
// (на сервере deploy-server.sh загружает server/.env.prod), окружение
// приоритетнее env-файлов. Устаревший .env.production.yandex читается, только
// если ещё лежит рядом.

import { execSync, spawn } from 'node:child_process'
import { loadEnv } from 'vite'

const MODE = 'production.yandex'

// Тот же порядок, что применит сама сборка: файлы режима, поверх — process.env.
const apiUrl = loadEnv(MODE, process.cwd(), 'VITE_').VITE_API_URL
if (!apiUrl) {
  console.error('[build-prod] VITE_API_URL не задан — добавь его в env-файл (на сервере: server/.env.prod).')
  process.exit(1)
}
if (/\/\/(localhost|127\.0\.0\.1)([:/]|$)/.test(apiUrl)) {
  console.error('[build-prod] VITE_API_URL указывает на localhost — такой бандл на проде не заработает.')
  console.error('[build-prod] Боевая сборка идёт на сервере; локально: VITE_API_URL=https://… npm run build:prod')
  process.exit(1)
}

const sha = execSync('git rev-parse --short HEAD').toString().trim()
const release = `hubtender-web@${sha}`
console.log(`[build-prod] VITE_SENTRY_RELEASE=${release}`)

const child = spawn('npx', ['vite', 'build', '--mode', MODE], {
  stdio: 'inherit',
  shell: true,
  env: { ...process.env, VITE_SENTRY_RELEASE: release },
})

child.on('exit', (code) => process.exit(code ?? 1))
