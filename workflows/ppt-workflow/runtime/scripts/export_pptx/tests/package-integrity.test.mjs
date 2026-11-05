import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import PptxGenJS from 'pptxgenjs';
import JSZip from 'jszip';
import { postprocessPptx } from '../lib/postprocess_pptx.mjs';

test('multi-slide packages retain their content without phantom masters, even with no gradient handlers', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'ppt-package-'));
  try {
    const output = path.join(dir, 'deck.pptx');
    const deck = new PptxGenJS();
    for (let i = 1; i <= 3; i += 1) {
      const slide = deck.addSlide();
      slide.addText(`Slide ${i}`, { x: 1, y: 1, w: 4, h: 1 });
      slide.addNotes(`Speaker notes ${i}`);
    }
    await deck.writeFile({ fileName: output });
    const original = await JSZip.loadAsync(await readFile(output));
    await postprocessPptx(output, []);
    const result = await JSZip.loadAsync(await readFile(output));
    const types = await result.file('[Content_Types].xml').async('string');
    for (const match of types.matchAll(/<Override\b[^>]*PartName="\/([^"]+)"/g)) {
      assert.ok(result.file(match[1]), `Missing content type target: ${match[1]}`);
    }
    for (const name of Object.keys(original.files)) {
      if (original.files[name].dir || name === '[Content_Types].xml') continue;
      assert.deepEqual(await result.file(name).async('uint8array'), await original.file(name).async('uint8array'));
    }
    const first = await readFile(output);
    await postprocessPptx(output, []);
    assert.deepEqual(await readFile(output), first);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
