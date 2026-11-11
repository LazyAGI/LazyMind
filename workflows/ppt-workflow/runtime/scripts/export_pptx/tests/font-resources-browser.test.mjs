import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtemp, writeFile, readFile, rm } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright';
import { extractPage } from '../lib/dom_extractor.mjs';

test('file-origin export loads managed URL fonts before measuring text', async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'ppt-font-resource-browser-'));
  const previous = process.env.LAZYMIND_FONT_DIR;
  process.env.LAZYMIND_FONT_DIR = directory;
  let browser;
  try {
    const source = path.join(directory, 'font.ttf');
    execFileSync(process.env.LAZYMIND_FONT_SUBSET_PYTHON || 'python3', ['-c', `
import sys
from fontTools.fontBuilder import FontBuilder
from fontTools.pens.ttGlyphPen import TTGlyphPen
b = FontBuilder(1000, isTTF=True)
glyphs = ['.notdef', 'A', 'B']
b.setupGlyphOrder(glyphs)
b.setupCharacterMap({65: 'A', 66: 'B'})
b.setupGlyf({g: TTGlyphPen(None).glyph() for g in glyphs})
b.setupHorizontalMetrics({g: (900, 0) for g in glyphs})
b.setupHorizontalHeader(ascent=800, descent=-200)
b.setupNameTable({'familyName':'Resource Test Font', 'styleName':'Regular', 'fullName':'Resource Test Font'})
b.setupOS2(fsType=0)
b.setupPost()
b.save(sys.argv[1])
b.font.flavor = 'woff'
b.save(sys.argv[1] + '.woff')
b.font.flavor = None
b.font['OS/2'].usWeightClass = 700
b.save(sys.argv[1] + '.bold.ttf')
b.font.flavor = 'woff'
b.save(sys.argv[1] + '.bold.woff')
`, source]);
    const data = await readFile(source);
    const id = createHash('sha256').update(data).digest('hex');
    await writeFile(path.join(directory, `${id}.ttf`), data);
    const metadata = { family: 'Resource Test Font', weight: 400, style: 'normal', sha256: id };
    await writeFile(path.join(directory, `${id}.json`), JSON.stringify({
      id, file: `${id}.ttf`, family: metadata.family, aliases: [metadata.family],
      weight: 400, style: 'normal', embeddable: true,
    }));
    const token = Buffer.from(JSON.stringify(metadata)).toString('base64');
    const resource = `/static-files/presentation-fonts/${id}.ttf`;
    const html = `<html><head><style data-lazymind-font-required="true">/*lazymind-font:${token}*/@font-face{font-family:"Resource Test Font";font-weight:400;font-style:normal;src:url(${resource})}</style></head><body><div class="wrapper" style="width:1280px;height:720px"><div id="ct"><span id="text" style="font-family:'Resource Test Font';font-size:100px">AB</span></div></div></body></html>`;
    const htmlPath = path.join(directory, 'page_001.html');
    await writeFile(htmlPath, html);
    browser = await chromium.launch({ headless: true });
    const page = await browser.newPage();
    const response = page.waitForResponse(url => url.url().startsWith('https://lazymind-fonts.invalid/'));
    await extractPage(page, htmlPath);
    assert.deepEqual(await (await response).body(), data);
    const result = await page.evaluate(() => ({
      fonts: [...document.fonts].map(font => font.status),
      width: document.getElementById('text').getBoundingClientRect().width,
      css: document.querySelector('style[data-lazymind-font-required]').textContent,
    }));
    assert.deepEqual(result.fonts, ['loaded']);
    assert.ok(Math.abs(result.width - 180) < 1, `Expected managed glyph width 180, got ${result.width}`);
    assert.doesNotMatch(result.css, /data:font/);
    assert.equal(await readFile(htmlPath, 'utf8'), html);

    // Migration may upgrade only available variants in one family block.
    // The absent original bold face must not prevent its inline subset rendering.
    const boldData = await readFile(source + '.bold.ttf');
    const boldToken = Buffer.from(JSON.stringify({ ...metadata, weight: 700,
      sha256: createHash('sha256').update(boldData).digest('hex') })).toString('base64');
    const boldWoff = (await readFile(source + '.bold.woff')).toString('base64');
    const mixed = html.replace('</style>', `/*lazymind-font:${boldToken}*/@font-face{font-family:"Resource Test Font";font-weight:700;font-style:normal;src:url(data:font/woff;base64,${boldWoff})}</style>`)
      .replace('</span>', '</span><span style="font-family:&#39;Resource Test Font&#39;;font-weight:700;font-size:100px">AB</span>');
    await writeFile(htmlPath, mixed);
    await extractPage(page, htmlPath);
    assert.deepEqual(await page.evaluate(() => [...document.fonts].map(font => font.status)), ['loaded', 'loaded']);

    const screenshot = path.join(directory, 'preview.png');
    const screenshotResult = execFileSync(process.execPath, [
      fileURLToPath(new URL('../screenshot.mjs', import.meta.url)), '--html', htmlPath, '--out', screenshot,
    ], { encoding: 'utf8' });
    assert.equal(JSON.parse(screenshotResult).status, 'ok');
    assert.ok((await readFile(screenshot)).length > 100);
    const pdfResult = execFileSync(process.execPath, [
      fileURLToPath(new URL('../html_to_pdf.mjs', import.meta.url)), '--deck-dir', directory,
      '--pages-dir', directory, '--output-dir', directory, '--output', 'preview.pdf', '--batch', '--force',
    ], { encoding: 'utf8' });
    assert.equal(JSON.parse(pdfResult).success, true);
    assert.equal((await readFile(path.join(directory, 'preview.pdf'))).subarray(0, 4).toString(), '%PDF');

    // A missing original is a portability error, never a silent layout fallback.
    await rm(path.join(directory, `${id}.ttf`));
    await assert.rejects(extractPage(page, htmlPath), /Original font resource unavailable/);
    const legacy = html.replace(resource, `data:font/woff;base64,${(await readFile(source + '.woff')).toString('base64')}`);
    await writeFile(htmlPath, legacy);
    await extractPage(page, htmlPath);
    assert.deepEqual(await page.evaluate(() => [...document.fonts].map(font => font.status)), ['loaded']);
  } finally {
    if (browser) await browser.close();
    if (previous === undefined) delete process.env.LAZYMIND_FONT_DIR;
    else process.env.LAZYMIND_FONT_DIR = previous;
    await rm(directory, { recursive: true, force: true });
  }
});
