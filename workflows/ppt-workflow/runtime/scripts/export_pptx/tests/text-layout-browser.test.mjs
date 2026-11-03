import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { chromium } from 'playwright';
import { extractPage } from '../lib/dom_extractor.mjs';
import { buildTextElement } from '../lib/pptx_builder.mjs';

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
