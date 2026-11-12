import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { chromium } from 'playwright';
import { extractPage } from '../lib/dom_extractor.mjs';
import { buildTextElement } from '../lib/pptx_builder.mjs';

test('standalone SVG retains CSS text fonts and colors without changing the page DOM', async () => {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'ppt-svg-style-'));
  const browser = await chromium.launch({ headless: true });
  try {
    const html = path.join(dir, 'page.html');
    await writeFile(html, `<style>.wrapper{width:1280px;height:720px}.label{fill:rgb(230,237,247);font-size:11px;font-family:Arial;font-weight:700}.box{fill:rgb(7,13,24);stroke:rgb(45,212,191)}</style><div class="wrapper"><div id="ct"><svg width="300" height="100"><rect class="box" width="300" height="100"/><text class="label" x="10" y="30">M = 16</text></svg></div></div>`);
    const page = await browser.newPage();
    const ir = await extractPage(page, html);
    let captured;
    const visit = node => { if (!node) return; if (node.svgContent) captured = node.svgContent; for (const child of node.children || []) visit(child); };
    visit(ir.ct);
    assert.match(captured, /font-size="11px"/);
    assert.match(captured, /font-family="Arial"/);
    assert.match(captured, /fill="rgb\(230, 237, 247\)"/);
    assert.equal(await page.locator('svg text').getAttribute('style'), null);
  } finally {
    await browser.close();
    await rm(dir, { recursive: true, force: true });
  }
});

test('horizontal flex formulas do not become paragraphs and retain real box insets', async () => {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'ppt-flex-layout-'));
  const browser = await chromium.launch({ headless: true });
  try {
    const html = path.join(dir, 'page.html');
    await writeFile(html, `<style>*{box-sizing:border-box}.wrapper{width:1280px;height:720px}.chip{display:flex;align-items:center;width:160px;padding:7px 9px;border:1px solid blue;font-size:12px;line-height:1.5}</style><div class="wrapper"><div id="ct"><div id="row" class="chip"><span>τ</span> = 每轮接受 token 数</div><div id="column" class="chip" style="flex-direction:column"><span>τ</span> = 每轮接受 token 数</div></div></div>`);
    const ir = await extractPage(await browser.newPage(), html);
    const nodes = [];
    const visit = x => { if (!x || typeof x !== 'object') return; if (x.id) nodes.push(x); for (const child of x.children || []) visit(child); };
    for (const key of ['bg', 'header', 'ct', 'footer']) visit(ir[key]);
    for (const x of ir.rest || []) visit(x);
    const row = nodes.find(x => x.id === 'row');
    const column = nodes.find(x => x.id === 'column');
    assert.ok(row?.textRuns?.length);
    assert.equal(row.textRuns[0].isBlock, false);
    assert.equal(column.textRuns[0].isBlock, true);
    assert.equal(row.styles.borderLeftWidth, '1px');
    assert.equal(buildTextElement(row).options.valign, 'middle');
    assert.ok(buildTextElement(row).text.every(run => !run.options.breakLine));
  } finally {
    await browser.close();
    await rm(dir, { recursive: true, force: true });
  }
});


test('chart fallback serializes quoted font families as valid standalone XML', async () => {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'ppt-chart-svg-'));
  const browser = await chromium.launch({ headless: true });
  try {
    const html = path.join(dir, 'page.html');
    await writeFile(html, `<div class="wrapper" style="width:1280px;height:720px"><div id="ct"><div id="chart_1"><svg width="300" height="100"><text x="10" y="30" style='font-family:"Inter","Noto Sans SC",sans-serif;fill:#22D3EE'>RMSE 15.2×</text></svg></div></div></div><script>window.__pptxChartsReady=1;window.echarts={getInstanceByDom:()=>({getOption:()=>({xAxis:[{type:'log'}],series:[]}),getDataURL:()=>{throw new Error('SVG exporter must not be used')}})};</script>`);
    const page = await browser.newPage();
    const ir = await extractPage(page, html);
    const uri = ir._chartOptions.chart_1.renderedImageDataUri;
    assert.ok(uri.startsWith('data:image/svg+xml;base64,'));
    const svg = Buffer.from(uri.split(',')[1], 'base64').toString();
    const parsed = await page.evaluate(svg => {
      const doc = new DOMParser().parseFromString(svg, 'image/svg+xml');
      return { error: !!doc.querySelector('parsererror'), family: doc.querySelector('text')?.getAttribute('font-family'), text: doc.querySelector('text')?.textContent };
    }, svg);
    assert.equal(parsed.error, false);
    assert.match(svg, /xmlns="http:\/\/www.w3.org\/2000\/svg"/);
    assert.match(parsed.family, /Inter/);
    assert.equal(parsed.text, 'RMSE 15.2×');
  } finally {
    await browser.close();
    await rm(dir, { recursive: true, force: true });
  }
});
