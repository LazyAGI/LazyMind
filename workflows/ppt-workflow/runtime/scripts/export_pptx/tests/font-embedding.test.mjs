import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { createHash } from 'node:crypto';
import { localFontFaces, localFontCss } from '../lib/local_fonts.mjs';

import JSZip from 'jszip';
import { buildPptx } from '../lib/pptx_builder.mjs';

import {
  createEot,
  embedFontFacesInPptx,
  googleStylesheetUrls,
  parseGoogleFontsCss,
  parseSfntMetadata,
  portableFontFaces,
  embedGoogleFontsFromPages,
  verifyEmbeddedFaces,
} from '../lib/font_embedding.mjs';

function portableHtml(data = makeTestTtf(), sha256 = createHash('sha256').update(data).digest('hex')) {
  const token = Buffer.from(JSON.stringify({ family: 'Test Web Font', weight: 400, style: 'normal', sha256 })).toString('base64');
  return `<html><head><style data-lazymind-font-required="true">/*lazymind-font:${token}*/@font-face{font-family:"Test Web Font";font-weight:400;font-style:normal;src:url(data:font/ttf;base64,${data.toString('base64')})}</style></head><body>Keep content</body></html>`;
}

test('portable font package rejects corruption and preview-only embedding', () => {
  assert.equal(portableFontFaces(portableHtml()).length, 1);
  assert.throws(() => portableFontFaces(portableHtml(makeTestTtf(), 'wrong')), /corrupted/);
  const data = makeTestTtf();
  data.writeUInt16BE(4, data.readUInt32BE(20) + 8);
  assert.throws(() => portableFontFaces(portableHtml(data)), /editable embedding/);
  assert.throws(() => portableFontFaces('<style data-lazymind-font-required="true"></style>'), /empty or invalid/);
  assert.throws(() => portableFontFaces(portableHtml().replace('</style>',
    '/*lazymind-font:broken*/@font-face{src:url(missing.ttf)}</style>')), /empty or invalid/);
});

test('subset HTML embeds verified original bytes and fails when the original is missing', async () => {
  const folder = await mkdtemp(path.join(os.tmpdir(), 'subset-export-'));
  const previous = process.env.LAZYMIND_FONT_DIR;
  process.env.LAZYMIND_FONT_DIR = folder;
  try {
    const data = makeTestTtf();
    const id = createHash('sha256').update(data).digest('hex');
    const record = { id, file: `${id}.ttf`, aliases: ['Test Web Font'], family: 'Test Web Font',
      weight: 400, style: 'normal', embeddable: true };
    await writeFile(path.join(folder, record.file), data);
    await writeFile(path.join(folder, `${id}.json`), JSON.stringify(record));
    // Browser subset is deliberately different from the complete editable font.
    const html = portableHtml().replace('data:font/ttf', 'data:font/woff')
      .replace(data.toString('base64'), Buffer.from('browser subset').toString('base64'));
    assert.throws(() => portableFontFaces(html), /Original font resource unavailable/);
    const faces = portableFontFaces(html, await localFontFaces());
    assert.ok(faces[0].data.equals(data));
    const source = path.join(folder, 'page.html');
    const output = path.join(folder, 'deck.pptx');
    await writeFile(source, html);
    const zip = new JSZip();
    zip.file('[Content_Types].xml', '<Types></Types>');
    zip.file('ppt/_rels/presentation.xml.rels', '<Relationships></Relationships>');
    zip.file('ppt/presentation.xml', '<p:presentation><p:defaultTextStyle/></p:presentation>');
    await writeFile(output, await zip.generateAsync({ type: 'nodebuffer' }));
    await embedGoogleFontsFromPages(output, [{ path: source }]);
    await verifyEmbeddedFaces(output, faces);
    await rm(path.join(folder, record.file));
    await assert.rejects(embedGoogleFontsFromPages(output, [{ path: source }]),
      error => error.fontPortability && /Original font resource unavailable/.test(error.message));
  } finally {
    if (previous === undefined) delete process.env.LAZYMIND_FONT_DIR;
    else process.env.LAZYMIND_FONT_DIR = previous;
    await rm(folder, { recursive: true, force: true });
  }
});

test('HTML exports its own fonts offline and verifies actual PPTX parts', async () => {
  const folder = await mkdtemp(path.join(os.tmpdir(), 'portable-export-'));
  try {
    const source = path.join(folder, 'page.html');
    const output = path.join(folder, 'deck.pptx');
    await writeFile(source, portableHtml());
    const zip = new JSZip();
    zip.file('[Content_Types].xml', '<Types></Types>');
    zip.file('ppt/_rels/presentation.xml.rels', '<Relationships></Relationships>');
    zip.file('ppt/presentation.xml', '<p:presentation><p:defaultTextStyle/></p:presentation>');
    zip.file('ppt/slides/slide1.xml', '<slide>Original content</slide>');
    await writeFile(output, await zip.generateAsync({ type: 'nodebuffer' }));
    const fonts = await embedGoogleFontsFromPages(output, [{ path: source }], {
      fetchImpl: () => { throw new Error('offline export must not access network'); },
    });
    assert.deepEqual(fonts, ['Test Web Font:regular']);
    const faces = portableFontFaces(await readFile(source, 'utf8'));
    await verifyEmbeddedFaces(output, faces);
    const result = await JSZip.loadAsync(await readFile(output));
    assert.equal(await result.file('ppt/slides/slide1.xml').async('string'), '<slide>Original content</slide>');
    result.remove('ppt/fonts/font1.fntdata');
    await writeFile(output, await result.generateAsync({ type: 'nodebuffer' }));
    await assert.rejects(verifyEmbeddedFaces(output, faces), /not embedded/);
    await writeFile(source, portableHtml(makeTestTtf(), 'wrong'));
    await assert.rejects(embedGoogleFontsFromPages(output, [{ path: source }]), error => error.fontPortability === true);
  } finally {
    await rm(folder, { recursive: true, force: true });
  }
});

test('required font failure preserves the previous exported file', async () => {
  const folder = await mkdtemp(path.join(os.tmpdir(), 'font-export-atomic-'));
  try {
    const source = path.join(folder, 'page.html');
    const output = path.join(folder, 'deck.pptx');
    await writeFile(source, portableHtml(makeTestTtf(), 'corrupt'));
    await writeFile(output, 'previous export');
    await assert.rejects(buildPptx([{ path: source, ir: { ct: {
      tag: 'P', text: 'Keep content', children: [],
      bounds: { x: 0, y: 0, w: 300, h: 80 }, styles: { fontFamily: 'Test Web Font' },
    } } }], folder, output), /corrupted/);
    assert.equal(await readFile(output, 'utf8'), 'previous export');
  } finally {
    await rm(folder, { recursive: true, force: true });
  }
});

test('editable export without font embedding keeps the font name and omits font parts', async () => {
  const folder = await mkdtemp(path.join(os.tmpdir(), 'pptx-no-embed-'));
  try {
    const source = path.join(folder, 'page.html');
    const output = path.join(folder, 'deck.pptx');
    // A required font fails embedded export when its original bytes do not match.
    await writeFile(source, portableHtml(makeTestTtf(), 'corrupt'));
    const result = await buildPptx([{ path: source, ir: { ct: {
      tag: 'P', text: 'Keep content', children: [],
      bounds: { x: 0, y: 0, w: 300, h: 80 }, styles: { fontFamily: 'Test Web Font' },
    } } }], folder, output, { embedFonts: false });
    assert.equal(result.successCount, 1);
    assert.deepEqual(result.embeddedFonts, []);
    const zip = await JSZip.loadAsync(await readFile(output));
    assert.match(await zip.file('ppt/slides/slide1.xml').async('string'), /typeface="Test Web Font"/);
    assert.doesNotMatch(await zip.file('ppt/presentation.xml').async('string'), /embeddedFontLst/);
    assert.equal(Object.keys(zip.files).some(name => name.startsWith('ppt/fonts/')), false);
  } finally {
    await rm(folder, { recursive: true, force: true });
  }
});

function writeUInt16BE(buffer, value, offset) {
  buffer.writeUInt16BE(value, offset);
}

function writeUInt32BE(buffer, value, offset) {
  buffer.writeUInt32BE(value >>> 0, offset);
}

function makeTestTtf() {
  const family = Buffer.from('Test Web Font', 'utf16le');
  for (let index = 0; index < family.length; index += 2) {
    [family[index], family[index + 1]] = [family[index + 1], family[index]];
  }
  const style = Buffer.from('Regular', 'utf16le');
  for (let index = 0; index < style.length; index += 2) {
    [style[index], style[index + 1]] = [style[index + 1], style[index]];
  }
  const nameStrings = Buffer.concat([family, style]);
  const name = Buffer.alloc(6 + 2 * 12 + nameStrings.length);
  writeUInt16BE(name, 0, 0);
  writeUInt16BE(name, 2, 2);
  writeUInt16BE(name, 6 + 2 * 12, 4);
  const records = [
    { id: 1, length: family.length, offset: 0 },
    { id: 2, length: style.length, offset: family.length },
  ];
  records.forEach((record, index) => {
    const offset = 6 + index * 12;
    [3, 1, 0x0409, record.id, record.length, record.offset]
      .forEach((value, field) => writeUInt16BE(name, value, offset + field * 2));
  });
  nameStrings.copy(name, 6 + 2 * 12);

  const os2 = Buffer.alloc(86);
  writeUInt16BE(os2, 1, 0);
  writeUInt16BE(os2, 400, 4);
  writeUInt16BE(os2, 0, 8);
  Buffer.from('020B0200000000000000', 'hex').copy(os2, 32);
  writeUInt32BE(os2, 1 << 18, 78);
  const head = Buffer.alloc(54);
  writeUInt32BE(head, 0x12345678, 8);

  const tables = [
    ['OS/2', os2],
    ['head', head],
    ['name', name],
  ];
  const directorySize = 12 + tables.length * 16;
  const totalSize = directorySize + tables.reduce((sum, [, data]) => sum + data.length, 0);
  const ttf = Buffer.alloc(totalSize);
  ttf.writeUInt32BE(0x00010000, 0);
  writeUInt16BE(ttf, tables.length, 4);
  let dataOffset = directorySize;
  tables.forEach(([tag, data], index) => {
    const record = 12 + index * 16;
    ttf.write(tag, record, 4, 'latin1');
    writeUInt32BE(ttf, dataOffset, record + 8);
    writeUInt32BE(ttf, data.length, record + 12);
    data.copy(ttf, dataOffset);
    dataOffset += data.length;
  });
  return ttf;
}

test('local font catalog verifies content hashes and shares fonts with browser CSS', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'ppt-local-fonts-'));
  const previous = process.env.LAZYMIND_FONT_DIR;
  process.env.LAZYMIND_FONT_DIR = root;
  try {
    const data = makeTestTtf();
    const id = createHash('sha256').update(data).digest('hex');
    await writeFile(path.join(root, `${id}.ttf`), data);
    await writeFile(path.join(root, `${id}.json`), JSON.stringify({
      id, file: `${id}.ttf`, family: 'Test Web Font', aliases: ['Test Web Font'],
      weight: 400, style: 'normal', embeddable: true,
    }));
    assert.equal((await localFontFaces()).length, 1);
    assert.match(await localFontCss('font-family: Test Web Font'), /@font-face.*data:font\/ttf;base64,/);
    const fileCss = await localFontCss('font-family: Test Web Font', { useFileUrls: true });
    assert.match(fileCss, /src:url\("file:\/\/\/[^\"]+\.ttf"\)/);
    assert.ok(fileCss.includes(`${id}.ttf`));
    assert.doesNotMatch(fileCss, /base64/);
    assert.equal(await localFontCss('font-family: Arial'), '');
    await writeFile(path.join(root, `${id}.ttf`), 'corrupted');
    assert.deepEqual(await localFontFaces(), []);
    assert.equal(await localFontCss('font-family: Test Web Font', { useFileUrls: true }), '');
  } finally {
    if (previous === undefined) delete process.env.LAZYMIND_FONT_DIR;
    else process.env.LAZYMIND_FONT_DIR = previous;
    await rm(root, { recursive: true, force: true });
  }
});

test('local font CSS avoids duplicate versions and fallback aliases but keeps separately used aliases', async () => {
  const root = await mkdtemp(path.join(os.tmpdir(), 'ppt-font-aliases-'));
  const previous = process.env.LAZYMIND_FONT_DIR;
  process.env.LAZYMIND_FONT_DIR = root;
  try {
    for (const data of [makeTestTtf(), Buffer.concat([makeTestTtf(), Buffer.from('second version')])]) {
      const id = createHash('sha256').update(data).digest('hex');
      await writeFile(path.join(root, `${id}.ttf`), data);
      await writeFile(path.join(root, `${id}.json`), JSON.stringify({
        id, file: `${id}.ttf`, aliases: ['Test Serif', '测试宋体'],
        weight: 400, style: 'normal', embeddable: true,
      }));
    }
    const css = await localFontCss('body{font-family:"Test Serif","测试宋体",serif}');
    assert.equal((css.match(/@font-face/g) || []).length, 1);
    assert.match(css, /font-family:"Test Serif"/);
    const separate = await localFontCss('body{font-family:"Test Serif"}h1{font-family:"测试宋体"}');
    assert.equal((separate.match(/@font-face/g) || []).length, 2);
  } finally {
    if (previous === undefined) delete process.env.LAZYMIND_FONT_DIR;
    else process.env.LAZYMIND_FONT_DIR = previous;
    await rm(root, { recursive: true, force: true });
  }
});

test('Google Fonts CSS parser selects direct TrueType font sources', () => {
  const faces = parseGoogleFontsCss(`
    @font-face {
      font-family: 'ZCOOL KuaiLe';
      font-style: normal;
      font-weight: 400;
      src: url(https://fonts.gstatic.com/s/zcool/test.ttf) format('truetype');
    }
    @font-face {
      font-family: 'Ignored';
      src: url(https://example.com/font.ttf) format('truetype');
    }
  `);
  assert.deepEqual(faces, [{
    family: 'ZCOOL KuaiLe',
    style: 'normal',
    weight: 400,
    url: 'https://fonts.gstatic.com/s/zcool/test.ttf',
  }]);
});

test('Google Fonts imports may contain semicolon-separated weight axes', () => {
  const importStatement = '@import url("https://fonts.googleapis.com/css2?family=Noto+Sans+SC:wght@400;700;900&display=swap");';
  assert.deepEqual([...googleStylesheetUrls(importStatement)], [
    'https://fonts.googleapis.com/css2?family=Noto+Sans+SC:wght@400;700;900&display=swap',
  ]);
});

test('EOT builder preserves sfnt metadata and full font bytes', () => {
  const ttf = makeTestTtf();
  const metadata = parseSfntMetadata(ttf);
  assert.equal(metadata.family, 'Test Web Font');
  assert.equal(metadata.charset, 134);
  const { data } = createEot(ttf);
  assert.equal(data.readUInt32LE(0), data.length);
  assert.equal(data.readUInt32LE(4), ttf.length);
  assert.equal(data.readUInt32LE(8), 0x00010000);
  assert.equal(data.readUInt16LE(34), 0x504c);
  assert.deepEqual(data.subarray(data.length - ttf.length), ttf);
});

test('font embedding adds fntdata, relationships, and embeddedFontLst', async () => {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'lazymind-font-embed-'));
  try {
    const pptxPath = path.join(dir, 'test.pptx');
    const zip = new JSZip();
    zip.file('[Content_Types].xml', '<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>');
    zip.file('ppt/_rels/presentation.xml.rels', '<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId7" Type="theme" Target="theme/theme1.xml"/></Relationships>');
    zip.file('ppt/presentation.xml', '<?xml version="1.0"?><p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><p:defaultTextStyle/></p:presentation>');
    await writeFile(pptxPath, await zip.generateAsync({ type: 'nodebuffer' }));

    const embedded = await embedFontFacesInPptx(pptxPath, [{
      family: 'Test Web Font',
      variant: 'regular',
      data: makeTestTtf(),
    }]);
    assert.deepEqual(embedded, ['Test Web Font:regular']);
    const result = await JSZip.loadAsync(await readFile(pptxPath));
    const presentation = await result.file('ppt/presentation.xml').async('string');
    const relationships = await result.file('ppt/_rels/presentation.xml.rels').async('string');
    const contentTypes = await result.file('[Content_Types].xml').async('string');
    const fontData = await result.file('ppt/fonts/font1.fntdata').async('nodebuffer');
    assert.match(presentation, /embedTrueTypeFonts="1"/);
    assert.match(presentation, /typeface="Test Web Font"/);
    assert.match(presentation, /<p:regular r:id="rId8"\/>/);
    assert.match(relationships, /Id="rId8"[^>]+relationships\/font/);
    assert.match(contentTypes, /Extension="fntdata" ContentType="application\/x-fontdata"/);
    assert.equal(fontData.readUInt32LE(8), 0x00010000);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test('export embeds only glyphs used across slides and speaker notes', async () => {
  const { execFileSync } = await import('node:child_process');
  const python = process.env.LAZYMIND_FONT_SUBSET_PYTHON || 'python3';
  const folder = await mkdtemp(path.join(os.tmpdir(), 'glyph-export-'));
  try {
    const fontPath = path.join(folder, 'test.ttf');
    execFileSync(python, ['-c', `
import sys
from fontTools.fontBuilder import FontBuilder
from fontTools.pens.ttGlyphPen import TTGlyphPen
b = FontBuilder(1000, isTTF=True)
chars = '甲乙丙丁'
glyphs = ['.notdef'] + ['g' + str(i) for i in range(4)]
b.setupGlyphOrder(glyphs)
b.setupCharacterMap(dict(zip(map(ord, chars), glyphs[1:])))
b.setupGlyf({g: TTGlyphPen(None).glyph() for g in glyphs})
b.setupHorizontalMetrics({g: (500, 0) for g in glyphs})
b.setupHorizontalHeader(ascent=800, descent=-200)
b.setupNameTable({'familyName':'Test Web Font', 'styleName':'Regular', 'fullName':'Test Web Font'})
b.setupOS2(fsType=0)
b.setupPost()
b.save(sys.argv[1])
`, fontPath]);
    const source = path.join(folder, 'page.html');
    const output = path.join(folder, 'deck.pptx');
    await writeFile(source, portableHtml(await readFile(fontPath)));
    const zip = new JSZip();
    zip.file('[Content_Types].xml', '<Types></Types>');
    zip.file('ppt/_rels/presentation.xml.rels', '<Relationships></Relationships>');
    zip.file('ppt/presentation.xml', '<p:presentation xmlns:p="urn:p"><p:defaultTextStyle/></p:presentation>');
    zip.file('ppt/slides/slide1.xml', '<slide><t>甲</t></slide>');
    zip.file('ppt/slides/slide2.xml', '<slide><t>乙</t></slide>');
    zip.file('ppt/notesSlides/notesSlide1.xml', '<notes><t>丙</t></notes>');
    await writeFile(output, await zip.generateAsync({ type: 'nodebuffer' }));
    await embedGoogleFontsFromPages(output, [{ path: source }]);
    const result = await JSZip.loadAsync(await readFile(output));
    const parts = Object.keys(result.files).filter(name => name.endsWith('.fntdata'));
    assert.equal(parts.length, 1);
    const eot = await result.file(parts[0]).async('nodebuffer');
    await writeFile(fontPath, eot.subarray(eot.length - eot.readUInt32LE(4)));
    const chars = execFileSync(python, ['-c',
      'from fontTools.ttLib import TTFont; import sys; print("".join(chr(c) for c in sorted(TTFont(sys.argv[1]).getBestCmap())))',
      fontPath], { encoding: 'utf8' }).trim();
    assert.deepEqual(new Set(chars), new Set('甲乙丙'));
    assert.match(await result.file('ppt/presentation.xml').async('string'), /saveSubsetFonts="1"/);
    assert.equal(await result.file('ppt/slides/slide2.xml').async('string'), '<slide><t>乙</t></slide>');
  } finally {
    await rm(folder, { recursive: true, force: true });
  }
});
