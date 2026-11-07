import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { chromium } from 'playwright';
import { extractPage } from '../lib/dom_extractor.mjs';
import { flattenIRToElements } from '../lib/pptx_builder.mjs';

test('editable export preserves unequal table cells, spans and rendered fallback fonts', async () => {
 const dir = await mkdtemp(path.join(os.tmpdir(), 'ppt-table-'));
 const browser = await chromium.launch({headless:true});
 try {
  const html = path.join(dir,'page.html');
  await writeFile(html, `<style>.wrapper{width:1600px;height:900px}table{border-collapse:collapse;table-layout:fixed;width:1000px}td{border:1px solid black;padding:5px 13px;font:20px 'Definitely Missing Font',Arial;vertical-align:top}tr:first-child{height:100px}tr:last-child{height:180px}</style><div class="wrapper"><div id="ct"><table><colgroup><col style="width:200px"><col style="width:800px"></colgroup><tr><td id="short">One</td><td>Two</td></tr><tr><td colspan="2">Spanning cell</td></tr></table></div></div>`);
  const page = await browser.newPage();
  const ir = await extractPage(page, html);
  const table = ir.ct.children.find(n=>n.tag==='TABLE');
  assert.equal(table.children.length,3);
  assert.ok(Math.abs(table.children[0].bounds.w - 200) < 2);
  assert.ok(Math.abs(table.children[1].bounds.w - 800) < 2);
  assert.ok(Math.abs(table.children[2].bounds.w - 1000) < 2);
  assert.ok(Math.abs(table.children[2].bounds.h - 180) < 2);
  assert.doesNotMatch(table.children[0].styles.fontFamily,/Definitely Missing/);
  const elements=flattenIRToElements(table,dir);
  assert.equal(elements.filter(e=>e.type==='table').length,0);
  assert.equal(elements.filter(e=>e.type==='text').length,3);
  const text=elements.find(e=>e.type==='text');
  assert.equal(text.data.options.valign,'top');
  assert.ok(text.data.options.margin[0]>0);
 } finally {await browser.close();await rm(dir,{recursive:true,force:true});}
});


test('grid agenda preserves its columns and does not invent native numbering', async () => {
 const dir = await mkdtemp(path.join(os.tmpdir(), 'ppt-agenda-'));
 const browser = await chromium.launch({headless:true});
 try {
  const html=path.join(dir,'page.html');
  await writeFile(html, `<style>.wrapper{width:1600px;height:900px}ol{list-style:none;padding:0}li{display:grid;grid-template-columns:90px 700px 400px;height:100px;align-items:center}span{font-size:30px}</style><div class="wrapper"><div id="ct"><ol><li><span>01</span><span>Title</span><span>Description</span></li><li><span>02</span><span>Next</span><span>Details</span></li></ol></div></div>`);
  const ir=await extractPage(await browser.newPage(),html);
  const list=ir.ct.children[0];
  assert.equal(list.listData,undefined);
  assert.equal(list.children.length,2);
  assert.equal(list.children[0].children.length,3);
  assert.equal(list.children[0].children[1].bounds.x-list.children[0].children[0].bounds.x,90);
  assert.equal(list.children[1].bounds.y-list.children[0].bounds.y,100);
  const elements=flattenIRToElements(list,dir);
  assert.equal(elements.filter(e=>e.type==='list').length,0);
  assert.equal(elements.filter(e=>e.type==='text').length,6);
 } finally {await browser.close();await rm(dir,{recursive:true,force:true});}
});
