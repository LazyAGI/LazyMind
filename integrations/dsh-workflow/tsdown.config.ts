import { defineConfig } from 'tsdown'
import { dshBuildVersion } from './release-version.mjs'

const define = { __LAZYMIND_DSH_BUILD__: JSON.stringify(dshBuildVersion) }

/**
 * Build a regular DSH Host plugin plus the browser module-table artifact.
 *
 * This package lives outside the DSH workspace, so it cannot use DSH's
 * workspace-only tsdown preset. The browser half follows the public dynamic
 * client protocol: a CJS factory registered in `window.__ModuleLoader__`.
 */
export default defineConfig([
  {
    entry: { index: 'src/index.ts' },
    outDir: 'lib',
    define,
    format: 'esm',
    platform: 'node',
    external: [/^@deepseek-ai\//],
    target: 'es2024',
    fixedExtension: false,
    dts: false,
    clean: true,
  },
  {
    entry: { client: 'src/client/index.tsx' },
    outDir: 'lib',
    define,
    format: 'cjs',
    platform: 'browser',
    target: 'es2024',
    dts: false,
    clean: false,
    external: ['react', 'react/jsx-runtime', 'react-dom'],
    outputOptions: {
      entryFileNames: 'client.js',
      banner: 'window.__ModuleLoader__.load({ id: "@lazymind/dsh-workflow", factory: (require) => {',
      intro: 'var module = { exports: {} }; var exports = module.exports;',
      footer: 'return module.exports; } });',
    },
  },
])
