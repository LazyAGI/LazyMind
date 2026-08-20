import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { buildPptx } from '../lib/pptx_builder.mjs';

function glowIr() {
  return {
    canvasWidth: 1600,
    canvasHeight: 900,
    bodyBgColor: 'rgb(10, 14, 26)',
    bodyBgImage: 'none',
    wrapperBgColor: 'rgb(10, 14, 26)',
    wrapperBgImage: 'none',
    bg: {
      tag: 'DIV',
      id: 'bg',
      bounds: { x: 0, y: 0, w: 1600, h: 900 },
      styles: {
        backgroundColor: 'rgb(10, 14, 26)',
        backgroundImage: 'none',
        opacity: '1',
      },
      children: [{
        tag: 'DIV',
        className: 'bg-glow bg-glow-1',
        bounds: { x: -100, y: -200, w: 600, h: 600 },
        styles: {
          backgroundColor: 'rgb(0, 188, 212)',
          backgroundImage: 'none',
          borderRadius: '50%',
          filter: 'blur(150px)',
          opacity: '0.3',
        },
        children: [],
      }],
    },
    ct: null,
    header: null,
    footer: null,
    overlays: [],
    rest: [],
  };
}

test('large CSS blur exports as a subtle radial fade, not a solid disc', async () => {
  const deckDir = await mkdtemp(path.join(os.tmpdir(), 'lazymind-blur-glow-'));
  try {
    const outputPath = path.join(deckDir, 'glow.pptx');
    const inspectPy = path.join(deckDir, 'inspect.py');
    await writeFile(inspectPy, `
import zipfile, sys
z = zipfile.ZipFile(sys.argv[1])
xml = z.read('ppt/slides/slide1.xml').decode()
print('RADIAL', xml.count('<a:path path="circle">'))
print('ALPHA_10000', xml.count('<a:alpha val="10000"/>'))
print('ALPHA_ZERO', xml.count('<a:alpha val="0"/>'))
print('OUTER_SHADOW', xml.count('<a:outerShdw'))
print('SOLID_CYAN', xml.count('<a:solidFill><a:srgbClr val="00BCD4"'))
`);

    await buildPptx(
      [{ path: path.join(deckDir, 'page_001.html'), ir: glowIr() }],
      deckDir,
      outputPath,
    );
    const out = execFileSync('python3', [inspectPy, outputPath], { encoding: 'utf8' });
    assert.match(out, /RADIAL 1/);
    assert.match(out, /ALPHA_10000 1/);
    assert.match(out, /ALPHA_ZERO 1/);
    assert.match(out, /OUTER_SHADOW 0/);
    assert.match(out, /SOLID_CYAN 0/);
  } finally {
    await rm(deckDir, { recursive: true, force: true });
  }
});
