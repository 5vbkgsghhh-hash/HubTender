import { defineConfig, loadEnv, type Plugin, type PluginOption } from 'vite'
import react from '@vitejs/plugin-react'

export default defineConfig(async ({ mode }) => {
  const isProd = mode.startsWith('production')
  const env = loadEnv(mode, process.cwd(), '')
  const sentryAuthToken = process.env.SENTRY_AUTH_TOKEN || env.SENTRY_AUTH_TOKEN
  const sentryRelease = process.env.VITE_SENTRY_RELEASE || env.VITE_SENTRY_RELEASE
  const noSourcemap = process.env.BUILD_NO_SOURCEMAP === '1'
  const enableSentryUpload =
    isProd && !noSourcemap && Boolean(sentryAuthToken) && Boolean(sentryRelease)

  // Идентификатор сборки: git-хеш из VITE_SENTRY_RELEASE (его проставляет
  // scripts/build-prod.mjs), иначе — отметка времени сборки. По нему баннер
  // обновления понимает, какую именно версию пользователь отложил кнопкой «Позже».
  const buildId = sentryRelease?.split('@')[1] || String(Date.now())

  // version.json намеренно вне precache: globPatterns ниже покрывает только
  // js/css/html/svg/woff — иначе SW отдавал бы старую версию из кеша.
  const emitVersionJson: Plugin = {
    name: 'emit-version-json',
    generateBundle() {
      this.emitFile({
        type: 'asset',
        fileName: 'version.json',
        source: JSON.stringify({ version: buildId }),
      })
    },
  }

  const plugins: PluginOption[] = [react(), emitVersionJson]

  try {
    const { VitePWA } = await import('vite-plugin-pwa')
    plugins.push(
      VitePWA({
        // 'prompt', а не 'autoUpdate': новая версия применяется только по кнопке
        // «Обновить» в UpdateBanner — молчаливая перезагрузка теряла несохранённые
        // данные форм. Ожидающий SW всё равно активируется, когда закрыты все вкладки.
        registerType: 'prompt',
        injectRegister: false,
        includeAssets: ['logo.svg'],
        manifest: {
          name: 'TenderHUB - Портал управления тендерами',
          short_name: 'TenderHUB',
          description:
            'Система управления строительными тендерами, сметами и номенклатурами СУ-10.',
          lang: 'ru',
          theme_color: '#10b981',
          background_color: '#0a0a0a',
          display: 'standalone',
          start_url: '/',
          scope: '/',
          // PNG генерирует scripts/generate-icons.mjs (npm run icons:generate).
          icons: [
            { src: '/icon-192.png', sizes: '192x192', type: 'image/png', purpose: 'any' },
            { src: '/icon-512.png', sizes: '512x512', type: 'image/png', purpose: 'any' },
            { src: '/icon-512-maskable.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
            { src: '/logo.svg', sizes: 'any', type: 'image/svg+xml', purpose: 'any' },
          ],
        },
        workbox: {
          globPatterns: ['**/*.{js,css,html,svg,woff,woff2}'],
          navigateFallback: '/index.html',
          navigateFallbackDenylist: [/^\/api\//],
          runtimeCaching: [
            {
              urlPattern: ({ url }) => url.pathname.startsWith('/api/'),
              handler: 'NetworkOnly',
            },
          ],
          cleanupOutdatedCaches: true,
          maximumFileSizeToCacheInBytes: 5 * 1024 * 1024,
        },
        devOptions: { enabled: false },
      }),
    )
  } catch {
    console.warn('[vite] vite-plugin-pwa is not installed; PWA support is disabled.')
  }

  if (enableSentryUpload) {
    try {
      const { sentryVitePlugin } = await import('@sentry/vite-plugin')
      plugins.push(
        sentryVitePlugin({
          org: 'odintsovorg',
          project: 'hubtender-web',
          authToken: sentryAuthToken,
          release: sentryRelease ? { name: sentryRelease } : undefined,
        }),
      )
    } catch {
      console.warn('[vite] @sentry/vite-plugin is not installed; skipping sourcemap upload.')
    }
  }

  return {
    plugins,
    define: {
      __BUILD_ID__: JSON.stringify(buildId),
    },
    server: {
      port: 5185,
      open: true,
    },
    esbuild: isProd
      ? { drop: ['debugger'], pure: ['console.log', 'console.debug', 'console.info'] }
      : undefined,
    build: {
      outDir: 'dist',
      sourcemap: noSourcemap ? false : isProd ? 'hidden' : true,
      target: 'es2020',
      cssCodeSplit: true,
      chunkSizeWarningLimit: 800,
      rollupOptions: {
        maxParallelFileOps: 4,
        output: {
          manualChunks: {
            'vendor-react': ['react', 'react-dom', 'react-router-dom'],
            'vendor-antd': ['antd', '@ant-design/icons'],
            'vendor-charts': ['chart.js', 'react-chartjs-2', 'chartjs-plugin-datalabels'],
            'vendor-xlsx': ['xlsx', 'xlsx-js-style'],
          },
        },
      },
    },
  }
})
