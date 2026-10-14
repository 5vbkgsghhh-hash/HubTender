#!/usr/bin/env node
// Рендерит PNG-иконки из public/logo.svg: apple-touch-icon (iOS), иконки
// манифеста (Android/десктопная установка PWA) и картинку превью ссылки.
// PNG руками не правим — меняем logo.svg и запускаем: npm run icons:generate

import sharp from 'sharp'
import { readFile } from 'node:fs/promises'
import { fileURLToPath } from 'node:url'
import { dirname, join } from 'node:path'

const publicDir = join(dirname(fileURLToPath(import.meta.url)), '..', 'public')
const svg = await readFile(join(publicDir, 'logo.svg'))

// Фон непрозрачный (background_color манифеста): прозрачный PNG на iOS даёт
// чёрный квадрат.
const BACKGROUND = '#0a0a0a'
// logo.svg объявлен 512×512 при 72 DPI
const SVG_SIZE = 512
// Доля стороны под логотип в maskable-иконке: поля по 10% с каждого края,
// иначе Android обрежет рисунок маской.
const MASKABLE_SCALE = 0.8

const targets = [
  { name: 'favicon-32.png', size: 32 },
  { name: 'apple-touch-icon.png', size: 180 },
  { name: 'apple-touch-icon-120.png', size: 120 },
  { name: 'apple-touch-icon-152.png', size: 152 },
  { name: 'apple-touch-icon-167.png', size: 167 },
  // Стандартные пути, которые iOS запрашивает сам, если иконка из ссылки в
  // странице не подошла (старая копия страницы из service worker ссылается на
  // SVG). Без файлов nginx отдаёт на них index.html — iOS рисует букву «T».
  { name: 'apple-touch-icon-180x180-precomposed.png', size: 180 },
  { name: 'apple-touch-icon-180x180.png', size: 180 },
  { name: 'apple-touch-icon-precomposed.png', size: 180 },
  { name: 'icon-192.png', size: 192 },
  { name: 'icon-512.png', size: 512 },
  { name: 'icon-512-maskable.png', size: 512, scale: MASKABLE_SCALE },
]

for (const { name, size, scale = 1 } of targets) {
  const inner = Math.round(size * scale)
  const pad = Math.round((size - inner) / 2)
  const density = Math.ceil((inner * 72) / SVG_SIZE) * 2
  await sharp(svg, { density })
    .resize(inner, inner)
    .flatten({ background: BACKGROUND })
    .extend({
      top: pad,
      bottom: size - inner - pad,
      left: pad,
      right: size - inner - pad,
      background: BACKGROUND,
    })
    .png({ compressionLevel: 9 })
    .toFile(join(publicDir, name))
  console.log(`✓ ${name} (${size}×${size})`)
}
