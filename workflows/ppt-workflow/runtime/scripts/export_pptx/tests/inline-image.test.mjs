import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { buildImageElement, buildPptx } from '../lib/pptx_builder.mjs';

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
