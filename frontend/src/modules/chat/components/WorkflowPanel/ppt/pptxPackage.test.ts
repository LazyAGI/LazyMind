import { expect, it } from 'vitest';
import PptxGenJS from 'pptxgenjs';
import JSZip from '@progress/jszip-esm';
import { normalizePptxPackage } from './pptxPackage';

it('exports a multi-slide deck without dangling master declarations and preserves notes', async () => {
  const deck = new PptxGenJS();
  for (let i = 1; i <= 3; i += 1) {
    const slide = deck.addSlide();
    slide.addText(`Slide ${i}`, { x: 1, y: 1, w: 4, h: 1 });
    slide.addNotes(`Speaker notes ${i}`);
  }
  const source = await deck.write({ outputType: 'arraybuffer' }) as ArrayBuffer;
  const original = await new JSZip().loadAsync(source);
  const blob = await normalizePptxPackage(source);
  const result = await new JSZip().loadAsync(await blob.arrayBuffer());
  const types = new DOMParser().parseFromString(
    await result.file('[Content_Types].xml')!.async('string'), 'application/xml',
  );
  for (const override of Array.from(types.getElementsByTagName('Override'))) {
    expect(result.file(override.getAttribute('PartName')!.slice(1))).not.toBeNull();
  }
  expect(Object.keys(result.files).filter(name => /^ppt\/slides\/slide\d+\.xml$/.test(name))).toHaveLength(3);
  for (const name of Object.keys(original.files).filter(name => !original.files[name].dir && name !== '[Content_Types].xml')) {
    expect(await result.file(name)!.async('uint8array')).toEqual(await original.file(name)!.async('uint8array'));
  }
});
