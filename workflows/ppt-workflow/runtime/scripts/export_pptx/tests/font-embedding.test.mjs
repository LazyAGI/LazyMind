import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import JSZip from 'jszip';

import {
  createEot,
  embedFontFacesInPptx,
  googleStylesheetUrls,
  parseGoogleFontsCss,
  parseSfntMetadata,
} from '../lib/font_embedding.mjs';

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
