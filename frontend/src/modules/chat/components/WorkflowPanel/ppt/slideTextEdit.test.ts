import { describe, expect, it } from 'vitest';
import { editSvgText, slideTextEditor } from './slideTextEdit';

describe('SVG slide label editing', () => {
  it('edits individual text runs without replacing sibling spans, geometry or SVG metadata', () => {
    const source = '<svg viewBox="0 0 100 50"><title>Diagram</title><defs><text>Template</text></defs>'
      + '<path d="M0 0L5 5"/><text x="7">Team <tspan fill="red">A</tspan></text></svg><p>Footer</p>';
    const editor = slideTextEditor(source);
    const parse = (html: string) => new DOMParser().parseFromString(html, 'text/html');
    const runs = Array.from(parse(editor.preview).querySelectorAll('[data-ppt-text-block]'));
    expect(runs.map(run => run.textContent)).toEqual(['Team A', 'Footer']);
    expect(runs[0].namespaceURI).toBe('http://www.w3.org/2000/svg');
    editSvgText(runs[0], 'Team B & C');
    const saved = parse(editor.editBlock(0, runs[0]));
    expect(saved.querySelector('text[x="7"]')?.textContent).toBe('Team B & C');
    expect(saved.querySelector('tspan[fill="red"]')?.textContent).toBe('B & C');
    expect(saved.querySelector('path')?.getAttribute('d')).toBe('M0 0L5 5');
    expect(saved.querySelector('defs text')?.textContent).toBe('Template');
    expect(saved.querySelector('[data-ppt-text-block]')).toBeNull();
    editSvgText(runs[0], 'Team A');
    expect(editor.editBlock(0, runs[0])).toBe(source);
  });
});

it('addresses both styled title lines as one editable and polishable block', () => {
  const source = '<h1 data-el="title"><span>英雄联盟：</span><br><b>五人协作的竞技场</b></h1><p>副标题</p>';
  const editor = slideTextEditor(source);
  const preview = new DOMParser().parseFromString(editor.preview, 'text/html');
  const title = preview.querySelector('h1')!;
  expect(title.getAttribute('data-ppt-text-block')).toBe('0');
  expect(title.querySelector('[data-ppt-text-block]')).toBeNull();
  expect(editor.selection(0)?.segments).toEqual(['英雄联盟：', '五人协作的竞技场']);
  title.querySelector('b')!.textContent = '团队竞技场';
  const saved = editor.editBlock(0, title);
  expect(saved).toContain('<span>英雄联盟：</span><br><b>团队竞技场</b>');
  expect(saved).toContain('<p>副标题</p>');
  expect(saved).not.toContain('data-ppt-text-');
});

it('keeps legacy paragraphs and separate table cells independently addressable', () => {
  const editor = slideTextEditor('<main>Intro <p>A <b>B</b></p><table data-el="table"><tr><td>X</td><td>X</td></tr></table></main>');
  expect([0, 1, 2, 3].map(i => editor.selection(i))).toEqual([
    {tag: 'main', occurrence: 0, el: '', direct_index: 0, segments: ['Intro']},
    {tag: 'p', occurrence: 0, el: '', segments: ['A', 'B']},
    {tag: 'td', occurrence: 0, el: '', segments: ['X']},
    {tag: 'td', occurrence: 1, el: '', segments: ['X']},
  ]);
});

it('saves cross-line edits and user line breaks without copying preview attributes', () => {
  const editor = slideTextEditor('<h1><b>First</b><br><i>Second</i></h1><p>Neighbor</p>');
  const title = new DOMParser().parseFromString(editor.preview, 'text/html').querySelector('h1')!;
  const range = title.ownerDocument.createRange();
  range.setStart(title.querySelector('b')!.firstChild!, 3);
  range.setEnd(title.querySelector('i')!.firstChild!, 2);
  range.deleteContents();
  range.insertNode(title.ownerDocument.createElement('br'));
  title.querySelector('i')!.setAttribute('style', 'color: red');
  expect(editor.editBlock(0, title)).toContain('<h1><b>Fir</b><br><i>cond</i></h1><p>Neighbor</p>');
});
