import assert from 'node:assert/strict';
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { buildBackground, buildImageElement, buildPptx } from '../lib/pptx_builder.mjs';

const ONE_PIXEL_PNG =
  'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=';

function inlineImageNode() {
  return {
    tag: 'IMG',
    src: ONE_PIXEL_PNG,
    naturalWidth: 1,
    naturalHeight: 1,
    bounds: { x: 120, y: 90, w: 640, h: 360 },
    styles: { objectFit: 'cover', opacity: '1' },
    children: [],
  };
}

test('buildImageElement keeps publisher-inlined data images', () => {
  const image = buildImageElement(inlineImageNode(), '/deck');
  assert.equal(image.data, ONE_PIXEL_PNG);
  assert.equal(image.path, undefined);
  assert.equal(image.sizing?.type, 'cover');
});

test('buildBackground keeps publisher-inlined CSS background images', () => {
  const result = buildBackground({
    bounds: { x: 0, y: 0, w: 1600, h: 900 },
    styles: {
      backgroundColor: 'rgba(0, 0, 0, 0)',
      backgroundImage: `url("${ONE_PIXEL_PNG}")`,
      opacity: '1',
    },
    children: [],
  }, 'rgb(255, 255, 255)', '/deck');
  assert.equal(result.slideBackground.data, ONE_PIXEL_PNG);
  assert.equal(result.slideBackground.path, undefined);
});

test('editable PPTX embeds a publisher-inlined CSS background', async () => {
  const deckDir = await mkdtemp(path.join(os.tmpdir(), 'lazymind-inline-background-'));
  try {
    const outputPath = path.join(deckDir, 'inline-background.pptx');
    const ir = {
      canvasWidth: 1600,
      canvasHeight: 900,
      bodyBgColor: 'rgb(255, 255, 255)',
      bodyBgImage: 'none',
      wrapperBgColor: 'rgba(0, 0, 0, 0)',
      wrapperBgImage: 'none',
      bg: {
        tag: 'DIV',
        id: 'bg',
        bounds: { x: 0, y: 0, w: 1600, h: 900 },
        styles: {
          backgroundColor: 'rgba(0, 0, 0, 0)',
          backgroundImage: `url("${ONE_PIXEL_PNG}")`,
          opacity: '1',
        },
        children: [],
      },
      header: null,
      footer: null,
      overlays: [],
      rest: [],
      ct: null,
    };

    const result = await buildPptx(
      [{ path: path.join(deckDir, 'page_001.html'), ir }],
      deckDir,
      outputPath,
    );

    assert.equal(result.successCount, 1);
    assert.equal(result.failCount, 0);
    const pptx = await readFile(outputPath);
    assert.notEqual(pptx.indexOf(Buffer.from(ONE_PIXEL_PNG.split(',')[1], 'base64')), -1);
  } finally {
    await rm(deckDir, { recursive: true, force: true });
  }
});

test('editable PPTX embeds publisher-inlined images instead of a transparent placeholder', async () => {
  const deckDir = await mkdtemp(path.join(os.tmpdir(), 'lazymind-inline-image-'));
  try {
    const outputPath = path.join(deckDir, 'inline-image.pptx');
    const ir = {
      canvasWidth: 1600,
      canvasHeight: 900,
      bodyBgColor: 'rgb(15, 15, 26)',
      bodyBgImage: 'none',
      wrapperBgColor: 'rgb(15, 15, 26)',
      wrapperBgImage: 'none',
      bg: null,
      header: null,
      footer: null,
      overlays: [],
      rest: [],
      ct: {
        tag: 'DIV',
        bounds: { x: 0, y: 0, w: 1600, h: 900 },
        styles: {},
        children: [inlineImageNode()],
      },
    };

    const result = await buildPptx(
      [{ path: path.join(deckDir, 'page_001.html'), ir }],
      deckDir,
      outputPath,
    );

    assert.equal(result.successCount, 1);
    assert.equal(result.failCount, 0);
    const pptx = await readFile(outputPath);
    assert.ok(pptx.length > 0);
    // The inlined PNG bytes must be packaged in the OOXML zip. This also
    // proves the exporter did not replace it with TRANSPARENT_PIXEL_PNG.
    assert.notEqual(pptx.indexOf(Buffer.from(ONE_PIXEL_PNG.split(',')[1], 'base64')), -1);
  } finally {
    await rm(deckDir, { recursive: true, force: true });
  }
});


test('registered PDF figure survives HTML export with contain sizing', async () => {
  const deckDir = await mkdtemp(path.join(os.tmpdir(), 'lazymind-pdf-figure-'));
  try {
    await mkdir(path.join(deckDir, 'images'));
    const raw = Buffer.from(ONE_PIXEL_PNG.split(',')[1], 'base64');
    await writeFile(path.join(deckDir, 'images', 'pdf-figure.png'), raw);
    const node = { ...inlineImageNode(), src: '../images/pdf-figure.png', styles: { objectFit: 'contain' } };
    const image = buildImageElement(node, deckDir);
    assert.equal(image.path, path.join(deckDir, 'images', 'pdf-figure.png'));
    assert.equal(image.sizing.type, 'contain');
    const outputPath = path.join(deckDir, 'figure.pptx');
    const ir = {
      canvasWidth: 1600, canvasHeight: 900, bodyBgColor: 'rgb(255,255,255)',
      bodyBgImage: 'none', wrapperBgImage: 'none', bg: null, header: null, footer: null,
      overlays: [], rest: [],
      ct: { tag: 'DIV', bounds: { x: 0, y: 0, w: 1600, h: 900 }, styles: {}, children: [node] },
    };
    const result = await buildPptx([{ path: path.join(deckDir, 'pages', 'page_001.html'), ir }], deckDir, outputPath);
    assert.equal(result.successCount, 1);
    assert.equal(result.failCount, 0);
    const JSZip = (await import('jszip')).default;
    const archive = await JSZip.loadAsync(await readFile(outputPath));
    const media = Object.keys(archive.files).filter(name => name.startsWith('ppt/media/') && !archive.files[name].dir);
    assert.ok((await Promise.all(media.map(name => archive.files[name].async('nodebuffer'))))
      .some(bytes => bytes.equals(raw)));
    const xml = await archive.file('ppt/slides/slide1.xml').async('string');
    assert.match(xml, /<p:pic>/);
    assert.doesNotMatch(xml, /<a:srcRect[^>]+[lrtb]="[1-9]/);
  } finally {
    await rm(deckDir, { recursive: true, force: true });
  }
});
