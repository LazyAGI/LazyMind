import { beforeEach, describe, expect, it, vi } from 'vitest';
import { markdownParagraphAtRange, markdownSelectionRange, preserveMarkdownSource } from './writerMarkdownSource';

const diffBudget = vi.hoisted(() => ({ maxChars: Infinity, abortLines: false }));
vi.mock('diff', async (importOriginal) => {
 const actual = await importOriginal<typeof import('diff')>();
 return { ...actual,
  diffChars: (a: string, b: string, options: import('diff').DiffCharsOptionsAbortable) =>
   a.length + b.length > diffBudget.maxChars ? undefined : actual.diffChars(a, b, options),
  diffLines: (a: string, b: string, options: import('diff').DiffLinesOptionsAbortable) =>
   diffBudget.abortLines ? undefined : actual.diffLines(a, b, options),
 };
});
beforeEach(() => {
 diffBudget.maxChars = Infinity;
 diffBudget.abortLines = false;
});

describe('source positions and untouched Markdown', () => {
 it.each([
  ['## 😀 **Same**\n\nSame\n\n## 😀 **Same**', '<h2>😀 <strong>Same</strong></h2><p>Same</p><h2>😀 <strong>Same</strong></h2>', 'h2', 1, 'Same', 3],
  ['3. Same\n4. Same', '<ol start="3"><li>Same</li><li>Same</li></ol>', 'li', 1, 'Same', 0],
  ['- Parent\n  - **Child**\n- Last', '<ul><li>Parent<ul><li><strong>Child</strong></li></ul></li><li>Last</li></ul>', 'li', 1, 'Child', 0],
  ['- First\n  second', '<ul><li>First\nsecond</li></ul>', 'li', 0, 'First\nsecond', 0],
  ['### # ###', '<h3>#</h3>', 'h3', 0, '#', 0],
  ['Setext\n======', '<h1>Setext</h1>', 'h1', 0, 'Setext', 0],
 ] as const)('maps heading/list text to its exact source span: %s', (source, html, tag, index, selectedText, startOffset) => {
  const root = document.createElement('div'); root.className = 'mdxeditor-root-contenteditable'; root.innerHTML = html;
  const paragraph = root.querySelectorAll<HTMLElement>(tag)[index];
  const result = markdownSelectionRange(source, { selectedText, paragraph, startOffset });
  expect(Array.from(source).slice(result.start, result.end).join('')).toBe(result.selected_text);
  expect(result.selected_text).toBe(selectedText === 'First\nsecond' ? 'First\n  second' : selectedText);
  const from = Array.from(source).slice(0, result.start).join('').length;
  const to = Array.from(source).slice(0, result.end).join('').length;
  expect(markdownParagraphAtRange(root, source, from, to)).toBe(paragraph);
  if (index === 1 && selectedText === 'Same') expect(from).toBe(source.lastIndexOf('Same'));
 });

 it('reattaches the correct repeated paragraph after the editor DOM is rebuilt', () => {
  const source = 'Accepted and expanded\n\n😀 **Same**\n\nMiddle edit\n\n😀 **Same**';
  const root = document.createElement('div');
  root.innerHTML = '<div class="mdxeditor-root-contenteditable"><p>Accepted and expanded</p><p>😀 <strong>Same</strong></p><p>Middle edit</p><p>😀 <strong>Same</strong></p></div>';
  const start = source.lastIndexOf('😀');
  expect(markdownParagraphAtRange(root, source, start, source.length)).toBe(root.querySelectorAll('p')[3]);
  root.querySelectorAll('p')[3].remove();
  expect(markdownParagraphAtRange(root, source, start, source.length)).toBeNull();
 });
 it('locates the second identical paragraph with Unicode and formatting', () => {
  const source = '😀 **相同** [链接](https://example.org)\n\n😀 **相同** [链接](https://example.org)';
  const editor = document.createElement('div'); editor.className = 'mdxeditor-root-contenteditable';
  editor.innerHTML = '<p>😀 <strong>相同</strong> <a>链接</a></p><p>😀 <strong>相同</strong> <a>链接</a></p>';
  const result = markdownSelectionRange(source, { selectedText:'相同 链接', paragraph: editor.querySelectorAll('p')[1], startOffset:3 });
  const runes = Array.from(source);
  expect(result.start).toBeGreaterThan(Array.from(source).length / 2);
  expect(runes.slice(result.start, result.end).join('')).toBe(result.selected_text);
  expect(result.selected_text).toBe('相同** [链接');
 });
 it('refuses to guess an identical paragraph without DOM identity', () => {
  expect(() => markdownSelectionRange('same\n\nsame', {selectedText:'same'})).toThrow(/ambiguous/);
 });
 it('preserves unedited links, tables, ampersands and terminal newlines', () => {
  const original = '\nA & B\n\nhttps://example.org\n\n| A | B |\n|---|---|\n|1|2|\n\n';
  const normalized = 'A \\& B\n\n[https://example.org](https://example.org)\n\n| A | B |\n| - | - |\n| 1 | 2 |';
  expect(preserveMarkdownSource(original, normalized, normalized.replace('A \\& B','Updated'))).toBe(original.replace('A & B','Updated'));
  expect(preserveMarkdownSource(original, normalized, normalized)).toBe(original);
 });
});

it('keeps untouched inline URL and ampersand spelling while editing the same paragraph', () => {
  const source = 'Old https://example.org A & B.\n';
  const normalized = 'Old [https://example.org](https://example.org) A \\& B.';
  expect(preserveMarkdownSource(source, normalized, normalized.replace('Old','New'))).toBe(source.replace('Old','New'));
});

it('changes inline formatting without normalizing an unrelated URL in that paragraph', () => {
  const source = 'Old https://example.org A & B.\n';
  const normalized = 'Old [https://example.org](https://example.org) A \\& B.';
  expect(preserveMarkdownSource(source, normalized, normalized.replace('Old','**Old**'))).toBe(source.replace('Old','**Old**'));
});

it('preserves existing source blocks when paragraphs are inserted or removed', () => {
 const source='Old\n\nhttps://example.org\n\n| A | B |\n|---|---|\n|1|2|\n';
 const previous='Old\n\n[https://example.org](https://example.org)\n\n| A | B |\n| - | - |\n| 1 | 2 |';
 expect(preserveMarkdownSource(source,previous,previous.replace('Old','Old\n\nNew paragraph'))).toBe(source.replace('Old','Old\n\nNew paragraph'));
 expect(preserveMarkdownSource(source,previous,previous.replace('Old\n\n',''))).toBe(source.replace('Old\n\n',''));
});
it('applies text and formatting edits together without normalizing untouched text', () => {
 expect(preserveMarkdownSource('Old A & B.\n','Old A \\& B.','**New** A \\& B.')).toBe('**New** A & B.\n');
});
it('preserves inline source when splitting and merging paragraphs', () => {
 const source='Old https://example.org A & B.\n';
 const before='Old [https://example.org](https://example.org) A \\& B.';
 const split=before.replace('Old ','Old\n\n');
 expect(preserveMarkdownSource(source,before,split)).toBe(source.replace('Old ','Old\n\n'));
 expect(preserveMarkdownSource(source.replace('Old ','Old\n\n'),split,before)).toBe(source);
});
it('keeps edited link destinations with parentheses while preserving unrelated source spelling',()=>{
 const url='https://example.org/a(b)/old';
 const source=`Visit ${url} A & B.\n`;
 const before=`Visit [${url}](${url}) A \\& B.`;
 const after=before.replace('/old)','/new)');
 expect(preserveMarkdownSource(source,before,after)).toBe(`Visit [${url}](https://example.org/a(b)/new) A & B.\n`);
});


it('maps task text after the checkbox marker even when both contain x', () => {
 const root = document.createElement('div'); root.className = 'mdxeditor-root-contenteditable';
 root.innerHTML = '<ul><li aria-checked="true">x</li></ul>';
 expect(markdownSelectionRange('- [x] x', { selectedText: 'x', paragraph: root.querySelector('li')! }))
  .toEqual({ selected_text: 'x', start: 6, end: 7 });
});

describe('README source preservation', () => {
 const original = [
  '# Project', '', '**[English](README.md)** | **中文**', '',
  '[![macOS](https://example.org/badge?style=flat&logo=apple)](desktop/README.md)', '',
  '- Desktop', '- Enterprise', '', '---', '',
  '| 场景 | 执行 |', '|------|------|', '| Writer | 编辑 |', '', '',
  'https://example.org/video', '', '## Features', '', 'Original paragraph.', '',
  '## Quick start', '', '```bash', 'echo "A & B"', '```', '',
 ].join('\n');
 const normalized = [
  '# Project', '', '[English](README.md) | **中文**', '',
  '![macOS](https://example.org/badge?style=flat\\&logo=apple)', '',
  '* Desktop', '* Enterprise', '', '***', '',
  '| 场景     | 执行 |', '| ------ | -- |', '| Writer | 编辑 |', '',
  '[https://example.org/video](https://example.org/video)', '', '## Features', '', 'Original paragraph.', '',
  '## Quick start', '', '```bash', 'echo "A & B"', '```',
 ].join('\n');

 it('preserves every untouched byte when adding a section, then editing and deleting it', () => {
  const addition = '### Security\n\nNew section.\n\n';
  const exported = normalized.replace('## Quick start', addition + '## Quick start');
  const saved = preserveMarkdownSource(original, normalized, exported);
  expect(saved).toBe(original.replace('## Quick start', addition + '## Quick start'));
  const edited = exported.replace('New section.', '**Updated section.**');
  const savedAgain = preserveMarkdownSource(saved, exported, edited);
  expect(savedAgain).toBe(saved.replace('New section.', '**Updated section.**'));
  expect(preserveMarkdownSource(savedAgain, edited, normalized)).toBe(original);
 });

 it('handles a long README without running a whole-document character diff', () => {
  const from = Array.from({ length: 80 }, (_, i) => `${original}\n## Section ${i}\n\n`).join('');
  const before = Array.from({ length: 80 }, (_, i) => `${normalized}\n\n## Section ${i}\n\n`).join('');
  const after = before.replace('## Section 40', 'New paragraph.\n\n## Section 40');
  // Model the abort on expensive input deterministically, without timing assertions.
  diffBudget.maxChars = 2000;
  expect(preserveMarkdownSource(from, before, after))
   .toBe(from.replace('## Section 40', 'New paragraph.\n\n## Section 40'));
 });

 it('keeps CRLF and blank lines around an edited paragraph', () => {
  const source = original.split('\n').join('\r\n');
  expect(preserveMarkdownSource(source, normalized, normalized.replace('Original paragraph.', 'Edited paragraph.')))
   .toBe(source.replace('Original paragraph.', 'Edited paragraph.'));
 });

 it.each(['lines', 'characters'])('refuses an unsafe save if the %s comparison cannot complete', (phase) => {
  if (phase === 'lines') diffBudget.abortLines = true;
  else diffBudget.maxChars = 0;
  expect(() => preserveMarkdownSource(original, normalized, normalized.replace('Original', 'Edited'))).toThrow();
 });
});
