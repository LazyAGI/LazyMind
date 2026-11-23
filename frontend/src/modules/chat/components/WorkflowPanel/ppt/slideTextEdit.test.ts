import { describe, expect, it } from 'vitest';
import { slideTextEditor } from './slideTextEdit';

describe('SVG slide label editing', () => {
  it('edits individual text runs without replacing sibling spans, geometry or SVG metadata', () => {
    const source = '<svg viewBox="0 0 100 50"><title>Diagram</title><defs><text>Template</text></defs>'
      + '<path d="M0 0L5 5"/><text x="7">Team <tspan fill="red">A</tspan></text></svg><p>Footer</p>';
    const editor = slideTextEditor(source);
    const parse = (html: string) => new DOMParser().parseFromString(html, 'text/html');
    const runs = Array.from(parse(editor.preview).querySelectorAll('[data-ppt-text-run]'));
    expect(runs.map(run => run.textContent)).toEqual(['Team ', 'A', 'Footer']);
    expect(runs[0].namespaceURI).toBe('http://www.w3.org/2000/svg');
    const saved = parse(editor.edit(1, 'B & C'));
    expect(saved.querySelector('text[x="7"]')?.textContent).toBe('Team B & C');
    expect(saved.querySelector('tspan[fill="red"]')?.textContent).toBe('B & C');
    expect(saved.querySelector('path')?.getAttribute('d')).toBe('M0 0L5 5');
    expect(saved.querySelector('defs text')?.textContent).toBe('Template');
    expect(saved.querySelector('[data-ppt-text-run]')).toBeNull();
    expect(editor.edit(1, 'A')).toBe(source);
  });
});
