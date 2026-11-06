import assert from 'node:assert/strict';
import test from 'node:test';
import PptxGenJS from 'pptxgenjs';
import JSZip from 'jszip';
import { buildTextElement, buildListElement } from '../lib/pptx_builder.mjs';
import { pxToInch } from '../lib/style_parser.mjs';

const node = (styles = {}) => ({
  tag: 'P', text: '中文正文与 English text',
  bounds: { x: 40, y: 80, w: 300, h: 60 },
  styles: { fontSize: '16px', color: '#000000', ...styles },
});

test('text stays inside measured HTML bounds for every alignment', () => {
  for (const textAlign of ['left', 'center', 'right']) {
    const n = node({ textAlign });
    const { options } = buildTextElement(n);
    assert.equal(options.x, pxToInch(n.bounds.x));
    assert.equal(options.w, pxToInch(n.bounds.w));
    assert.equal(options.h, pxToInch(n.bounds.h));
    assert.equal(options.valign, 'top');
    assert.equal(options.fit, 'shrink');
    assert.deepEqual(options.margin, [0, 0, 0, 0]);
  }
});

test('mixed runs retain padding and border insets without expanding the box', () => {
  const n = node({ paddingTop: '7px', paddingRight: '9px', paddingBottom: '7px', paddingLeft: '9px', borderLeftWidth: '1px' });
  n.textRuns = [{ text: 'τ', fontSize: '16px', italic: true }, { text: ' = 每轮接受 token 数', fontSize: '16px' }];
  const { options } = buildTextElement(n);
  assert.deepEqual(options.margin, [10, 9, 7, 7].map(x => pxToInch(x) * 72));
  assert.equal(options.w, pxToInch(300));
  assert.equal(options.wrap, true);
});

test('serialized heading and pill insets match the CSS sides in PowerPoint', async () => {
  const pptx = new PptxGenJS();
  const slide = pptx.addSlide();
  for (const styles of [
    { paddingLeft: '8px', borderLeftWidth: '3px' },
    { paddingTop: '6px', paddingBottom: '6px', paddingLeft: '12px', paddingRight: '12px' },
  ]) {
    const text = buildTextElement(node(styles));
    slide.addText(text.text, text.options);
  }
  const zip = await JSZip.loadAsync(await pptx.write({ outputType: 'nodebuffer' }));
  const xml = await zip.file('ppt/slides/slide1.xml').async('string');
  const bodies = [...xml.matchAll(/<a:bodyPr\b[^>]*>/g)].map(x => x[0]);
  const emu = x => Math.round(pxToInch(x) * 914400);
  for (const [index, expected] of [[0, { lIns: 11, tIns: 0, rIns: 0, bIns: 0 }], [1, { lIns: 12, tIns: 6, rIns: 12, bIns: 6 }]]) {
    for (const [side, px] of Object.entries(expected)) {
      const value = Number(bodies[index].match(new RegExp(`${side}="(\\d+)"`))[1]);
      assert.ok(Math.abs(value - emu(px)) <= 1, `${side}: ${value} vs ${emu(px)}`);
    }
  }
});

test('short lines and keep-all/pre-line still wrap; only explicit nowrap/pre disable it', () => {
  for (const whiteSpace of ['normal', 'pre-line', 'pre-wrap', 'nowrap', 'pre']) {
    const n = node({ whiteSpace, wordBreak: 'keep-all' });
    n.text = '研究问题';
    n.bounds.h = 20;
    assert.equal(buildTextElement(n).options.wrap, !['nowrap', 'pre'].includes(whiteSpace));
  }
});

test('list geometry also stays within the source column', () => {
  const n = node();
  n.listData = [{ text: '列表内容', styles: { fontSize: '16px' } }];
  const { options } = buildListElement(n);
  assert.equal(options.w, pxToInch(n.bounds.w));
  assert.equal(options.wrap, true);
  assert.equal(options.fit, 'shrink');
});
