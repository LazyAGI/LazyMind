/**
 * Stage echarts.min.js into <deck_dir>/assets/ so page HTML that references
 * `../assets/echarts.min.js` actually loads during Playwright export.
 *
 * Frontend preview rewrites that script src to the Vite-hosted bundle, so
 * charts look fine in the UI even when the deck has no assets/ folder. The
 * editable HTML→PPTX path loads the raw file from disk — without this file
 * `window.echarts` is undefined, getInstanceByDom finds nothing, and the
 * chart tile exports as an empty card (title/legend HTML survive, bars don't).
 */
import { copyFileSync, existsSync, mkdirSync, readdirSync, readFileSync, statSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const __dirname = dirname(fileURLToPath(import.meta.url));

function findEchartsBundle() {
  const depsDir = String(process.env.LAZYMIND_PPT_EXPORT_DEPS || '').trim();
  const candidates = [
    // Installed dependency ZIP (local/desktop)
    depsDir ? resolve(depsDir, 'node_modules/echarts/dist/echarts.min.js') : null,
    // Next to the exporter (docker volume / npm-installed export_pptx)
    resolve(__dirname, '../node_modules/echarts/dist/echarts.min.js'),
    // Local runtime copy used by desktop/local
    resolve(
      process.env.LAZYMIND_SUBAGENT_WORKSPACE || '/data/subagent',
      '.ppt_export_runtime/export_pptx/node_modules/echarts/dist/echarts.min.js',
    ),
  ].filter(Boolean);
  for (const path of candidates) {
    if (existsSync(path)) return path;
  }
  return null;
}

function pagesNeedEcharts(deckDir) {
  const pagesDir = resolve(deckDir, 'pages');
  if (!existsSync(pagesDir)) return false;
  for (const name of readdirSync(pagesDir)) {
    if (!/\.html?$/i.test(name)) continue;
    const html = readFileSync(resolve(pagesDir, name), 'utf-8');
    if (/echarts\.min\.js|echarts\.init|id=["']chart_/i.test(html)) return true;
  }
  return false;
}

/**
 * @param {string} deckDir
 * @returns {{ staged: boolean, path?: string, reason?: string }}
 */
export function ensureEchartsAsset(deckDir) {
  if (!deckDir || !pagesNeedEcharts(deckDir)) {
    return { staged: false, reason: 'no_chart_pages' };
  }
  const dstDir = resolve(deckDir, 'assets');
  const dst = resolve(dstDir, 'echarts.min.js');
  const src = findEchartsBundle();
  if (!src) {
    console.error('[echarts] bundle not found next to exporter; chart tiles will be empty');
    return { staged: false, reason: 'bundle_missing' };
  }
  mkdirSync(dstDir, { recursive: true });
  if (!existsSync(dst) || statSync(dst).size !== statSync(src).size) {
    copyFileSync(src, dst);
    console.error(`[echarts] staged → ${dst}`);
  }
  return { staged: true, path: dst };
}
