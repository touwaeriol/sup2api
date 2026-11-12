// Vite preset for sub2api native plugin UIs.
//
//   // vite.config.ts
//   import { defineConfig } from 'vite'
//   import { sub2apiPlugin } from '@sub2api/vite-preset'
//   export default defineConfig(sub2apiPlugin({ entry: 'src/entry.ts' }))
//
// Output: dist/entry.js (ES module exporting register/unregister), optional
// dist/entry.css (linked automatically by entry.js), dist/chunks/*.
// The shared modules below are provided by the console through an import map
// and must never be bundled into a plugin.

import vue from '@vitejs/plugin-vue'

export const SHARED_MODULES = ['vue', 'vue-router', 'pinia', 'vue-i18n', '@sub2api/ui', '@sub2api/host']

/** Host UI contract version the preset targets (manifest hostUICompat). */
export const HOST_UI_VERSION = '3.14.0'

function isShared(id) {
  return SHARED_MODULES.some(m => id === m || id.startsWith(m + '/'))
}

// Makes entry.js add <link rel="stylesheet"> for the extracted CSS, relative
// to its own URL (same origin as the console, so CSP needs no changes).
function cssLink() {
  return {
    name: 'sub2api:css-link',
    apply: 'build',
    enforce: 'post',
    generateBundle(_, bundle) {
      const css = Object.values(bundle).filter(f => f.type === 'asset' && f.fileName.endsWith('.css'))
      const entry = Object.values(bundle).find(f => f.type === 'chunk' && f.isEntry)
      if (!entry || css.length === 0) return
      const loader =
        `(function(){if(typeof document==='undefined')return;` +
        css
          .map(
            c =>
              `{var h=new URL(${JSON.stringify('./' + c.fileName)},import.meta.url).href;` +
              `if(!document.querySelector('link[href="'+h+'"]')){var l=document.createElement('link');l.rel='stylesheet';l.href=h;document.head.appendChild(l);}}`
          )
          .join('') +
        `})();\n`
      entry.code = loader + entry.code
    }
  }
}

/**
 * @param {import('./index').Sub2apiPluginOptions} [options]
 * @returns {import('vite').UserConfig}
 */
export function sub2apiPlugin(options = {}) {
  const { entry = 'src/entry.ts', outDir = 'dist', vue: vueOptions, minify = true } = options
  return {
    plugins: [vue(vueOptions), cssLink()],
    define: {
      'process.env.NODE_ENV': JSON.stringify('production'),
      __VUE_OPTIONS_API__: 'true',
      __VUE_PROD_DEVTOOLS__: 'false',
      __VUE_PROD_HYDRATION_MISMATCH_DETAILS__: 'false'
    },
    build: {
      outDir,
      emptyOutDir: true,
      target: 'es2022',
      minify,
      cssCodeSplit: false,
      lib: {
        entry,
        formats: ['es'],
        fileName: () => 'entry.js',
        cssFileName: 'entry'
      },
      rollupOptions: {
        external: isShared,
        output: {
          chunkFileNames: 'chunks/[name]-[hash].js',
          assetFileNames: info => {
            const name = (info.names && info.names[0]) || info.name || ''
            return name.endsWith('.css') ? 'entry.css' : 'assets/[name]-[hash][extname]'
          }
        }
      }
    }
  }
}

export default sub2apiPlugin
