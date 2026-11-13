import { readdir, readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import path from 'node:path';
import os from 'node:os';
import { pathToFileURL } from 'node:url';
import { portableFontFaces } from './font_embedding.mjs';

// TODO: use cloud object storage + local cache here later; preserve hash IDs.
export const fontLibraryRoot = () => path.resolve(process.env.LAZYMIND_FONT_DIR || path.join(os.homedir(), '.local/share/lazymind/fonts'));

export async function localFontFaces() {
  const root = fontLibraryRoot();
  let names;
  try { names = await readdir(root); } catch (error) {
    if (error.code === 'ENOENT') return [];
    throw error;
  }
  const faces = [];
  for (const name of names.filter(name => /^[a-f0-9]{64}\.json$/.test(name)).sort()) {
    try {
      const record = JSON.parse(await readFile(path.join(root, name), 'utf8'));
      if (!record.embeddable || ![`${record.id}.ttf`, `${record.id}.otf`].includes(record.file)
          || !/^[a-f0-9]{64}$/.test(record.id)) continue;
      const data = await readFile(path.join(root, record.file));
      if (createHash('sha256').update(data).digest('hex') !== record.id) continue;
      for (const family of record.aliases) {
        faces.push({ ...record, family, data, url: record.file });
      }
    } catch (error) {
      process.stderr.write(`[fonts] Invalid local font ${name}: ${error.message}\n`);
    }
  }
  return faces;
}

const managedFontRoute = 'https://lazymind-fonts.invalid/static-files/presentation-fonts/**';
const managedFontHandlers = new WeakMap();

/** File-origin renderers cannot load API-relative URLs. Serve only verified fonts
 * through a renderer-local route; the HTML on disk keeps its normal resource URL.
 */
export async function prepareManagedFontResources(page, htmls) {
  const localFaces = await localFontFaces();
  const resources = new Map();
  const prepared = htmls.map(html => {
    // Legacy inline subsets remain directly renderable even without the full
    // original font; only native PPTX embedding needs their original bytes.
    const required = [...html.matchAll(/<style\b[^>]*data-lazymind-font-required="true"[^>]*>[\s\S]*?<\/style>/gi)]
      .map(match => match[0]).filter(block => [...block.matchAll(/src:url\(["']?([^\s"')]+)["']?\)/g)]
        .some(([, source]) => !source.startsWith('data:'))).join('');
    const fonts = portableFontFaces(required, localFaces, { resourceUrlsOnly: true });
    let resolved = html;
    for (const face of fonts) {
      const extension = new URL(face.source, 'https://lazymind.invalid').pathname.split('.').at(-1);
      const url = `https://lazymind-fonts.invalid/static-files/presentation-fonts/${face.sha256}.${extension}`;
      resources.set(url, { body: face.data, contentType: `font/${extension}` });
      resolved = resolved.replace(/<style\b[^>]*data-lazymind-font-required="true"[^>]*>[\s\S]*?<\/style>/gi,
        block => block.replaceAll(face.source, url));
    }
    return {
      html: resolved,
      fontRequests: fonts.map(face => `${face.style} ${face.weight} 16px ${JSON.stringify(face.family)}`),
    };
  });
  const previous = managedFontHandlers.get(page);
  if (previous) await page.unroute(managedFontRoute, previous);
  if (resources.size) {
    const handler = route => {
      const resource = resources.get(route.request().url());
      return resource ? route.fulfill({ status: 200, ...resource,
        headers: { 'Access-Control-Allow-Origin': '*', 'Cache-Control': 'no-store' } }) : route.abort();
    };
    await page.route(managedFontRoute, handler);
    managedFontHandlers.set(page, handler);
  } else {
    managedFontHandlers.delete(page);
  }
  return prepared;
}

export async function loadManagedFontResources(page) {
  const [prepared] = await prepareManagedFontResources(page, [await page.content()]);
  if (!prepared.fontRequests.length) return;
  await page.evaluate(async ({ html, fontRequests }) => {
    const resolved = new DOMParser().parseFromString(html, 'text/html');
    const originals = [...document.querySelectorAll('style[data-lazymind-font-required="true"]')];
    [...resolved.querySelectorAll('style[data-lazymind-font-required="true"]')].forEach((style, index) => {
      originals[index].textContent = style.textContent;
    });
    for (const request of fontRequests) {
      if (!(await document.fonts.load(request)).length) throw new Error(`Required font did not load: ${request}`);
    }
  }, prepared);
}

function fontFaceRule(face, family, useFileUrls) {
  const source = useFileUrls
    ? pathToFileURL(path.join(fontLibraryRoot(), face.file)).href
    : `data:font/ttf;base64,${face.data.toString('base64')}`;
  return `@font-face{font-family:${JSON.stringify(family).replaceAll('<', '\\3c ')};font-weight:${face.weight};font-style:${face.style};src:url(${JSON.stringify(source)})}`;
}

export async function localFontCss(html, { useFileUrls = false } = {}) {
  const packagedFamilies = new Set();
  const packaged = new Set([...html.matchAll(/\/\*lazymind-font:([A-Za-z0-9+/=]+)\*\//g)].flatMap(([, token]) => {
    try {
      const record = JSON.parse(Buffer.from(token, 'base64').toString('utf8'));
      packagedFamilies.add(record.family.toLowerCase());
      return [record.sha256];
    } catch { return []; }
  }));
  const faces = await localFontFaces();
  const knownFamilies = new Set(faces.map(face => face.family.toLowerCase()));
  const usedFamilies = new Set();
  for (const [, declaration] of html.matchAll(/font-family\s*:\s*([^;}]+)/gi)) {
    const first = declaration.split(',').map(name => name.trim().replace(/^['"]|['"]$/g, '').toLowerCase())
      .find(name => knownFamilies.has(name));
    if (first) usedFamilies.add(first);
  }
  const selected = new Set();
  return faces.filter(face => !packaged.has(face.id)
    && !face.aliases.some(alias => packagedFamilies.has(alias.toLowerCase()))
    && html.toLowerCase().includes(face.family.toLowerCase())).filter(face => {
    // Aliases describe the same bytes, not separate fonts. Install one used
    // alias per face and one version per CSS face: duplicate CJK data URLs can
    // otherwise exceed Chromium's protocol message limit during addStyleTag.
    if (usedFamilies.size) {
      if (!usedFamilies.has(face.family.toLowerCase())) return false;
    } else {
      const alias = face.aliases.find(name => html.toLowerCase().includes(name.toLowerCase()));
      if (face.family !== alias) return false;
    }
    const key = JSON.stringify([face.family.toLowerCase(), face.weight, face.style]);
    if (selected.has(key)) return false;
    selected.add(key);
    return true;
  }).map(face => fontFaceRule(face, face.family, useFileUrls)).join('\n');
}

/** Substitute only unavailable CJK families with the managed, embeddable font.
 * Install before DOM measurement so geometry and embedded font bytes agree.
 * Never alias a font that is already installed or supplied by the document.
 */
export async function installMissingFontFallbacks(page, { useFileUrls = false } = {}) {
  const faces = (await localFontFaces()).filter(face => face.family === 'Noto Sans SC');
  if (!faces.length) return [];
  const missing = await page.evaluate(async () => {
    const allowed = new Set(['microsoft yahei', '微软雅黑', 'pingfang sc', 'noto sans cjk sc']);
    const names = new Set();
    for (const el of document.querySelectorAll('body *')) {
      for (const part of getComputedStyle(el).fontFamily.split(',')) {
        const name = part.trim().replace(/^['"]|['"]$/g, '');
        if (allowed.has(name.toLowerCase())) names.add(name);
      }
    }
    const missing = [];
    for (const name of names) {
      // document.fonts.check alone returns true for nonexistent families.
      if ([...document.fonts].some(face => face.family.replace(/^['"]|['"]$/g, '').toLowerCase() === name.toLowerCase())) continue;
      try { await new FontFace('__ppt_font_probe', `local(${JSON.stringify(name)})`).load(); }
      catch { missing.push(name); }
    }
    return missing;
  });
  if (!missing.length) return [];
  const css = missing.flatMap(family => faces.map(face => fontFaceRule(face, family, useFileUrls))).join('\n');
  await page.addStyleTag({ content: css });
  await page.evaluate(() => document.fonts.ready);
  process.stderr.write(`[fonts] Missing ${missing.join(', ')}; using managed Noto Sans SC before layout measurement.\n`);
  return missing;
}
