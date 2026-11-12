import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import PptxGenJS from 'pptxgenjs';
import JSZip from 'jszip';
import { normalizeTextParagraphs, normalizeTextFontCharsets, postprocessPptx } from '../lib/postprocess_pptx.mjs';

test('removes generated legacy charset hints without changing fonts or embedded/symbol metadata', () => {
  const xml = '<a:latin typeface="PingFang SC" pitchFamily="34" charset="0"/>'
    + '<a:ea typeface="Times New Roman" charset="-122"/>'
    + '<a:cs typeface="PingFang SC" charset="-120"/>'
    + '<a:latin typeface="Wingdings" charset="2"/>'
    + '<p:font typeface="Embedded Font" charset="-122"/>';
  const expected = '<a:latin typeface="PingFang SC" pitchFamily="34"/>'
    + '<a:ea typeface="Times New Roman"/>'
    + '<a:cs typeface="PingFang SC"/>'
    + '<a:latin typeface="Wingdings" charset="2"/>'
    + '<p:font typeface="Embedded Font" charset="-122"/>';
  assert.equal(normalizeTextFontCharsets(xml), expected);
  assert.equal(normalizeTextFontCharsets(expected), expected);
});

test('normalizes each paragraph without changing runs, breaks or paragraph styles', () => {
  const first = '<a:pPr algn="l"><a:lnSpc><a:spcPct val="150000"/></a:lnSpc></a:pPr>';
  const run = '<a:r><a:rPr i="1"/><a:t>中文 B &amp; n</a:t></a:r>';
  const tail = '<a:endParaRPr lang="zh-CN"/>';
  const second = '<a:p><a:pPr algn="r"/><a:r><a:t>下一段</a:t></a:r></a:p>';
  const input = `<a:p>${first}${run}<a:br/>${first}${run}${tail}</a:p>${second}`;
  const expected = `<a:p>${first}${run}<a:br/>${run}${tail}</a:p>${second}`;
  assert.equal(normalizeTextParagraphs(input), expected);
  assert.equal(normalizeTextParagraphs(expected), expected);
  assert.equal(normalizeTextParagraphs(`<a:p>${run}</a:p>`), `<a:p>${run}</a:p>`);
});

test('postprocess repairs real mixed CJK/math output even with no gradient handlers', async () => {
  const dir = await mkdtemp(path.join(os.tmpdir(), 'ppt-paragraphs-'));
  try {
    const output = path.join(dir, 'mixed.pptx');
    const pptx = new PptxGenJS();
    pptx.addSlide().addText([
      { text: '残差流扩展为', options: { fontFace: 'PingFang SC' } },
      { text: 'n', options: { fontFace: 'Times New Roman', italic: true } },
      { text: '=4）；投影到双随机矩阵', options: { fontFace: 'PingFang SC' } },
    ], { x: 1, y: 1, w: 8, h: 2, lineSpacingMultiple: 1.5 });
    await pptx.writeFile({ fileName: output });
    const before = await JSZip.loadAsync(await readFile(output));
    const originalXml = await before.file('ppt/slides/slide1.xml').async('string');
    await postprocessPptx(output);
    const after = await JSZip.loadAsync(await readFile(output));
    const xml = await after.file('ppt/slides/slide1.xml').async('string');
    for (const [, body] of xml.matchAll(/<a:p>([\s\S]*?)<\/a:p>/g)) {
      assert.ok((body.match(/<a:pPr\b/g) || []).length <= 1);
      if (body.includes('<a:pPr')) assert.ok(body.startsWith('<a:pPr'));
    }
    assert.deepEqual(xml.match(/<a:r>[\s\S]*?<\/a:r>/g), normalizeTextFontCharsets(originalXml).match(/<a:r>[\s\S]*?<\/a:r>/g));
    assert.doesNotMatch(xml, /<a:(?:latin|ea|cs)\b[^>]*charset=/);
    const theme = await after.file('ppt/theme/theme1.xml').async('string');
    assert.equal(theme, normalizeTextFontCharsets(await before.file('ppt/theme/theme1.xml').async('string')));
    assert.equal(await after.file('ppt/presentation.xml').async('string'), await before.file('ppt/presentation.xml').async('string'));
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});
