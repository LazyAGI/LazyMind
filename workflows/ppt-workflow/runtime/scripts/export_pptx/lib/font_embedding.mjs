// Embed Google Fonts used by HTML slides into the generated PPTX.
//
// Browser previews can load a web font that does not exist on the machine
// opening the exported deck. Merely writing its family name into slide XML is
// therefore insufficient: PowerPoint substitutes another font. PresentationML
// stores embeddable fonts as EOT data in ppt/fonts/*.fntdata, referenced from
// p:embeddedFontLst in ppt/presentation.xml.

import JSZip from 'jszip';
import { readFile, writeFile } from 'node:fs/promises';

import { parseFontFamily } from './style_parser.mjs';

const GOOGLE_CSS_HOST = 'fonts.googleapis.com';
const GOOGLE_FONT_HOST = 'fonts.gstatic.com';
const FETCH_TIMEOUT_MS = 15_000;
const MAX_CSS_BYTES = 512 * 1024;
const MAX_FONT_BYTES = 32 * 1024 * 1024;
const MAX_EMBEDDED_FONTS = 8;
const MAX_TOTAL_FONT_BYTES = 64 * 1024 * 1024;
const FONT_RELATIONSHIP = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships/font';

function normalizeFamily(value) {
  return String(value || '').trim().replace(/^['"]|['"]$/g, '').toLowerCase();
}

function xmlEscape(value) {
  return String(value || '')
    .replaceAll('&', '&amp;')
    .replaceAll('<', '&lt;')
    .replaceAll('>', '&gt;')
    .replaceAll('"', '&quot;')
    .replaceAll("'", '&apos;');
}

function decodeHtmlEntities(value) {
  return String(value || '')
    .replaceAll('&amp;', '&')
    .replaceAll('&quot;', '"')
    .replaceAll('&#39;', "'");
}

function readUInt16BE(buffer, offset) {
  if (offset < 0 || offset + 2 > buffer.length) throw new Error('invalid sfnt uint16 offset');
  return buffer.readUInt16BE(offset);
}

function readUInt32BE(buffer, offset) {
  if (offset < 0 || offset + 4 > buffer.length) throw new Error('invalid sfnt uint32 offset');
  return buffer.readUInt32BE(offset);
}

function readSfntTables(buffer) {
  const signature = buffer.subarray(0, 4).toString('latin1');
  if (signature !== '\u0000\u0001\u0000\u0000' && signature !== 'OTTO' && signature !== 'true') {
    throw new Error('font source is not an OpenType/TrueType sfnt');
  }
  const count = readUInt16BE(buffer, 4);
  if (12 + count * 16 > buffer.length) throw new Error('truncated sfnt table directory');
  const tables = new Map();
  for (let index = 0; index < count; index++) {
    const record = 12 + index * 16;
    const tag = buffer.subarray(record, record + 4).toString('latin1');
    const offset = readUInt32BE(buffer, record + 8);
    const length = readUInt32BE(buffer, record + 12);
    if (offset + length <= buffer.length) tables.set(tag, { offset, length });
  }
  return tables;
}

function decodeUtf16BE(buffer) {
  const evenLength = buffer.length - (buffer.length % 2);
  const littleEndian = Buffer.alloc(evenLength);
  for (let index = 0; index < evenLength; index += 2) {
    littleEndian[index] = buffer[index + 1];
    littleEndian[index + 1] = buffer[index];
  }
  return littleEndian.toString('utf16le').replace(/\0/g, '').trim();
}

function readSfntNames(buffer, tables) {
  const table = tables.get('name');
  if (!table || table.length < 6) return {};
  const { offset } = table;
  const count = readUInt16BE(buffer, offset + 2);
  const storageOffset = offset + readUInt16BE(buffer, offset + 4);
  const names = new Map();
  for (let index = 0; index < count; index++) {
    const record = offset + 6 + index * 12;
    if (record + 12 > offset + table.length) break;
    const platform = readUInt16BE(buffer, record);
    const encoding = readUInt16BE(buffer, record + 2);
    const language = readUInt16BE(buffer, record + 4);
    const nameId = readUInt16BE(buffer, record + 6);
    const length = readUInt16BE(buffer, record + 8);
    const stringOffset = storageOffset + readUInt16BE(buffer, record + 10);
    if (![1, 2, 4, 5].includes(nameId) || stringOffset + length > buffer.length) continue;
    const raw = buffer.subarray(stringOffset, stringOffset + length);
    let value = '';
    try {
      value = platform === 0 || platform === 3
        ? decodeUtf16BE(raw)
        : raw.toString('latin1').replace(/\0/g, '').trim();
    } catch {
      continue;
    }
    if (!value) continue;
    const score = (platform === 3 ? 4 : 0) + (language === 0x0409 ? 2 : 0)
      + ([1, 10].includes(encoding) ? 1 : 0);
    if (!names.has(nameId) || names.get(nameId).score < score) names.set(nameId, { value, score });
  }
  return Object.fromEntries([...names].map(([id, item]) => [id, item.value]));
}

function charsetForCodePages(codePageRange1) {
  if (codePageRange1 & (1 << 17)) return 128; // Shift-JIS
  if (codePageRange1 & (1 << 18)) return 134; // GB2312
  if (codePageRange1 & (1 << 19)) return 129; // Hangul
  if (codePageRange1 & (1 << 20)) return 136; // Big5
  return 1; // DEFAULT_CHARSET
}

/** Parse the metadata required by the EOT and PresentationML font records. */
export function parseSfntMetadata(fontData) {
  const buffer = Buffer.isBuffer(fontData) ? fontData : Buffer.from(fontData);
  const tables = readSfntTables(buffer);
  const names = readSfntNames(buffer, tables);
  const os2 = tables.get('OS/2');
  const head = tables.get('head');
  const metadata = {
    family: names[1] || names[4] || 'Embedded Font',
    style: names[2] || 'Regular',
    version: names[5] || 'Version 1.0',
    fullName: names[4] || names[1] || 'Embedded Font',
    panose: Buffer.alloc(10),
    charset: 1,
    italic: 0,
    weight: 400,
    fsType: 0,
    unicodeRanges: [0, 0, 0, 0],
    codePageRanges: [0, 0],
    checkSumAdjustment: 0,
  };
  if (os2 && os2.length >= 64) {
    const { offset, length } = os2;
    const version = readUInt16BE(buffer, offset);
    metadata.weight = readUInt16BE(buffer, offset + 4);
    metadata.fsType = readUInt16BE(buffer, offset + 8);
    metadata.panose = Buffer.from(buffer.subarray(offset + 32, offset + 42));
    metadata.unicodeRanges = [0, 1, 2, 3].map(index => readUInt32BE(buffer, offset + 42 + index * 4));
    metadata.italic = (readUInt16BE(buffer, offset + 62) & 1) ? 1 : 0;
    if (version >= 1 && length >= 86) {
      metadata.codePageRanges = [readUInt32BE(buffer, offset + 78), readUInt32BE(buffer, offset + 82)];
    }
    metadata.charset = charsetForCodePages(metadata.codePageRanges[0]);
  }
  if (head && head.length >= 12) metadata.checkSumAdjustment = readUInt32BE(buffer, head.offset + 8);
  return metadata;
}

function utf16Name(value) {
  const data = Buffer.from(`${value || ''}\0`, 'utf16le');
  if (data.length > 0xffff) throw new Error('font metadata name is too long for EOT');
  const size = Buffer.alloc(2);
  size.writeUInt16LE(data.length);
  return [size, data];
}

/** Convert a full TTF/OTF into an uncompressed EOT 1.0 font data part. */
export function createEot(fontData) {
  const buffer = Buffer.isBuffer(fontData) ? fontData : Buffer.from(fontData);
  const metadata = parseSfntMetadata(buffer);
  const permission = metadata.fsType & 0x000f;
  if (permission === 0x0002 || (metadata.fsType & 0x0200)) {
    throw new Error(`font ${metadata.family} does not permit outline document embedding`);
  }

  // Version 1.0 has an 82-byte fixed header, four UTF-16 strings, and the
  // original sfnt. Flags=0 means full, uncompressed, non-obfuscated font data.
  const header = Buffer.alloc(82);
  header.writeUInt32LE(buffer.length, 4);
  header.writeUInt32LE(0x00010000, 8);
  header.writeUInt32LE(0, 12);
  metadata.panose.copy(header, 16, 0, 10);
  header.writeUInt8(metadata.charset, 26);
  header.writeUInt8(metadata.italic, 27);
  header.writeUInt32LE(metadata.weight, 28);
  header.writeUInt16LE(metadata.fsType, 32);
  header.writeUInt16LE(0x504c, 34);
  metadata.unicodeRanges.forEach((value, index) => header.writeUInt32LE(value, 36 + index * 4));
  metadata.codePageRanges.forEach((value, index) => header.writeUInt32LE(value, 52 + index * 4));
  header.writeUInt32LE(metadata.checkSumAdjustment, 60);

  const chunks = [header];
  for (const [index, value] of [metadata.family, metadata.style, metadata.version, metadata.fullName].entries()) {
    chunks.push(...utf16Name(value));
    if (index < 3) chunks.push(Buffer.alloc(2));
  }
  chunks.push(buffer);
  const eot = Buffer.concat(chunks);
  eot.writeUInt32LE(eot.length, 0);
  return { data: eot, metadata };
}

function parseWeight(value) {
  const number = parseInt(String(value || '').match(/\d+/)?.[0] || '400', 10);
  return Number.isFinite(number) ? number : 400;
}

/** Parse @font-face blocks returned by Google Fonts CSS. */
export function parseGoogleFontsCss(css, stylesheetUrl = 'https://fonts.googleapis.com/') {
  const faces = [];
  const blockPattern = /@font-face\s*\{([\s\S]*?)\}/gi;
  for (const match of String(css || '').matchAll(blockPattern)) {
    const block = match[1];
    const family = block.match(/font-family\s*:\s*([^;]+)\s*;/i)?.[1]?.trim().replace(/^['"]|['"]$/g, '');
    const style = block.match(/font-style\s*:\s*([^;]+)\s*;/i)?.[1]?.trim().toLowerCase() || 'normal';
    const weight = parseWeight(block.match(/font-weight\s*:\s*([^;]+)\s*;/i)?.[1]);
    const sourceMatch = [...block.matchAll(/url\(\s*(['"]?)([^)'"\s]+)\1\s*\)\s*(?:format\(\s*(['"]?)([^)'"\s]+)\3\s*\))?/gi)]
      .find(item => !item[4] || /^(?:truetype|opentype)$/i.test(item[4]) || /\.(?:ttf|otf)(?:$|\?)/i.test(item[2]));
    if (!family || !sourceMatch) continue;
    try {
      const url = new URL(sourceMatch[2], stylesheetUrl);
      if (url.protocol !== 'https:' || url.hostname !== GOOGLE_FONT_HOST) continue;
      faces.push({ family, style, weight, url: url.href });
    } catch { /* Ignore malformed font URLs. */ }
  }
  return faces;
}

export function googleStylesheetUrls(html) {
  const urls = new Set();
  const source = decodeHtmlEntities(html);
  const patterns = [
    // Semicolons are valid inside Google Fonts axis lists, e.g.
    // `wght@400;700;900`; stop at the closing quote/parenthesis instead.
    /@import\s+(?:url\(\s*)?(['"]?)(https:\/\/fonts\.googleapis\.com\/[^)'"\s]+)\1\s*\)?\s*;?/gi,
    /<link\b[^>]*\bhref\s*=\s*(['"])(https:\/\/fonts\.googleapis\.com\/[^'"]+)\1[^>]*>/gi,
  ];
  for (const pattern of patterns) {
    for (const match of source.matchAll(pattern)) {
      try {
        const url = new URL(match[2]);
        if (url.protocol === 'https:' && url.hostname === GOOGLE_CSS_HOST) urls.add(url.href);
      } catch { /* Ignore malformed stylesheet URLs. */ }
    }
  }
  return urls;
}

function collectUsedFontWeights(pages) {
  const used = new Map();
  const record = (fontFamily, weight = 400, style = 'normal') => {
    const family = parseFontFamily(fontFamily, { genericFallback: false });
    const key = normalizeFamily(family);
    if (!key) return;
    if (!used.has(key)) used.set(key, { family, weights: [], italic: false });
    used.get(key).weights.push(parseWeight(weight));
    if (String(style).toLowerCase() === 'italic') used.get(key).italic = true;
  };
  const visit = node => {
    if (!node || typeof node !== 'object') return;
    record(node.styles?.fontFamily, node.styles?.fontWeight, node.styles?.fontStyle);
    for (const run of node.textRuns || []) record(run?.fontFamily, run?.bold ? 700 : 400, run?.italic ? 'italic' : 'normal');
    for (const item of node.listData || []) record(item?.styles?.fontFamily, item?.styles?.fontWeight, item?.styles?.fontStyle);
    for (const child of node.children || []) visit(child);
  };
  for (const page of pages || []) {
    const ir = page?.ir || {};
    for (const key of ['bg', 'header', 'ct', 'footer']) visit(ir[key]);
    for (const node of [...(ir.overlays || []), ...(ir.rest || [])]) visit(node);
  }
  return used;
}

function selectUsedFaces(faces, usedFonts) {
  const selected = [];
  const byFamily = new Map();
  for (const face of faces) {
    const key = normalizeFamily(face.family);
    if (!usedFonts.has(key)) continue;
    if (!byFamily.has(key)) byFamily.set(key, []);
    byFamily.get(key).push(face);
  }
  const closest = (items, target) => [...items].sort((a, b) => Math.abs(a.weight - target) - Math.abs(b.weight - target))[0];
  for (const [key, items] of byFamily) {
    const usage = usedFonts.get(key);
    const normal = items.filter(item => item.style !== 'italic');
    const italic = items.filter(item => item.style === 'italic');
    if (normal.length) {
      const regular = closest(normal, 400);
      selected.push({ ...regular, variant: 'regular' });
      if (usage.weights.some(weight => weight >= 600)) {
        const bold = closest(normal, 700);
        if (bold.url !== regular.url) selected.push({ ...bold, variant: 'bold' });
      }
    }
    if (usage.italic && italic.length) {
      const regularItalic = closest(italic, 400);
      selected.push({ ...regularItalic, variant: 'italic' });
      if (usage.weights.some(weight => weight >= 600)) {
        const boldItalic = closest(italic, 700);
        if (boldItalic.url !== regularItalic.url) selected.push({ ...boldItalic, variant: 'boldItalic' });
      }
    }
  }
  return selected.slice(0, MAX_EMBEDDED_FONTS);
}

async function fetchLimited(url, maxBytes, fetchImpl) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS);
  try {
    const response = await fetchImpl(url, {
      signal: controller.signal,
      headers: { 'User-Agent': 'Mozilla/5.0' },
      redirect: 'follow',
    });
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const contentLength = Number(response.headers.get('content-length') || 0);
    if (contentLength > maxBytes) throw new Error(`resource exceeds ${maxBytes} bytes`);
    const data = Buffer.from(await response.arrayBuffer());
    if (data.length > maxBytes) throw new Error(`resource exceeds ${maxBytes} bytes`);
    return data;
  } finally {
    clearTimeout(timer);
  }
}

/** Download the embeddable Google font faces actually used by the extracted DOM. */
export async function collectGoogleFontFaces(pages, { fetchImpl = globalThis.fetch } = {}) {
  if (typeof fetchImpl !== 'function') return [];
  const stylesheetUrls = new Set();
  for (const page of pages || []) {
    if (!page?.path) continue;
    try {
      for (const url of googleStylesheetUrls(await readFile(page.path, 'utf8'))) stylesheetUrls.add(url);
    } catch { /* A missing source page must not fail the PPTX export. */ }
  }
  if (!stylesheetUrls.size) return [];

  const advertisedFaces = [];
  for (const url of stylesheetUrls) {
    try {
      const css = (await fetchLimited(url, MAX_CSS_BYTES, fetchImpl)).toString('utf8');
      advertisedFaces.push(...parseGoogleFontsCss(css, url));
    } catch (error) {
      process.stderr.write(`[fonts] Google Fonts stylesheet unavailable (${url}): ${error.message}\n`);
    }
  }
  const selected = selectUsedFaces(advertisedFaces, collectUsedFontWeights(pages));
  const downloaded = [];
  let totalBytes = 0;
  for (const face of selected) {
    try {
      const data = await fetchLimited(face.url, MAX_FONT_BYTES, fetchImpl);
      if (totalBytes + data.length > MAX_TOTAL_FONT_BYTES) {
        process.stderr.write('[fonts] Embedded font byte limit reached; remaining web fonts will use Office fallback.\n');
        break;
      }
      const metadata = parseSfntMetadata(data);
      const permission = metadata.fsType & 0x000f;
      if (permission === 0x0002 || (metadata.fsType & 0x0200)) {
        process.stderr.write(`[fonts] ${face.family} disallows outline document embedding; skipped.\n`);
        continue;
      }
      totalBytes += data.length;
      downloaded.push({ ...face, data, metadata });
    } catch (error) {
      process.stderr.write(`[fonts] Font unavailable (${face.family} ${face.weight}): ${error.message}\n`);
    }
  }
  return downloaded;
}

function nextRelationshipId(xml) {
  let maximum = 0;
  for (const match of xml.matchAll(/\bId="rId(\d+)"/g)) maximum = Math.max(maximum, Number(match[1]));
  return maximum + 1;
}

function nextFontPartNumber(zip) {
  let maximum = 0;
  for (const name of Object.keys(zip.files)) {
    const match = name.match(/^ppt\/fonts\/font(\d+)\.fntdata$/);
    if (match) maximum = Math.max(maximum, Number(match[1]));
  }
  return maximum + 1;
}

/** Inject already-downloaded TTF/OTF faces into a PPTX package. */
export async function embedFontFacesInPptx(pptxPath, faces) {
  if (!faces?.length) return [];
  const zip = await JSZip.loadAsync(await readFile(pptxPath));
  const presentationFile = zip.file('ppt/presentation.xml');
  const relationshipsFile = zip.file('ppt/_rels/presentation.xml.rels');
  const contentTypesFile = zip.file('[Content_Types].xml');
  if (!presentationFile || !relationshipsFile || !contentTypesFile) {
    throw new Error('PPTX is missing presentation package parts');
  }
  let presentation = await presentationFile.async('string');
  let relationships = await relationshipsFile.async('string');
  let contentTypes = await contentTypesFile.async('string');
  const existingFamilies = new Set(
    [...presentation.matchAll(/<p:font\b[^>]*\btypeface="([^"]+)"/g)].map(match => normalizeFamily(match[1])),
  );
  let relationshipId = nextRelationshipId(relationships);
  let fontPartNumber = nextFontPartNumber(zip);
  const relationshipEntries = [];
  const familyEntries = new Map();
  const embedded = [];

  for (const face of faces) {
    const key = normalizeFamily(face.family);
    if (!key || existingFamilies.has(key)) continue;
    try {
      const { data, metadata } = createEot(face.data);
      const relId = `rId${relationshipId++}`;
      const partName = `ppt/fonts/font${fontPartNumber++}.fntdata`;
      zip.file(partName, data);
      relationshipEntries.push(
        `<Relationship Id="${relId}" Type="${FONT_RELATIONSHIP}" Target="fonts/${partName.split('/').at(-1)}"/>`,
      );
      if (!familyEntries.has(key)) {
        const charset = metadata.charset > 127 ? metadata.charset - 256 : metadata.charset;
        familyEntries.set(key, {
          family: face.family,
          panose: metadata.panose.toString('hex').toUpperCase().padEnd(20, '0').slice(0, 20),
          charset,
          variants: new Map(),
        });
      }
      familyEntries.get(key).variants.set(face.variant || 'regular', relId);
      embedded.push(`${face.family}:${face.variant || 'regular'}`);
    } catch (error) {
      process.stderr.write(`[fonts] Failed to embed ${face.family}: ${error.message}\n`);
    }
  }
  if (!familyEntries.size) return [];

  const embeddedFontXml = [...familyEntries.values()].map(item => {
    const variants = ['regular', 'bold', 'italic', 'boldItalic']
      .filter(variant => item.variants.has(variant))
      .map(variant => `<p:${variant} r:id="${item.variants.get(variant)}"/>`)
      .join('');
    return `<p:embeddedFont><p:font typeface="${xmlEscape(item.family)}" panose="${item.panose}" pitchFamily="34" charset="${item.charset}"/>${variants}</p:embeddedFont>`;
  }).join('');
  if (/<p:embeddedFontLst\b/.test(presentation)) {
    presentation = presentation.replace('</p:embeddedFontLst>', `${embeddedFontXml}</p:embeddedFontLst>`);
  } else if (/<p:defaultTextStyle\b/.test(presentation)) {
    presentation = presentation.replace('<p:defaultTextStyle', `<p:embeddedFontLst>${embeddedFontXml}</p:embeddedFontLst><p:defaultTextStyle`);
  } else {
    presentation = presentation.replace('</p:presentation>', `<p:embeddedFontLst>${embeddedFontXml}</p:embeddedFontLst></p:presentation>`);
  }
  presentation = presentation.replace(/<p:presentation\b([^>]*)>/, (match, attributes) => {
    if (/\bembedTrueTypeFonts=/.test(attributes)) {
      return `<p:presentation${attributes.replace(/\bembedTrueTypeFonts="[^"]*"/, 'embedTrueTypeFonts="1"')}>`;
    }
    return `<p:presentation embedTrueTypeFonts="1"${attributes}>`;
  });
  relationships = relationships.replace('</Relationships>', `${relationshipEntries.join('')}</Relationships>`);
  if (!/Extension="fntdata"/i.test(contentTypes)) {
    contentTypes = contentTypes.replace('</Types>', '<Default Extension="fntdata" ContentType="application/x-fontdata"/></Types>');
  }
  zip.file('ppt/presentation.xml', presentation);
  zip.file('ppt/_rels/presentation.xml.rels', relationships);
  zip.file('[Content_Types].xml', contentTypes);
  await writeFile(pptxPath, await zip.generateAsync({
    type: 'nodebuffer',
    compression: 'DEFLATE',
    mimeType: 'application/vnd.openxmlformats-officedocument.presentationml.presentation',
  }));
  return embedded;
}

/** Best-effort end-to-end web-font embedding for an exported deck. */
export async function embedGoogleFontsFromPages(pptxPath, pages, options = {}) {
  const faces = await collectGoogleFontFaces(pages, options);
  if (!faces.length) return [];
  const embedded = await embedFontFacesInPptx(pptxPath, faces);
  if (embedded.length) process.stderr.write(`[fonts] Embedded ${embedded.join(', ')}\n`);
  return embedded;
}
