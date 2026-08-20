import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { buildPptx } from '../lib/pptx_builder.mjs';


const ONE_PIXEL_PNG =
  'data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=';

test('uses the captured page image when editable IR cannot be built', async () => {
  const deckDir = await mkdtemp(path.join(os.tmpdir(), 'lazymind-ppt-fallback-'));
  try {
    const htmlPath = path.join(deckDir, 'page_001.html');
    const outputPath = path.join(deckDir, 'fallback.pptx');
    await writeFile(htmlPath, '<!doctype html><html><body>fallback</body></html>');
    await writeFile(path.join(deckDir, 'page_001.notes.txt'), 'Fallback speaker notes');

    const result = await buildPptx(
      [{
        path: htmlPath,
        ir: { error: 'unsupported page structure' },
        fallbackImageDataUri: ONE_PIXEL_PNG,
      }],
      deckDir,
      outputPath,
    );

    assert.equal(result.successCount, 1);
    assert.equal(result.failCount, 0);
    assert.equal(result.fallbackCount, 1);
    assert.equal(result.fallbacks.length, 1);
    assert.ok((await readFile(outputPath)).length > 0);
  } finally {
    await rm(deckDir, { recursive: true, force: true });
  }
});
