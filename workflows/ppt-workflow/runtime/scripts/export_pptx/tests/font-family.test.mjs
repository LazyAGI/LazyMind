import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { buildPptx, resolveDeckTypography } from '../lib/pptx_builder.mjs';
import { parseFontFamily } from '../lib/style_parser.mjs';

test('named display fonts are preserved instead of collapsed to generic fonts', () => {
  assert.equal(parseFontFamily("'ZCOOL KuaiLe', cursive"), 'ZCOOL KuaiLe');
  assert.equal(parseFontFamily("Orbitron, 'Noto Sans SC', sans-serif"), 'Orbitron');
  assert.equal(
    parseFontFamily('system-ui, sans-serif', { genericFallback: false }),
    null,
  );
});

test('deck typography reads heading_font and body_font independently', () => {
  assert.deepEqual(resolveDeckTypography({
    typography: {
      heading_font: "Orbitron, 'Microsoft YaHei', sans-serif",
      body_font: "'Noto Sans SC', 'Microsoft YaHei', sans-serif",
    },
  }), {
    headingFontFace: 'Orbitron',
    bodyFontFace: 'Noto Sans SC',
  });
});

test('on-demand HTML export infers named fonts when style_spec is unavailable', () => {
  const pages = [{
    ir: {
      ct: {
        tag: 'DIV',
        children: [
          {
            tag: 'H1',
            text: '未来标题',
            styles: { fontFamily: 'Orbitron, sans-serif' },
            children: [],
          },
          {
            tag: 'P',
            text: '正文内容',
            styles: { fontFamily: "'Noto Sans SC', sans-serif" },
            children: [],
          },
        ],
      },
      overlays: [],
      rest: [],
    },
  }];

  assert.deepEqual(resolveDeckTypography({}, pages), {
    headingFontFace: 'Orbitron',
    bodyFontFace: 'Noto Sans SC',
  });
});

test('generic element CSS uses distinct deck heading and body fonts in PPTX', async () => {
  const deckDir = await mkdtemp(path.join(os.tmpdir(), 'lazymind-ppt-fonts-'));
  try {
    await writeFile(path.join(deckDir, 'style_spec.json'), JSON.stringify({
      typography: {
        heading_font: 'Orbitron, sans-serif',
        body_font: "'Noto Sans SC', sans-serif",
      },
    }));
    const outputPath = path.join(deckDir, 'fonts.pptx');
    const baseStyles = {
      color: 'rgb(0, 0, 0)',
      fontSize: '28px',
      fontWeight: '400',
      fontFamily: 'system-ui, sans-serif',
      backgroundColor: 'rgba(0, 0, 0, 0)',
      backgroundImage: 'none',
      opacity: '1',
      textAlign: 'left',
      verticalAlign: 'top',
      display: 'block',
    };
    await buildPptx([{
      path: path.join(deckDir, 'page_001.html'),
      ir: {
        canvasWidth: 1600,
        canvasHeight: 900,
        bodyBgColor: 'rgb(255, 255, 255)',
        wrapperBgColor: 'rgb(255, 255, 255)',
        bg: null,
        header: null,
        footer: null,
        overlays: [],
        rest: [],
        ct: {
          tag: 'DIV',
          bounds: { x: 0, y: 0, w: 1600, h: 900 },
          styles: { ...baseStyles },
          children: [
            {
              tag: 'H1',
              el: 'title',
              text: '未来标题',
              bounds: { x: 80, y: 80, w: 900, h: 90 },
              styles: { ...baseStyles, fontSize: '56px', fontWeight: '700' },
              children: [],
            },
            {
              tag: 'P',
              el: 'narrative',
              text: '正文内容',
              bounds: { x: 80, y: 220, w: 900, h: 60 },
              styles: { ...baseStyles },
              children: [],
            },
          ],
        },
      },
    }], deckDir, outputPath);

    const inspect = `
import re, sys, zipfile
with zipfile.ZipFile(sys.argv[1]) as archive:
    xml = archive.read('ppt/slides/slide1.xml').decode('utf-8')
    print('\\n'.join(re.findall(r'typeface="([^"]+)"', xml)))
`;
    const fonts = execFileSync('python3', ['-c', inspect, outputPath], { encoding: 'utf8' });
    assert.match(fonts, /Orbitron/);
    assert.match(fonts, /Noto Sans SC/);
  } finally {
    await rm(deckDir, { recursive: true, force: true });
  }
});
